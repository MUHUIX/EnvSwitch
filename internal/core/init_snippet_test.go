package core

// 层 1 钩子片段（§6.1）的单测：生成内容的语义齐备性 + 写块/删块的可逆性。
// 全程只碰 t.TempDir()：不读不写真实 $PROFILE / ~/.bashrc / HKCU AutoRun。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitBlockPowerShellSemantics(t *testing.T) {
	home, _ := bindEnv(t)
	block, err := InitBlock("powershell", filepath.Join(home, "envs.exe"))
	mustNoErr(t, err, "生成 PS 块")
	if !strings.HasPrefix(block, initBlockBeginShell) || !strings.Contains(block, initBlockEndShell) {
		t.Fatal("PS 块必须用 BEGIN/END 标记包裹（§6.1 精确删除的前提）")
	}
	want := map[string]string{
		"取当前目录（§6.1-1）":    "(Get-Location).ProviderPath",
		"同目录零成本（§6.1-2）":   "EnvSwitchLastDir",
		"查预生成扁平表（§6.1-3）":  "$env:ENVS_BIND_TABLE",
		"从快照重建（§6.1-4）":    "EnvSwitchPathBak",
		"分隔符边界（§2-L12）":    "$k + '\\'",
		"最长前缀先命中":          "break",
		"逃生门（§10）":         "ENVS_DISABLE",
		"env 覆盖（§4.4）":     "EnvSwitchEnvBak",
		"percent 解码（契约v2）": "UnescapeDataString",
	}
	for name, needle := range want {
		if !strings.Contains(block, needle) {
			t.Errorf("%s：PS 块缺 %q", name, needle)
		}
	}
	// §6.1-5：回车路径零进程创建 —— PS 钩子里不得调用 envs.exe
	if strings.Contains(block, "__bind-resolve") || strings.Contains(block, "& $env:ENVS_EXE") {
		t.Error("PS 钩子不得在回车路径上拉起子进程（§2-L10）")
	}
	// 表路径必须绝对且写死（不接受相对/裸名，§9-5）
	if !strings.Contains(block, home) {
		t.Errorf("PS 块应内嵌 home 绝对路径：%s", block)
	}
	assertPowerShellParses(t, block)
}

func TestInitBlockBashSemantics(t *testing.T) {
	home, _ := bindEnv(t)
	block, err := InitBlock("bash", filepath.Join(home, "envs.exe"))
	mustNoErr(t, err, "生成 bash 块")
	want := map[string]string{
		"BEGIN/END 标记":         initBlockBeginShell,
		"取 $PWD 并转 Windows 形态": "pwd -W",
		"cmpKey 小写（纯内建）":       "${lc,,}",
		"制表符切列":                "IFS=$'\\t'",
		"percent 解码（契约v2 第4列）": "envs_pct_decode",
		"边界匹配（§2-L12）":         "\"$k\"|\"$k\"",
		"快照重建（§6.1-4）":         "ENVS_PATH_BAK",
		"/c/ 形态（§2-L7）":        "envs_win_to_posix",
		"冒号分隔（§2-L7）":          "$dirs:$ENVS_PATH_BAK",
		"同目录零成本":               "ENVS_LAST_CMP",
		"逃生门":                  "ENVS_DISABLE",
		"PROMPT_COMMAND 挂钩":    "PROMPT_COMMAND",
		"不重复挂钩":                `*envs_cd_hook*`,
	}
	for name, needle := range want {
		if !strings.Contains(block, needle) {
			t.Errorf("%s：bash 块缺 %q", name, needle)
		}
	}
	if strings.Contains(block, "__bind-resolve") {
		t.Error("bash 钩子不得在回车路径上拉起子进程")
	}
	// 表路径要落成 /x/... 形态，Git Bash 才读得到
	if !strings.Contains(block, "ENVS_BIND_TABLE='/") {
		t.Errorf("bash 侧表路径必须是 POSIX 绝对形态：%s", firstLineOf(block))
	}
	assertBashParses(t, block)
}

