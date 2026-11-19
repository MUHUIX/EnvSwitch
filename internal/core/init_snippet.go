package core

// 层 1「会话注入」的钩子片段生成与安装（方案 §6.1）。
//
// 五条钩子语义（§6.1，三种 shell 同一套）：
//  1. 取当前目录（PS: (Get-Location).ProviderPath；bash: $PWD；cmd: %CD%）
//  2. 与上次解析的目录相同 → 直接返回（同目录零成本，绝大多数回车走这条）
//  3. 变了 → 纯 shell 查预生成的扁平表 bindings.gen.txt，命中即 PATH = <版本目录> + PATH_BAK
//  4. 撤销语义：从 PATH_BAK 快照重建，绝不在当前 PATH 上叠加（治 §2-L5 膨胀）
//  5. 回车路径零进程创建（不调 envs.exe；§2-L10）
//
// 三条实现纪律（都被 internal/core/init_snippet_test.go 真跑过）：
//   - 只用目标 shell 的内建能力：外部 tr/cut/awk 在坏 PATH 下会让钩子静默失效，且每回车 fork；
//   - 第 4 列 envPairs 是 percent-encoded（契约 v2），读侧必须解码后再注入；
//   - 生成的 shell 文本全英文 —— 项目既有铁律：bat / profile 里的中文按 GBK 解码会乱码。
//
// 诚实边界：cmd 没有 cd 事件，AutoRun 只覆盖「新开窗口的起始目录」，已开窗口不会变
// （Windows 环境块机制，与 envs use 的官方文案同源）。
//
// 安全：写入块一律用 BEGIN/END 注释标记包裹，`envs init --remove` 精确删除自己写的部分；
// 未经 envs init 绝不改用户的 profile / rc / 注册表（§12）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MUHUIX/envswitch/internal/settings"
)

// 块标记（删块靠它精确定位；改动会让用户已装的块认不出来，禁止随意变更措辞）。
const (
	initBlockBeginShell = "# BEGIN EnvSwitch directory-binding hook v1 (managed by: envs init --remove)"
	initBlockEndShell   = "# END EnvSwitch directory-binding hook v1"
	initBlockBeginBat   = "rem BEGIN EnvSwitch directory-binding hook v1 (managed by: envs init --remove cmd)"
	initBlockEndBat     = "rem END EnvSwitch directory-binding hook v1"
	autoRunValueMarker  = "envs-autorun.cmd"

	phTable = "@@TABLE@@"
	phExe   = "@@EXE@@"
)

// InitResult 一次 init / remove 的结果（CLI 与 --json 共用）。
type InitResult struct {
	Shell   string   `json:"shell"`
	Targets []string `json:"targets"`
	Written bool     `json:"written"`
	Changed int      `json:"changed"`
	Skipped []string `json:"skipped,omitempty"`
	Note    string   `json:"note,omitempty"`
	Block   string   `json:"block,omitempty"`
}

