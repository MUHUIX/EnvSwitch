package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/HUIdada1/envswitch/internal/settings"
	"github.com/HUIdada1/envswitch/internal/sys"
)

// ListInstalls 汇总本机版本：store 自有 + 适配器声明的收编来源（如 nvm）。
func ListInstalls(ad Adapter, s *settings.Settings) []Install {
	out := scanStore(ad, s)
	if ex, ok := ad.(interface{ Adopted(s *settings.Settings) []Install }); ok {
		out = append(out, ex.Adopted(s)...)
	}
	sortInstalls(out)
	return out
}

// ListInstallsFast 版本条目与 ListInstalls 相同，只是 Size 统一为 -1（不统计目录大小）。
// 总览页每 3.5 秒轮询一次，若每次递归统计整个版本库会让界面明显发卡。
func ListInstallsFast(ad Adapter, s *settings.Settings) []Install {
	out := scanStoreFast(ad, s)
	if ex, ok := ad.(FastAdopter); ok {
		out = append(out, ex.AdoptedFast(s)...)
	} else if ex, ok := ad.(interface{ Adopted(s *settings.Settings) []Install }); ok {
		out = append(out, ex.Adopted(s)...)
	}
	sortInstalls(out)
	return out
}

func sortInstalls(out []Install) {
	sort.Slice(out, func(i, j int) bool { return CompareVersions(out[i].Version, out[j].Version) > 0 })
}

func scanStore(ad Adapter, s *settings.Settings) []Install { return scanStoreN(ad, s, true) }
func scanStoreFast(ad Adapter, s *settings.Settings) []Install { return scanStoreN(ad, s, false) }

func scanStoreN(ad Adapter, s *settings.Settings, withSize bool) []Install {
	dir := settings.ToolStoreDir(s, ad.ID())
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Install
	for _, e := range ents {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") || strings.HasSuffix(e.Name(), ".extract") {
			continue
		}
		bin := filepath.Join(dir, e.Name())
		if ad.BinSub() != "" {
			bin = filepath.Join(bin, ad.BinSub())
		}
		if fi, err := os.Stat(bin); err != nil || !fi.IsDir() {
			continue // 半残目录不参与列表
		}
		size := int64(-1)
		if withSize {
			size = DirSize(filepath.Join(dir, e.Name()))
		}
		out = append(out, Install{
			Version: e.Name(), Source: "store", BinDir: bin,
			Size: size, Editable: true,
		})
	}
	return out
}