func TestInitBlockCmdAutorun(t *testing.T) {
	home, _ := bindEnv(t)
	exe := filepath.Join(home, "bin", "envs.exe")
	block, err := InitBlock("cmd", exe)
	mustNoErr(t, err, "生成 cmd 块")
	if !strings.Contains(block, initBlockBeginBat) || !strings.Contains(block, initBlockEndBat) {
		t.Fatal("cmd 块必须用 BEGIN/END rem 标记包裹")
	}
	for _, needle := range []string{"@echo off", "if defined ENVS_BIND_INIT goto :envs_end",
		"usebackq", "__bind-resolve --shell cmd", exe} {
		if !strings.Contains(block, needle) {
			t.Errorf("cmd 块缺 %q", needle)
		}
	}
	// §6.2 铁律：不用 IF (…) ELSE (…) 括号块
	if strings.Contains(block, ") ELSE (") || strings.Contains(block, "if (") {
		t.Error("cmd 块不得用括号块 IF")
	}
	// 绝对路径内嵌（§9-5：裸名 envs 会被 cmd 隐式当前目录抢走）：
	// 调用行只能出现 %ENVS_EXE%，不得出现裸名 envs 调用
	if !strings.Contains(block, "in (`\"%ENVS_EXE%\" __bind-resolve") {
		t.Errorf("cmd 块必须用内嵌绝对路径变量调用 helper：%s", block)
	}
	if strings.Contains(block, "in (`envs ") || strings.Contains(block, "in (`\"envs\" ") {
		t.Error("cmd 块用了裸名 envs（会被当前目录的 dev 版抢位，§9-5）")
	}
	// 实跑一次：exe 不存在 → 必须安静退出且不改动 PATH（§10「helper not found」）
	bat := filepath.Join(home, "autorun.bat")
	mustNoErr(t, os.WriteFile(bat, []byte(block), 0o755), "写临时 bat")
	if _, err := exec.LookPath("cmd.exe"); err != nil {
		if _, e := exec.LookPath("cmd"); e != nil {
			t.Skip("本机取不到 cmd.exe，bat 实跑未验")
		}
	}
	out, err := exec.Command("cmd", "/c", bat).CombinedOutput()
	if err != nil {
		t.Fatalf("临时 bat 实跑失败：%v\n%s", err, out)
	}
	if strings.Contains(strings.ToLower(string(out)), "syntax error") ||
		strings.Contains(string(out), "命令语法不正确") {
		t.Fatalf("bat 语法错误：\n%s", out)
	}
}