// InitExePath 返回要嵌进钩子的 envs.exe 绝对路径。
// §2-L4 附带发现：裸名 envs 会被 cmd 的隐式当前目录抢走（仓库里就有 dev 版），
// 所以钩子里一律内嵌绝对路径。
func InitExePath() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, n := range []string{"envs.exe", "EnvSwitch.exe", "envswitch.exe"} {
			if p := filepath.Join(dir, n); fileExists(p) {
				return p
			}
		}
		return filepath.Join(dir, "envs.exe")
	}
	return filepath.Join(settings.ExeDir(), "envs.exe")
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// PowerShellProfiles 返回两个标准 $PROFILE 路径（5.1 与 7）。
// 本机实测两者都不存在（§6.1），所以 --append 时会新建文件与父目录。
func PowerShellProfiles() []string {
	up := os.Getenv("USERPROFILE")
	if up == "" {
		return nil
	}
	base := filepath.Join(up, "Documents")
	return []string{
		filepath.Join(base, "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(base, "PowerShell", "Microsoft.PowerShell_profile.ps1"),
	}
}

// BashrcPath 返回 ~/.bashrc（Git Bash 的 HOME）。
func BashrcPath() string {
	if h := os.Getenv("HOME"); h != "" {
		return filepath.Join(h, ".bashrc")
	}
	if up := os.Getenv("USERPROFILE"); up != "" {
		return filepath.Join(up, ".bashrc")
	}
	return ""
}

// AutoRunBatPath 是 cmd 侧钩子的落点（EnvSwitch 自己的 home\bin，不动用户文件）。
// AutoRun 只能整行执行，把多行逻辑放这里，注册表值保持一行且不受路径空格影响。
func AutoRunBatPath() string { return filepath.Join(settings.Home(), "bin", "envs-autorun.cmd") }

// InitBlock 生成指定 shell 的钩子块（含 BEGIN/END 标记）。
func InitBlock(shell, exePath string) (string, error) {
	shell = strings.ToLower(strings.TrimSpace(shell))
	if exePath == "" {
		exePath = InitExePath()
	}
	switch shell {
	case "powershell", "pwsh", "ps":
		return psBlock(), nil
	case "bash", "git-bash", "sh":
		return bashBlock(), nil
	case "cmd":
		return cmdBlock(exePath), nil
	}
	return "", W(ErrUsage, "未知 shell %q，可选: powershell / bash / cmd（方案 §12 明确不支持 WSL）", shell)
}

// —— PowerShell 5.1 / 7 ——

// psHookTmpl 正文（@@TABLE@@ 处填扁平表绝对路径）。
const psHookTmpl = `$env:ENVS_BIND_TABLE = '@@TABLE@@'
if (-not $global:EnvSwitchHookInstalled) {
  $global:EnvSwitchHookInstalled = $true
  if (-not $global:EnvSwitchPathBak) { $global:EnvSwitchPathBak = $env:PATH }
  $global:EnvSwitchLastDir = [string]::Empty
  $global:EnvSwitchEnvBak = @{}
  function global:EnvSwitch-CdHook {
    if ($env:ENVS_DISABLE) { return }
    $d = (Get-Location).ProviderPath.TrimEnd('\')
    if ($d -eq $global:EnvSwitchLastDir) { return }
    $global:EnvSwitchLastDir = $d
    $cmp = $d.ToLower()
    $dirs = New-Object System.Collections.Generic.List[string]
    $pairs = @{}
    $best = [string]::Empty
    $tbl = $env:ENVS_BIND_TABLE
    if (Test-Path -LiteralPath $tbl) {
      foreach ($ln in [System.IO.File]::ReadAllLines($tbl)) {
        if ($ln.Length -eq 0 -or $ln[0] -eq '#') { continue }
        $c = $ln -split [char]9
        if ($c.Count -ne 4) { continue }
        $k = $c[0].TrimEnd('\')
        if ($k.Length -lt 3) { continue }
        if (-not ($cmp -eq $k -or $cmp.StartsWith($k + '\'))) { continue }
        if ($best.Length -eq 0) { $best = $k } elseif ($k -ne $best) { break }
        if ($c[2]) { $dirs.Add($c[2]) }
        if ($c[3]) {
          foreach ($p in $c[3] -split '&') {
            $i = $p.IndexOf('=')
            if ($i -gt 0) {
              $pairs[[Uri]::UnescapeDataString($p.Substring(0, $i))] = [Uri]::UnescapeDataString($p.Substring($i + 1))
            }
          }
        }
      }
    }
    $env:PATH = (@($dirs) + $global:EnvSwitchPathBak) -join ';'
    foreach ($k in @($global:EnvSwitchEnvBak.Keys)) {
      if (-not $pairs.ContainsKey($k)) {
        $old = $global:EnvSwitchEnvBak[$k]
        if ($null -eq $old) { Remove-Item -LiteralPath ("Env:" + $k) -ErrorAction SilentlyContinue } else { Set-Item -LiteralPath ("Env:" + $k) -Value $old }
        $global:EnvSwitchEnvBak.Remove($k) | Out-Null
      }
    }
    foreach ($k in $pairs.Keys) {
      if (-not $global:EnvSwitchEnvBak.ContainsKey($k)) {
        $cur = Get-Item -LiteralPath ("Env:" + $k) -ErrorAction SilentlyContinue
        if ($null -eq $cur) { $global:EnvSwitchEnvBak[$k] = $null } else { $global:EnvSwitchEnvBak[$k] = $cur.Value }
      }
      Set-Item -LiteralPath ("Env:" + $k) -Value $pairs[$k]
    }
  }
  $__envsOldPrompt = $null
  if (Test-Path Function:prompt) { $__envsOldPrompt = (Get-Item Function:prompt).ScriptBlock }
  function global:prompt {
    EnvSwitch-CdHook
    if ($__envsOldPrompt) { & $__envsOldPrompt } else { "PS $($executionContext.SessionState.Path.CurrentLocation)$('>' * ($nestedPromptLevel + 1)) " }
  }
}
`

func psBlock() string {
	return initBlockBeginShell + "\r\n" +
		"# Directory-level version binding for EnvSwitch (layer 1: session injection).\r\n" +
		"# Resolve only on directory change; rebuild PATH from the snapshot so it cannot grow.\r\n" +
		windowsToCRLF(strings.ReplaceAll(psHookTmpl, phTable, psQuote(GenTablePath()))) +
		initBlockEndShell + "\r\n"
}

func windowsToCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// —— Git Bash / MSYS ——

// bashHookTmpl 正文：注入必须是 /c/... + ':' 形态（§2-L7），只用 bash 内建。
const bashHookTmpl = `# Flat table is read as-is; PATH is rebuilt from the snapshot so it never grows (L5).
# Only bash builtins are used: external tr/cut would fail silently on a broken PATH
# and would fork on every prompt (L10 cost budget).
ENVS_BIND_TABLE='@@TABLE@@'
if [ -z "${ENVS_PATH_BAK+x}" ]; then ENVS_PATH_BAK="$PATH"; ENVS_LAST_CMP=""; fi
declare -gA ENVS_ENV_BAK 2>/dev/null || true
envs_win_to_posix() {
  local p="${1//\\//}" d
  case "$p" in
    ?:[/]*) d="${p:0:1}"; d="${d,,}"; printf '/%s%s' "$d" "${p:2}" ;;
    *) printf '%s' "$p" ;;
  esac
}
envs_pct_decode() { printf '%b' "${1//%/\\x}"; }
envs_apply_env() {
  local pair k v IFS='&'
  local -A new=()
  for pair in $1; do
    [ -z "$pair" ] && continue
    k="$(envs_pct_decode "${pair%%=*}")"
    v="$(envs_pct_decode "${pair#*=}")"
    [ -z "$k" ] && continue
    new["$k"]="$v"
    if [ -z "${ENVS_ENV_BAK[$k]+x}" ]; then ENVS_ENV_BAK[$k]="${!k-}"; fi
    export "$k=$v"
  done
  for k in ${!ENVS_ENV_BAK[@]}; do
    if [ -z "${new[$k]+x}" ]; then
      if [ -n "${ENVS_ENV_BAK[$k]}" ]; then export "$k=${ENVS_ENV_BAK[$k]}"; else unset "$k"; fi
      unset "ENVS_ENV_BAK[$k]"
    fi
  done
}
envs_cd_hook() {
  [ -n "${ENVS_DISABLE:-}" ] && return 0
  local w lc best dirs k tool dir pair p
  w="$(pwd -W 2>/dev/null || pwd)"
  w="${w%[\\/]}"
  lc="${w//\//\\}"
  lc="${lc,,}"
  [ "$lc" = "$ENVS_LAST_CMP" ] && return 0
  ENVS_LAST_CMP="$lc"
  best=""; dirs=""; ev=""
  if [ -f "$ENVS_BIND_TABLE" ]; then
    while IFS=$'@@TAB@@' read -r k tool dir pair; do
      case "$k" in ''|'#'*) continue ;; esac
      k="${k%\\}"
      case "$lc" in
        "$k"|"$k"\\*) ;;
        *) continue ;;
      esac
      if [ -z "$best" ]; then best="$k"; elif [ "$k" != "$best" ]; then break; fi
      p="$(envs_win_to_posix "$dir")"
      case ":$dirs:" in *":$p:"*) ;; *) dirs="${dirs:+$dirs:}$p" ;; esac
      [ -n "$pair" ] && ev="$pair"
    done < "$ENVS_BIND_TABLE"
  fi
  if [ -n "$dirs" ]; then export PATH="$dirs:$ENVS_PATH_BAK"; else export PATH="$ENVS_PATH_BAK"; fi
  envs_apply_env "$ev"
  return 0
}
case ":${PROMPT_COMMAND:-}:" in *envs_cd_hook*) ;; *) PROMPT_COMMAND="envs_cd_hook${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;; esac
`

func bashBlock() string {
	return initBlockBeginShell + "\n" +
		"# Directory-level version binding for EnvSwitch (layer 1: session injection).\n" +
		strings.ReplaceAll(strings.ReplaceAll(bashHookTmpl, phTable, shQuote(WindowsPathToPosix(GenTablePath()))), "@@TAB@@", "\\t") +
		initBlockEndShell + "\n"
}

// —— cmd.exe（AutoRun）——

// cmdHookTmpl 由 envs init cmd --autorun 写成 home\bin\envs-autorun.cmd。
// 铁律（§6.2）：不用 IF (…) ELSE (…) 括号块；提示语英文；绝对路径内嵌。
const cmdHookTmpl = `@echo off
if defined ENVS_BIND_INIT goto :envs_end
set "ENVS_BIND_INIT=1"
if defined ENVS_DISABLE goto :envs_end
set "ENVS_EXE=@@EXE@@"
set "ENVS_BIND_TABLE=@@TABLE@@"
if not exist "%ENVS_EXE%" goto :envs_end
for /f "usebackq delims=" %%i in (` + "`" + `"%ENVS_EXE%" __bind-resolve --shell cmd` + "`" + `) do %%i
:envs_end
`

func cmdBlock(exePath string) string {
	body := strings.ReplaceAll(cmdHookTmpl, phExe, exePath)
	body = strings.ReplaceAll(body, phTable, GenTablePath())
	return "@echo off\r\n" + initBlockBeginBat + "\r\n" +
		"rem Layer-1 hook for cmd.exe. cmd has no cd event, so this covers the\r\n" +
		"rem start directory of NEW windows only; already open windows do not change.\r\n" +
		"rem One envs.exe call per new window is the only process it spawns (L10).\r\n" +
		windowsToCRLF(strings.TrimPrefix(body, "@echo off\n")) +
		initBlockEndBat + "\r\n"
}

func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }
func shQuote(s string) string { return strings.ReplaceAll(s, "'", `'\''`) }