// CurrentInstall 读取工具当前激活 junction 目标，返回对应 Install（可能 source=外部目录）。
func CurrentInstall(ad Adapter, s *settings.Settings) (*Install, string, error) {
	link := settings.CurrentLink(s, ad.ID())
	target, ok := junctionTarget(link)
	if !ok {
		return nil, "", nil
	}
	ver := versionFromStorePath(ad, s, target)
	bin := target
	if ad.BinSub() != "" && strings.Contains(target, filepath.Join("store", ad.ID())) {
		bin = filepath.Join(target, ad.BinSub())
	}
	// nvm 收编目录：target 本身就是版本根（含 node.exe），不再拼 BinSub
	src := "store"
	if !strings.Contains(strings.ToLower(target), strings.ToLower(settings.StoreDir(s))) {
		src = "external"
		if root := NvmRoot(); root != "" && strings.HasPrefix(strings.ToLower(target), strings.ToLower(root)+`\`) {
			src = "nvm"
		}
	}
	return &Install{Version: ver, Source: src, BinDir: bin, Size: -1, Active: true, Editable: src == "store"}, target, nil
}

func versionFromStorePath(ad Adapter, s *settings.Settings, target string) string {
	rel, err := filepath.Rel(settings.ToolStoreDir(s, ad.ID()), target)
	if err == nil && !strings.HasPrefix(rel, "..") {
		return strings.Split(rel, string(filepath.Separator))[0]
	}
	// nvm 目录形如 C:\nvm4w\v22.22.0
	base := filepath.Base(target)
	base = strings.TrimPrefix(base, "v")
	return base
}

// junctionTarget 解析 junction 指向的真实目录。
// 注意：Go 的 filepath.EvalSymlinks 在 Windows 上不解析 junction（重解析点挂载），
// 必须走 FSCTL_GET_REPARSE_POINT（真机实测教训）。
func junctionTarget(link string) (string, bool) {
	return sys.ReadJunction(link)
}

// —— nvm-windows 收编（只读）——

var nvmVerDir = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

func NvmRoot() string {
	candidates := []string{}
	if v := os.Getenv("NVM_ROOT"); v != "" {
		candidates = append(candidates, v)
	}
	if v := os.Getenv("NVM_HOME"); v != "" {
		candidates = append(candidates, v)
	}
	if ad := os.Getenv("APPDATA"); ad != "" {
		candidates = append(candidates, filepath.Join(ad, "nvm"))
	}
	candidates = append(candidates, `C:\nvm4w`, `C:\Program Files\nodejs\nvm`)
	for _, c := range candidates {
		data, err := os.ReadFile(filepath.Join(c, "settings.txt"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if rest, ok := strings.CutPrefix(line, "root:"); ok {
				return strings.TrimSpace(rest)
			}
		}
		return c
	}
	return ""
}

// NvmVersions 列出 nvm-windows 已装 node 版本目录。
func NvmVersions() []Install { return nvmVersions(true) }

// NvmVersionsFast 同上，但不算各版本目录大小（供总览轮询）。
func NvmVersionsFast() []Install { return nvmVersions(false) }

func nvmVersions(withSize bool) []Install {
	root := NvmRoot()
	if root == "" {
		return nil
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Install
	for _, e := range ents {
		if m := nvmVerDir.FindStringSubmatch(e.Name()); m != nil {
			dir := filepath.Join(root, e.Name())
			size := int64(-1)
			if withSize {
				size = DirSize(dir)
			}
			out = append(out, Install{
				Version: m[1], Source: "nvm", BinDir: dir,
				Size: size, Editable: false,
			})
		}
	}
	return out
}

// —— 系统 PATH 检测（只读展示）——

// DetectedOnPATH 在（注册表合并后的）PATH 上找 tool.exe，排除 EnvSwitch 自己的目录。
func DetectedOnPATH(exe string) *Install {
	merged := MachinePATH() + ";" + UserPATHRaw()
	for _, p := range strings.Split(merged, ";") {
		p = os.ExpandEnv(strings.TrimSpace(p))
		if p == "" || settings.IsOwnedPath(p) {
			continue
		}
		cand := filepath.Join(p, exe)
		if _, err := os.Stat(cand); err == nil {
			return &Install{Version: "detected", Source: "detected", BinDir: p, Size: -1, Editable: false}
		}
	}
	return nil
}

func MachinePATH() string { return sys.MachinePath() }
func UserPATHRaw() string { v, _ := sys.ReadUserPath(); return v }

// WhichResolved 按 Windows PATH 语义与常用路径寻找 exe。
func WhichResolved(exe string) string {
	names := []string{exe}
	ext := filepath.Ext(exe)
	if ext == "" {
		names = append(names, exe+".exe", exe+".cmd", exe+".bat")
	}
	for _, n := range names {
		if got, err := exec.LookPath(n); err == nil && !settings.IsOwnedPath(got) {
			return got
		}
	}
	// 在用户与系统 PATH 中逐目录排查
	merged := MachinePATH() + ";" + UserPATHRaw()
	for _, p := range strings.Split(merged, ";") {
		p = os.ExpandEnv(strings.TrimSpace(p))
		if p == "" || settings.IsOwnedPath(p) {
			continue
		}
		for _, n := range names {
			cand := filepath.Join(p, n)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
	}
	// 常见 Windows 安装目录直通兜底探测
	baseName := strings.ToLower(filepath.Base(exe))
	baseNameNoExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	localApp := os.Getenv("LOCALAPPDATA")
	progFiles := os.Getenv("ProgramFiles")
	candidates := []string{}
	switch baseNameNoExt {
	case "git":
		if localApp != "" {
			candidates = append(candidates, filepath.Join(localApp, "Programs", "Git", "cmd", "git.exe"))
		}
		// 自定义盘符安装（安装器允许改路径），按常见盘符探测而不是写死某一台机器
		for _, d := range []string{"C", "D", "E", "F"} {
			candidates = append(candidates, d+`:\Git\cmd\git.exe`)
		}
		candidates = append(candidates,
			filepath.Join(progFiles, "Git", "cmd", "git.exe"),
			filepath.Join(progFiles, "Git", "bin", "git.exe"))
	case "docker":
		candidates = append(candidates, filepath.Join(progFiles, "Docker", "Docker", "resources", "bin", "docker.exe"))
	case "nvm":
		if localApp != "" {
			candidates = append(candidates, filepath.Join(localApp, "nvm", "nvm.exe"))
		}
		candidates = append(candidates, `C:\nvm4w\nvm.exe`)
	case "pip":
		if localApp != "" {
			pyDir := filepath.Join(localApp, "Programs", "Python")
			if ents, err := os.ReadDir(pyDir); err == nil {
				for _, e := range ents {
					candidates = append(candidates, filepath.Join(pyDir, e.Name(), "Scripts", "pip.exe"))
				}
			}
		}
	case "python":
		if localApp != "" {
			pyDir := filepath.Join(localApp, "Programs", "Python")
			if ents, err := os.ReadDir(pyDir); err == nil {
				for _, e := range ents {
					candidates = append(candidates, filepath.Join(pyDir, e.Name(), "python.exe"))
				}
			}
		}
	case "redis", "redis-cli":
		candidates = append(candidates, `C:\Program Files\Redis\redis-cli.exe`, `C:\Program Files\Redis\redis-server.exe`)
	case "node":
		candidates = append(candidates, `C:\nvm4w\nodejs\node.exe`, filepath.Join(progFiles, "nodejs", "node.exe"))
	case "go":
		candidates = append(candidates, filepath.Join(progFiles, "Go", "bin", "go.exe"))
	case "java":
		if jh := os.Getenv("JAVA_HOME"); jh != "" {
			candidates = append(candidates, filepath.Join(jh, "bin", "java.exe"))
		}
		javaRoot := filepath.Join(progFiles, "Java")
		if ents, err := os.ReadDir(javaRoot); err == nil {
			for _, e := range ents {
				candidates = append(candidates, filepath.Join(javaRoot, e.Name(), "bin", "java.exe"))
			}
		}
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// CompareVersions 语义化点分数字版本比较（可带前缀 v、可带构建号 20241002）。
func CompareVersions(a, b string) int {
	pa, pb := splitVer(a), splitVer(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func splitVer(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, s := range strings.Split(v, ".") {
		n, err := strconv.Atoi(s)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// MatchVersion 在候选里匹配 want：latest / 精确 / 前缀（22 → 22.x 最新）。
func MatchVersion(vers []string, want string) (string, error) {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	if want == "" {
		return "", W(ErrUsage, "版本不能为空")
	}
	if want == "latest" {
		if len(vers) == 0 {
			return "", W(ErrNotFound, "没有可用版本")
		}
		sorted := append([]string{}, vers...)
		sort.Slice(sorted, func(i, j int) bool { return CompareVersions(sorted[i], sorted[j]) > 0 })
		return sorted[0], nil
	}
	var hits []string
	for _, v := range vers {
		vt := strings.TrimPrefix(v, "v")
		if vt == want || strings.HasPrefix(vt, want+".") {
			hits = append(hits, v)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", W(ErrNotFound, "没有匹配 %q 的版本", want)
	default:
		// major/minor 别名语义（同 nvm/fnm）：`use node 22` → 最新 22.x
		sort.Slice(hits, func(i, j int) bool { return CompareVersions(hits[i], hits[j]) > 0 })
		return hits[0], nil
	}
}