func TestInitInstallAndRemoveAreReversible(t *testing.T) {
	home, _ := bindEnv(t)
	profile := filepath.Join(home, "Documents", "WindowsPowerShell")
	mustNoErr(t, os.MkdirAll(profile, 0o755), "建 profile 目录")
	psFile := filepath.Join(profile, "Microsoft.PowerShell_profile.ps1")
	userText := "Set-PSReadLineOption -PredictionSource History" + "\r\n"
	mustNoErr(t, os.WriteFile(psFile, []byte(userText), 0o644), "写用户既有 profile")

	// 指到临时 HOME 下，绝不影响真实 $PROFILE
	t.Setenv("USERPROFILE", home)

	res, err := InstallInit("powershell", filepath.Join(home, "envs.exe"), true, false)
	mustNoErr(t, err, "安装钩子")
	if res.Changed == 0 {
		t.Fatalf("钩子未写入：%+v", res)
	}
	data, err := os.ReadFile(psFile)
	mustNoErr(t, err, "读 profile")
	got := string(data)
	if !strings.Contains(got, initBlockBeginShell) || !strings.Contains(got, userText) {
		t.Fatal("写入后必须同时含钩子块与用户原内容")
	}
	// 幂等：再装一次不得出现两个块
	if _, err := InstallInit("powershell", filepath.Join(home, "envs.exe"), true, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(psFile)
	if strings.Count(string(data), initBlockBeginShell) != 1 {
		t.Fatal("重复 init 必须替换而不是追加第二个块")
	}
	// 删除：块没了，用户原内容一个字不少
	if _, err := RemoveInit("powershell"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(psFile)
	if strings.Contains(string(data), initBlockBeginShell) {
		t.Fatal("remove 后块必须消失")
	}
	if !strings.Contains(string(data), "Set-PSReadLineOption") {
		t.Fatalf("remove 不得吃掉用户自己的内容：%q", data)
	}
}

func TestInitInstallCmdDefaultsToPreviewOnly(t *testing.T) {
	home, _ := bindEnv(t)
	exe := filepath.Join(home, "envs.exe")

	// 打桩注册表读写：本测试绝不能碰真实 HKCU Command Processor\AutoRun
	var written string
	removed := false
	oldGet, oldSet := winAutoRunGet, winAutoRunSet
	defer func() { winAutoRunGet, winAutoRunSet = oldGet, oldSet }()
	winAutoRunGet = func() (string, bool, error) { return written, written != "", nil }
	winAutoRunSet = func(v string, remove bool) error {
		if remove {
			removed, written = true, ""
			return nil
		}
		written = v
		return nil
	}

	// 默认（不带 --autorun）：只预览，不写注册表、不落 bat
	res, err := InstallInit("cmd", exe, false, false)
	mustNoErr(t, err, "预览 cmd init")
	if written != "" || res.Written {
		t.Fatalf("未加 --autorun 不得写任何东西：%q %+v", written, res)
	}
	if !strings.Contains(res.Block, "envs-autorun.cmd") {
		t.Fatalf("预览要给出将要写入的 AutoRun 值：%q", res.Block)
	}
	if _, err := os.Stat(AutoRunBatPath()); !os.IsNotExist(err) {
		t.Fatal("预览模式不得生成 envs-autorun.cmd")
	}

	// 带 --autorun：写 bat + 写值（值在打桩里，bat 在临时 home 里）
	res, err = InstallInit("cmd", exe, true, true)
	mustNoErr(t, err, "安装 cmd 钩子")
	if written == "" || !res.Written {
		t.Fatalf("应已写入 AutoRun：%q %+v", written, res)
	}
	if _, err := os.Stat(AutoRunBatPath()); err != nil {
		t.Fatalf("应已生成 %s：%v", AutoRunBatPath(), err)
	}
	// 已有别的 AutoRun 值时拒绝覆盖（不偷偷改用户启动设置，§12）
	written = `doskey /macros`
	_, err = InstallInit("cmd", exe, true, true)
	if err == nil || !strings.Contains(err.Error(), "AutoRun") {
		t.Fatalf("已有自定义 AutoRun 必须拒绝而不是覆盖：%v", err)
	}
	written = ""
	// 卸载：值删除 + bat 删除
	if _, err := InstallInit("cmd", exe, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveInit("cmd"); err != nil {
		t.Fatal(err)
	}
	if !removed || written != "" {
		t.Fatalf("remove 必须删掉自己写的 AutoRun 值：%v %q", removed, written)
	}
	if _, err := os.Stat(AutoRunBatPath()); !os.IsNotExist(err) {
		t.Fatal("remove 必须删掉自己生成的 bat")
	}
}

func TestInitUnknownShellRejected(t *testing.T) {
	if _, err := InitBlock("wsl", "x"); err == nil {
		t.Fatal("方案 §12 明确不支持 WSL，init 必须拒绝")
	}
	if _, err := InstallInit("powershell", "", false, false); err != nil {
		t.Fatalf("powershell 预览不该报错：%v", err)
	}
	if _, err := RemoveInit(""); err == nil {
		t.Fatal("remove 不给 shell 必须报参数错")
	}
}

func TestWindowsPathToPosix(t *testing.T) {
	for _, tc := range [][2]string{
		{`C:\Windows\system32`, "/c/Windows/system32"},
		{`E:\idea work\EnvSwitch`, "/e/idea work/EnvSwitch"},
		{`x:\A`, "/x/A"},
		{`already/posix`, "already/posix"},
	} {
		if got := WindowsPathToPosix(tc[0]); got != tc[1] {
			t.Errorf("WindowsPathToPosix(%q) = %q，期望 %q", tc[0], got, tc[1])
		}
	}
}

func TestBindResolveSetLinesSafety(t *testing.T) {
	home, s := bindEnv(t)
	nodeBin := bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"},
		Env: map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096 --openssl-legacy-provider"}}}
	r := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}})
	lines, skipped := BindResolveSetLines(r)
	if skipped != "" {
		t.Fatalf("干净值不该被挡：%s", skipped)
	}
	if len(lines) != 2 {
		t.Fatalf("应发 PATH + NODE_OPTIONS 两行，实得 %q", lines)
	}
	if !strings.HasPrefix(lines[0], `set "PATH=`+nodeBin) {
		t.Errorf("第一行必须前置版本目录：%q", lines[0])
	}
	if lines[1] != `set "NODE_OPTIONS=--max-old-space-size=4096 --openssl-legacy-provider"` {
		t.Errorf("env 行不符：%q", lines[1])
	}
	// §2-L9 注入面：值里放 zz" & echo [PWNED] & " 必须整条不发
	t.Setenv("PATH", `C:\Windows`+`" & echo [PWNED] & "`)
	lines2, skipped2 := BindResolveSetLines(r)
	if len(lines2) != 0 || skipped2 == "" {
		t.Fatalf("含 bat 元字符的 PATH 必须放弃注入而不是拼进去：%q / %q", lines2, skipped2)
	}
	if strings.Contains(strings.Join(lines2, "\n"), "PWNED") {
		t.Fatal("注入面失守")
	}
}