// WindowsPathToPosix E:\x\y → /e/x/y（§2-L7：Git Bash 只认 /c/... + ':' 形态）。
func WindowsPathToPosix(p string) string {
	s := strings.ReplaceAll(p, `\`, "/")
	if len(s) >= 2 && s[1] == ':' {
		return "/" + strings.ToLower(s[:1]) + s[2:]
	}
	return s
}

// InstallInit 生成并按需安装钩子。
// doWrite=false 只回文本（默认安全：绝不偷偷改启动文件，§12）；
// cmd 的写入需要 autorun=true（写 home\bin\envs-autorun.cmd + HKCU AutoRun）。
func InstallInit(shell, exePath string, doWrite, autorun bool) (InitResult, error) {
	shell = strings.ToLower(strings.TrimSpace(shell))
	if exePath == "" {
		exePath = InitExePath()
	}
	switch shell {
	case "powershell", "pwsh", "ps":
		targets := PowerShellProfiles()
		if len(targets) == 0 {
			return InitResult{}, W(ErrUsage, "找不到 USERPROFILE，无法定位 $PROFILE")
		}
		return installFileBlock(shell, targets, InitBlockFor(shell, exePath), doWrite)
	case "bash", "git-bash", "sh":
		p := BashrcPath()
		if p == "" {
			return InitResult{}, W(ErrUsage, "找不到 HOME / USERPROFILE，无法定位 ~/.bashrc")
		}
		return installFileBlock(shell, []string{p}, InitBlockFor(shell, exePath), doWrite)
	case "cmd":
		return installCmdAutoRun(exePath, doWrite && autorun)
	}
	return InitResult{}, W(ErrUsage, "未知 shell %q，可选: powershell / bash / cmd", shell)
}

// InitBlockFor 是 InitBlock 的「只要文本」版本（CLI 需要同时拿文本与目标路径）。
func InitBlockFor(shell, exePath string) string {
	b, err := InitBlock(shell, exePath)
	if err != nil {
		return ""
	}
	return b
}

func installFileBlock(shell string, targets []string, block string, doWrite bool) (InitResult, error) {
	res := InitResult{Shell: shell, Targets: targets, Block: block}
	if block == "" {
		return res, W(ErrUsage, "钩子生成失败")
	}
	if !doWrite {
		res.Note = "未写入（默认只预览）。加 --append 才动 profile；卸载：envs init --remove " + shell
		return res, nil
	}
	for _, t := range targets {
		if _, err := os.Stat(filepath.Dir(t)); err != nil {
			res.Skipped = append(res.Skipped, filepath.Dir(t)+"（父目录不存在；PowerShell 首次自建 profile 后会存在，或手动建一次）")
			continue
		}
		if err := upsertBlock(t, block, initBlockBeginShell, initBlockEndShell); err != nil {
			res.Skipped = append(res.Skipped, t+"（"+err.Error()+"）")
			continue
		}
		res.Changed++
	}
	res.Written = res.Changed > 0
	res.Note = "已打开的终端不会变化，请新开终端（Windows 环境块机制，§7.2 文案）"
	return res, nil
}

func installCmdAutoRun(exePath string, doWrite bool) (InitResult, error) {
	bat := AutoRunBatPath()
	value := `if not defined ENVS_BIND_INIT call "` + bat + `"`
	res := InitResult{Shell: "cmd", Targets: []string{bat, `HKCU\` + cmdProcessorKey + `\AutoRun`}, Block: value}
	cur, had, err := winAutoRunGet()
	if err != nil {
		return res, W(ErrPerm, "读取 cmd AutoRun 失败：%v", err)
	}
	if had && !strings.Contains(cur, autoRunValueMarker) {
		return res, W(ErrUsage, "已存在自定义 AutoRun（%q）。为了不覆盖你的设置，请先手动合并或清空该值再执行 envs init cmd --autorun", cur)
	}
	if !doWrite {
		res.Block = "HKCU\\" + cmdProcessorKey + " 的 AutoRun 值：\r\n  " + value +
			"\r\n\r\n并生成 " + bat + "：\r\n" + cmdBlock(exePath)
		res.Note = "未写入（默认只预览）。加 --autorun 才写 home\\bin\\envs-autorun.cmd 与 HKCU AutoRun；卸载：envs init --remove cmd"
		return res, nil
	}
	if err := writeFileAtomic(bat, []byte(cmdBlock(exePath))); err != nil {
		return res, W(ErrPerm, "写 %s 失败：%v", bat, err)
	}
	if err := winAutoRunSet(value, false); err != nil {
		return res, W(ErrPerm, "写 AutoRun 失败：%v", err)
	}
	res.Changed = 2
	res.Written = true
	res.Note = "cmd 只在「新开窗口的起始目录」解析（无 cd 事件）；卸载：envs init --remove cmd"
	return res, nil
}

// upsertBlock 幂等写入：已有本工具写的块 → 原地替换；没有 → 追加到文件末尾。
func upsertBlock(path, block, begin, end string) error {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(old)
	if s := strings.Index(content, begin); s >= 0 {
		e := strings.Index(content[s:], end)
		if e < 0 {
			return fmt.Errorf("块结束标记缺失，为避免误删请手工处理 %s", path)
		}
		rest := content[s+e+len(end):]
		return os.WriteFile(path, []byte(content[:s]+strings.TrimLeft(block+rest, "\r\n")), 0o644)
	}
	sep := ""
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		sep = "\n"
	}
	return os.WriteFile(path, []byte(content+sep+"\n"+block), 0o644)
}

// RemoveInit 精确删除自己写入的块（§6.1 --remove）。shell 传 "*" / "all" 清三种 shell。
func RemoveInit(shell string) (InitResult, error) {
	shell = strings.ToLower(strings.TrimSpace(shell))
	if shell == "" {
		return InitResult{}, W(ErrUsage, "remove 需要指定 shell：powershell / bash / cmd / *")
	}
	if shell == "*" || shell == "all" {
		var merged InitResult
		merged.Shell = "*"
		var firstErr error
		for _, sh := range []string{"powershell", "bash", "cmd"} {
			r, err := RemoveInit(sh)
			merged.Targets = append(merged.Targets, r.Targets...)
			merged.Changed += r.Changed
			merged.Skipped = append(merged.Skipped, r.Skipped...)
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		merged.Written = merged.Changed > 0
		return merged, firstErr
	}
	switch shell {
	case "powershell", "pwsh", "ps":
		return removeFileBlock(shell, PowerShellProfiles())
	case "bash", "git-bash", "sh":
		p := BashrcPath()
		if p == "" {
			return InitResult{}, W(ErrUsage, "找不到 HOME / USERPROFILE")
		}
		return removeFileBlock(shell, []string{p})
	case "cmd":
		bat := AutoRunBatPath()
		res := InitResult{Shell: "cmd", Targets: []string{bat, `HKCU\` + cmdProcessorKey + `\AutoRun`}}
		cur, had, err := winAutoRunGet()
		if err != nil {
			return res, err
		}
		if had && strings.Contains(cur, autoRunValueMarker) {
			if err := winAutoRunSet("", true); err != nil {
				return res, err
			}
			res.Changed++
		} else if had {
			res.Skipped = append(res.Skipped, "AutoRun 里不是本工具的值，未改动")
		}
		if err := os.Remove(bat); err == nil {
			res.Changed++
		} else if !os.IsNotExist(err) {
			res.Skipped = append(res.Skipped, bat+"（"+err.Error()+"）")
		}
		res.Written = res.Changed > 0
		res.Note = "PATH / junction / 用户环境变量一概未碰（§11 回滚半径）"
		return res, nil
	}
	return InitResult{}, W(ErrUsage, "未知 shell %q", shell)
}

func removeFileBlock(shell string, targets []string) (InitResult, error) {
	res := InitResult{Shell: shell, Targets: targets}
	for _, t := range targets {
		old, err := os.ReadFile(t)
		if os.IsNotExist(err) {
			res.Skipped = append(res.Skipped, t+"（文件不存在）")
			continue
		}
		if err != nil {
			res.Skipped = append(res.Skipped, t+"（"+err.Error()+"）")
			continue
		}
		content := string(old)
		s := strings.Index(content, initBlockBeginShell)
		if s < 0 {
			res.Skipped = append(res.Skipped, t+"（没有本工具写入的块）")
			continue
		}
		rest := content[s:]
		e := strings.Index(rest, initBlockEndShell)
		if e < 0 {
			res.Skipped = append(res.Skipped, t+"（块结束标记缺失，为避免误删请手工处理）")
			continue
		}
		after := strings.TrimPrefix(strings.TrimPrefix(rest[e+len(initBlockEndShell):], "\r\n"), "\n")
		if err := os.WriteFile(t, []byte(content[:s]+after), 0o644); err != nil {
			return res, err
		}
		res.Changed++
	}
	res.Written = res.Changed > 0
	return res, nil
}

// BindResolveSetLines 把一次解析写成 cmd 的 set 行（隐藏命令 __bind-resolve 的输出）。
// 只发经过安全过滤的值：含 % & " ^ | < > 换行 的一律整条不发并回传原因
// （§2-L9 注入面 / §10「bat 语法炸」行）。
func BindResolveSetLines(r *BindingResolve) (lines []string, skipped string) {
	if r == nil {
		return nil, ""
	}
	dirs := r.InjectDirs()
	if len(dirs) == 0 && len(r.Env) == 0 {
		return nil, ""
	}
	base := os.Getenv("PATH")
	merged := strings.Join(append(append([]string{}, dirs...), base), ";")
	if !CmdSafeValue(merged) {
		return nil, "PATH 含 cmd 元字符（% & \" ^ | < >），已放弃本次注入以避免解析风险"
	}
	lines = append(lines, `set "PATH=`+merged+`"`)
	for _, k := range sortedKeys(r.Env) {
		v := r.Env[k]
		if !CmdSafeValue(k) || !CmdSafeValue(v) {
			skipped = fmt.Sprintf("env %s 含 cmd 元字符，已跳过该条", k)
			continue
		}
		lines = append(lines, `set "`+k+`=`+v+`"`)
	}
	return lines, skipped
}

// CmdSafeValue bat 侧可安全拼接的值（延迟展开之外的第二道闸）。
func CmdSafeValue(s string) bool {
	return !strings.ContainsAny(s, "%&\"^|<>\r\n`!")
}
