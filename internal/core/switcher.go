package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HUIdada1/envswitch/internal/settings"
	"github.com/HUIdada1/envswitch/internal/sys"
)

// Switch 把工具激活 junction 重指到 inst.BinDir，并广播环境变化所需的最小写入。
// store 版本的 BinDir = 版本根（runtime）或包根（js，经 shim 间接进 PATH）；
// nvm 等外部来源的 BinDir 即其版本目录。
func Switch(s *settings.Settings, ad Adapter, inst *Install) error {
	target := inst.BinDir
	switch inst.Source {
	case "store", "nvm", "detected", "external":
	default:
		return W(ErrUsage, "未知来源 %q", inst.Source)
	}
	if _, err := os.Stat(target); err != nil {
		return W(ErrNotFound, "目标目录不存在: %s", target)
	}
	link := settings.CurrentLink(s, ad.ID())
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	// 先摘旧链接（不存在则跳过）
	if _, ok := junctionTarget(link); ok || dirExists(link) {
		if err := removeLink(link); err != nil {
			return err
		}
	}
	if err := sys.CreateJunction(link, target); err != nil {
		return err
	}
	if _, ok := ad.(JSRunner); ok {
		if err := EnsureShims(s, []Adapter{ad}); err != nil {
			return err
		}
	}
	return nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func removeLink(link string) error {
	if _, ok := junctionTarget(link); ok {
		return sys.RemoveJunction(link)
	}
	// 普通目录（首次或有残留）：只允许删空目录，绝不递归删未知内容
	return os.Remove(link)
}

// InstallPipeline 下载 → 校验 → 解包 → 冒烟。
// progress 回调透传下载进度；ctx 支持取消。
func InstallPipeline(ctx context.Context, s *settings.Settings, ad Adapter, rv RemoteVersion, progress Progress) error {
	if _, err := os.Stat(settings.VersionDir(s, ad.ID(), rv.Version)); err == nil {
		return nil // 已装
	}
	urls := append([]string{rv.URL}, rv.Alts...)
	// 单文件发行形态（kubectl 等 .exe）：下载流直接进版本目录，跳过解包
	singleFile := strings.HasSuffix(strings.ToLower(rv.Archive), ".exe")
	archive := filepath.Join(settings.DownloadsDir(s), rv.Archive)
	dst := settings.VersionDir(s, ad.ID(), rv.Version)
	if singleFile {
		archive = filepath.Join(dst, rv.Archive)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
	}

	sha := rv.SHA256
	companion512 := ""
	if sha == "" {
		for _, u := range urls {
			pu := WithProxy(s.Proxy, u)
			if v, _, ok := fetchCompanionHash(ctx, pu+".sha256"); ok {
				sha = v
				break
			}
			if v, _, ok := fetchCompanionHash(ctx, pu+".sha256.txt"); ok { // Adoptium 形态
				sha = v
				break
			}
			if txt, e := GetText(ctx, pu+".sha512"); e == nil { // Apache/dotnet 形态
				if f := strings.Fields(txt); len(f) >= 1 && len(f[0]) == 128 {
					companion512 = strings.ToLower(f[0])
					break
				}
			}
			if txt, e := GetText(ctx, pathDir(pu)+"/_SHA256SUMS"); e == nil { // HashiCorp 汇总清单
				if got, ok := ParseSHASUMS(txt, rv.Archive); ok {
					sha = got
					break
				}
			}
		}
	}
	if sha == "" && companion512 == "" && strings.HasPrefix(rv.Archive, "node-") && strings.HasSuffix(rv.Archive, "-win-x64.zip") {
		// node（官方与 npmmirror 都是镜像 dist 目录）：同目录 SHASUMS256.txt 兜底
		for _, u := range urls {
			if txt, e := GetText(ctx, pathDir(WithProxy(s.Proxy, u))+"/SHASUMS256.txt"); e == nil {
				if got, ok := ParseSHASUMS(txt, rv.Archive); ok {
					sha = got
					break
				}
			}
		}
	}
	// 多源回退：主源网络错误（404/超时）自动试 Alts；校验错误不换源直接报
	var dlErr error
	dlOK := false
	for _, u := range urls {
		dlErr = FetchURL(ctx, WithProxy(s.Proxy, u), archive, sha, progress)
		if dlErr == nil {
			dlOK = true
			break
		}
		if errors.Is(dlErr, ErrVerify) {
			break
		}
		if ctx.Err() != nil {
			break // 用户取消
		}
	}
	if !dlOK {
		if dlErr != nil && ctx.Err() != nil {
			return W(ErrUsage, "已取消")
		}
		return dlErr
	}
	if sha == "" && companion512 == "" && rv.Integrity != "" {
		if err := VerifyIntegrity(archive, rv.Integrity); err != nil {
			os.Remove(archive)
			return err
		}
	}
	if companion512 != "" {
		got, err := FileSHA512(archive)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, companion512) {
			os.Remove(archive)
			return W(ErrVerify, "%s sha512 与官方伴生值不符", rv.Archive)
		}
	}

	if !singleFile {
		if err := Unpack(archive, dst); err != nil { // Unpack 内部经 *.extract 临时目录原子落位
			return err
		}
	}

	// 冒烟
	exe, args := ad.Smoke()
	if exe != "" {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var out string
		var err error
		if _, ok := ad.(JSRunner); ok {
			// node 驱动：直接跑入口 js，用激活 node 或系统 node
			out, err = smokeRunJS(sctx, dst, exe, args, s)
		} else {
			bin := dst
			if ad.BinSub() != "" {
				bin = filepath.Join(dst, ad.BinSub())
			}
			out, err = smokeRun(sctx, bin, exe, args, s)
		}
		if err != nil {
			os.RemoveAll(dst)
			return W(ErrVerify, "冒烟测试失败（%v）：%s", err, firstLines(out, 3))
		}
		want := strings.TrimPrefix(rv.Version, "v")
		if !strings.Contains(out, want) {
			os.RemoveAll(dst)
			return W(ErrVerify, "冒烟版本不符：期望 %s，实际 %s", want, firstLine(out))
		}
	} else if p, ok := ad.(Prober); ok {
		for _, f := range p.ProbeFiles() {
			if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(f))); err != nil {
				os.RemoveAll(dst)
				return W(ErrVerify, "解包缺关键文件 %s", f)
			}
		}
	}
	if !singleFile {
		os.Remove(archive)
	}
	return nil
}