// —— 语法闸门：生成的片段必须能被目标 shell 解析（不执行、不落用户文件）——

func assertPowerShellParses(t *testing.T, block string) {
	t.Helper()
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("本机无 powershell，PS 片段语法未验")
	}
	f := filepath.Join(t.TempDir(), "snippet.ps1")
	mustNoErr(t, os.WriteFile(f, []byte(block), 0o644), "写临时 ps1")
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$e=$null;[System.Management.Automation.Language.Parser]::ParseFile('"+f+"',[ref]$null,[ref]$e)|Out-Null;"+
			"if($e.Count){$e|ForEach-Object{$_.Message};exit 1}")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("生成的 PS 片段语法不过关：%v\n%s", err, out)
	}
}

func assertBashParses(t *testing.T, block string) {
	t.Helper()
	bin := ""
	cands := []string{"bash", `C:\Program Files\Git\bin\bash.exe`, `C:\Program Files\Git\usr\bin\bash.exe`}
	// 便携安装（如 D:\Git\cmd\git.exe）：从 git 位置推出同根的 bash，不写死盘符
	if g, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(g))
		cands = append(cands, filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "bash.exe"))
	}
	for _, c := range cands {
		if p, err := exec.LookPath(c); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		t.Skip("本机取不到 bash，bash 片段语法未验")
	}
	f := filepath.Join(t.TempDir(), "snippet.sh")
	mustNoErr(t, os.WriteFile(f, []byte(block), 0o755), "写临时 sh")
	out, err := exec.Command(bin, "-n", f).CombinedOutput() // -n 只解析不执行
	if err != nil {
		t.Fatalf("生成的 bash 片段语法不过关：%v\n%s", err, out)
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// —— 层 1 钩子的真跑通验证（子进程 dot-source 生成的块；全程临时目录，不碰真实 profile）——

// layerOneFixture 造一个「home\store 里有 16.20.2、proj 绑了它并带 env 覆盖」的场景，
// 返回 (临时 home, 绑定目录, 版本目录, 生成的钩子块)。
func layerOneFixture(t *testing.T, shell string) (string, string, string, string) {
	t.Helper()
	home, s := bindEnv(t)
	nodeBin := bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16.20.2"},
		Env: map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096 --openssl-legacy-provider"}}}
	if _, err := SaveBindings(b, []Adapter{&bindAdapter{id: "node"}}, s); err != nil {
		t.Fatal(err)
	}
	block, err := InitBlock(shell, filepath.Join(home, "envs.exe"))
	mustNoErr(t, err, "生成块")
	return home, proj, nodeBin, block
}

// TestLayerOnePowerShellHookActuallyWorks 真跑 §6.1 的五条语义：
// 同目录零成本、命中前置、从快照重建（离开自动撤销）、env 覆盖、回车路径不拉子进程。
func TestLayerOnePowerShellHookActuallyWorks(t *testing.T) {
	home, proj, nodeBin, block := layerOneFixture(t, "powershell")
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("本机无 powershell")
	}
	ps1 := filepath.Join(t.TempDir(), "hook.ps1")
	mustNoErr(t, os.WriteFile(ps1, []byte(block), 0o644), "写临时 profile")
	other := filepath.Join(home, "outside")
	mustNoErr(t, os.MkdirAll(other, 0o755), "建对照目录")
	// 子进程里：dot-source 块 → 手动跑一次 CdHook（等价于提示符触发）→ 报出 PATH 头与 env
	script := fmt.Sprintf(`
$env:PATH = 'PRESET: marcador'
. '%s'
Set-Location -LiteralPath '%s'
EnvSwitch-CdHook
$head = ($env:PATH -split ';')[0]
"HEAD=$head"
"OPTS=$env:NODE_OPTIONS"
EnvSwitch-CdHook
"AGAIN=$(($env:PATH -split ';').Count)"
Set-Location -LiteralPath '%s'
EnvSwitch-CdHook
"BACK=$env:PATH"
"BACKOPTS=[$env:NODE_OPTIONS]"
`, ps1, proj, other)
	sf := filepath.Join(t.TempDir(), "run.ps1")
	mustNoErr(t, os.WriteFile(sf, []byte(script), 0o644), "写脚本")
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", sf).CombinedOutput()
	if err != nil {
		t.Fatalf("子进程跑钩子失败：%v\n%s", err, out)
	}
	got := string(out)
	kv := map[string]string{}
	for _, ln := range strings.Split(got, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(ln), "="); ok {
			kv[k] = v
		}
	}
	if !strings.EqualFold(kv["HEAD"], nodeBin) {
		t.Errorf("命中后 PATH 首位应是版本目录，实得 %q（期望 %q）\n%s", kv["HEAD"], nodeBin, got)
	}
	if kv["OPTS"] != "--max-old-space-size=4096 --openssl-legacy-provider" {
		t.Errorf("env 覆盖没生效：%q", kv["OPTS"])
	}
	if kv["AGAIN"] != "2" {
		t.Errorf("同目录重复触发不得叠加 PATH（应仍是 注入+原值 两段），实得 %q", kv["AGAIN"])
	}
	if kv["BACK"] != "PRESET: marcador" {
		t.Errorf("离开绑定目录必须从快照重建回原值，实得 %q", kv["BACK"])
	}
	if kv["BACKOPTS"] != "[]" {
		t.Errorf("离开后 env 覆盖要撤销，实得 %q", kv["BACKOPTS"])
	}
}