func smokeRun(ctx context.Context, bin, exe string, args []string, s *settings.Settings) (string, error) {
	full := exe
	if !filepath.IsAbs(full) {
		full = filepath.Join(bin, exe)
		if _, err := os.Stat(full); err != nil {
			if _, err2 := os.Stat(full + ".cmd"); err2 == nil {
				full += ".cmd"
			} else if _, err2 := os.Stat(full + ".exe"); err2 == nil {
				full += ".exe"
			}
		}
	}
	cmd := exec.CommandContext(ctx, full, args...)
	env := append(os.Environ(), "NO_UPDATE_NOTIFIER=1")
	// node 驱动的包（npm/pnpm/yarn）优先用当前激活的 node 跑冒烟，
	// 避免宿主机 PATH 上的老 node（如 16）拒绝新包引擎。
	if nodeDir, ok := junctionTarget(settings.CurrentLink(s, "node")); ok {
		for i, kv := range env {
			if len(kv) > 5 && strings.EqualFold(kv[:5], "PATH=") {
				env[i] = "PATH=" + nodeDir + ";" + kv[5:]
				break
			}
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

// smokeRunJS 用激活 node（缺省回退系统 node）直接跑包根相对入口 js。
func smokeRunJS(ctx context.Context, verDir, entry string, args []string, s *settings.Settings) (string, error) {
	node := "node"
	if nd, ok := junctionTarget(settings.CurrentLink(s, "node")); ok {
		cand := filepath.Join(nd, "node.exe")
		if _, err := os.Stat(cand); err == nil {
			node = cand
		}
	}
	argv := append([]string{filepath.Join(verDir, filepath.FromSlash(entry))}, args...)
	cmd := exec.CommandContext(ctx, node, argv...)
	cmd.Env = append(os.Environ(), "NO_UPDATE_NOTIFIER=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RemoveVersion 删除 store 中版本；当前激活版本必须 cascade（先切走再删）。
func RemoveVersion(s *settings.Settings, ad Adapter, ver string, cascade bool) error {
	dst := settings.VersionDir(s, ad.ID(), ver)
	if _, err := os.Stat(dst); err != nil {
		return W(ErrNotFound, "store 中没有 %s %s", ad.ID(), ver)
	}
	cur, _, _ := CurrentInstall(ad, s)
	isActive := cur != nil && cur.Version == ver
	if isActive && !cascade {
		return W(ErrUsage, "%s %s 正在使用；先切到别的版本，或加 --cascade 一并处理", ad.ID(), ver)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if isActive {
		link := settings.CurrentLink(s, ad.ID())
		if _, ok := junctionTarget(link); ok {
			removeLink(link)
		}
		// 切到剩余最新
		rest := scanStore(ad, s)
		sort.Slice(rest, func(i, j int) bool { return CompareVersions(rest[i].Version, rest[j].Version) > 0 })
		if len(rest) > 0 {
			Switch(s, ad, &rest[0])
			return fmt.Errorf("%s 已删除；已自动切到 %s", ad.ID(), rest[0].Version) // 非致命提示由调用方决定展示
		}
	}
	return nil
}

// UseByName 匹配已装版本（精确/前缀/latest，支持 v 前缀）并激活；
// 第二返回值 = PATH 是否首次写入。ensurePath=false 时只重指 junction。
func UseByName(s *settings.Settings, ad Adapter, want string, ensurePath bool) (string, bool, error) {
	installs := ListInstalls(ad, s)
	if len(installs) == 0 {
		return "", false, W(ErrNotFound, "%s 没有已安装版本；先 envs install %s <ver>", ad.ID(), ad.ID())
	}
	names := make([]string, len(installs))
	for i, iv := range installs {
		names[i] = iv.Version
	}
	ver, err := MatchVersion(names, want)
	if err != nil {
		return "", false, err
	}
	for i := range installs {
		if installs[i].Version != ver {
			continue
		}
		if err := Switch(s, ad, &installs[i]); err != nil {
			return "", false, err
		}
		pathChanged := false
		if ensurePath {
			if pathChanged, err = EnsurePATH(s); err != nil {
				return "", false, err
			}
		}
		return ver, pathChanged, nil
	}
	return "", false, W(ErrNotFound, "内部错误：匹配后未找到条目")
}

// ResolveRemote 在远程索引（含缓存）里把 want 解析成可下载的条目；
// 第二返回值 = 索引来自缓存（离线回退）。
func ResolveRemote(ctx context.Context, s *settings.Settings, ad Adapter, want string) (*RemoteVersion, bool, error) {
	vers, stale, err := RemoteCached(ctx, ad, s)
	if err != nil {
		return nil, stale, err
	}
	names := make([]string, len(vers))
	for i, rv := range vers {
		names[i] = rv.Version
	}
	ver, err := MatchVersion(names, want)
	if err != nil {
		return nil, stale, err
	}
	for i := range vers {
		if vers[i].Version == ver {
			return &vers[i], stale, nil
		}
	}
	return nil, stale, W(ErrNotFound, "索引中无 %s %s", ad.ID(), ver)
}

// EnsurePATH 首次把四条 junction/shims 路径写入用户 PATH（幂等），返回是否变更。
// 变更时自动把旧值快照进 path-backup.json。
// ENVS_NO_PATH=1：只建目录不碰注册表（隔离测试专用，install 自动切换也不写 PATH）。
func EnsurePATH(s *settings.Settings) (bool, error) {
	owned := settings.OwnedPathEntries(s)
	for _, o := range owned {
		if err := os.MkdirAll(o, 0o755); err != nil {
			return false, err
		}
	}
	if os.Getenv("ENVS_NO_PATH") == "1" {
		return false, nil
	}
	raw, changed, err := sys.EnsureUserPath(owned)
	if err != nil {
		if strings.Contains(err.Error(), "Access is denied") || strings.Contains(err.Error(), "被拒绝") {
			return false, W(ErrPerm, "%v（组策略可能禁止修改用户环境变量，doctor 有手动指引）", err)
		}
		return false, err
	}
	if changed {
		bak := map[string]any{"savedAt": time.Now().Format(time.RFC3339), "userPath": raw}
		if data, e := json.MarshalIndent(bak, "", "  "); e == nil {
			os.WriteFile(settings.PathBackup(s), data, 0o644)
		}
		sys.BroadcastEnvChange()
	}
	return changed, nil
}

// StripPATH 卸载/恢复：移除 EnvSwitch 的 PATH 条目并广播。
func StripPATH(s *settings.Settings) (bool, error) {
	_, changed, err := sys.StripOwnedUserPath(settings.IsOwnedPath)
	if err != nil {
		return false, err
	}
	if changed {
		sys.BroadcastEnvChange()
	}
	return changed, nil
}