// TestLayerOneBashHookActuallyWorks 真跑 Git Bash 侧：注入条目必须是 /x/... + ':' 形态（§2-L7）。
func TestLayerOneBashHookActuallyWorks(t *testing.T) {
	_, proj, nodeBin, block := layerOneFixture(t, "bash")
	bin := ""
	if p, err := exec.LookPath("bash"); err == nil {
		bin = p
	} else if g, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(g))
		for _, c := range []string{filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "bash.exe")} {
			if fileExists(c) {
				bin = c
				break
			}
		}
	}
	if bin == "" {
		t.Skip("本机取不到 bash，层1 bash 真跑未验")
	}
	sh := filepath.Join(t.TempDir(), "hook.sh")
	mustNoErr(t, os.WriteFile(sh, []byte(block), 0o755), "写临时 sh")
	other := t.TempDir()
	script := fmt.Sprintf(`
export PATH='PRESETBIN'
. '%s'
cd '%s' || exit 9
envs_cd_hook
echo "HEAD=${PATH%%:*}"
echo "OPTS=$NODE_OPTIONS"
envs_cd_hook
n=0; for x in ${PATH//:/ }; do n=$((n+1)); done; echo "AGAIN=$n"
cd '%s' || exit 9
envs_cd_hook
echo "BACK=$PATH"
echo "BACKOPTS=[$NODE_OPTIONS]"
`, sh, WindowsPathToPosix(proj), WindowsPathToPosix(other))
	sf := filepath.Join(t.TempDir(), "run.sh")
	mustNoErr(t, os.WriteFile(sf, []byte(script), 0o755), "写脚本")
	out, err := exec.Command(bin, "--norc", "--noprofile", sf).CombinedOutput()
	if err != nil {
		t.Fatalf("bash 跑钩子失败：%v\n%s", err, out)
	}
	kv := map[string]string{}
	for _, ln := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(ln), "="); ok {
			kv[k] = v
		}
	}
	wantHead := strings.ToLower(WindowsPathToPosix(nodeBin))
	if strings.ToLower(kv["HEAD"]) != wantHead {
		t.Errorf("bash 侧 PATH 首位应是 /x/... 形态的版本目录：实得 %q 期望 %q\n%s", kv["HEAD"], wantHead, out)
	}
	if strings.Contains(kv["HEAD"], `\`) || strings.Contains(kv["HEAD"], ";") {
		t.Errorf("注入条目混进了 Windows 形态（§2-L7）：%q", kv["HEAD"])
	}
	if kv["OPTS"] != "--max-old-space-size=4096 --openssl-legacy-provider" {
		t.Errorf("bash 侧 env 覆盖没生效：%q", kv["OPTS"])
	}
	if kv["AGAIN"] != "2" {
		t.Errorf("同目录重复触发不得叠加（应 2 段），实得 %q", kv["AGAIN"])
	}
	if kv["BACK"] != "PRESETBIN" {
		t.Errorf("离开后必须回快照原值，实得 %q", kv["BACK"])
	}
	if kv["BACKOPTS"] != "[]" {
		t.Errorf("离开后 env 要撤销，实得 %q", kv["BACKOPTS"])
	}
}
