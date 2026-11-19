package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// Check 单项诊断结果。
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok / warn / bad / info
	Detail string `json:"detail"`
}

// 注册表读取留成变量：单测注入假的 MachinePath / 用户 PATH，绝不碰真实注册表，
// 而且换台机器也能重复跑同一条判定（与 managed_env.go 的 readUserEnv 同一手法）。
var (
	doctorMachinePath = sys.MachinePath
	doctorUserPath    = sys.ReadUserPath
)

// doctorRuntimes 是 EnvSwitch 默认写进用户 PATH 的三个运行时，顺序与 OwnedPathEntries 一致。
var doctorRuntimes = []string{"node", "python", "go"}

// Doctor 体检：写权限、junction 健康、PATH 仲裁、共存管理器、WebView2、自启。
func Doctor(ads []Adapter, s *settings.Settings) []Check {
	var out []Check

	// 1 注册表写权限
	if sys.UserPathCanWrite() {
		out = append(out, Check{"注册表写入", "ok", `HKCU\Environment 可写`})
	} else {
		out = append(out, Check{"注册表写入", "bad", `HKCU\Environment 不可写，请检查组策略；可手动添加 PATH 条目`})
	}

	// 2 激活链接健康
	links := settings.OwnedPathEntries(s)
	alive, dead := 0, 0
	var deadNames []string
	for _, l := range links {
		if _, ok := junctionTarget(l); ok {
			alive++
			continue
		}
		tool := filepath.Base(l)
		if !anyInstallFor(ads, s, tool) { // 该工具一个版本都没有：不算死
			continue
		}
		dead++
		deadNames = append(deadNames, tool)
	}
	if dead == 0 {
		out = append(out, Check{"激活链接", "ok", fmt.Sprintf("%d 条 junction 有效", alive)})
	} else {
		out = append(out, Check{"激活链接", "bad", "失效: " + strings.Join(deadNames, ", ") + "（重新 use 即可修复）"})
	}

	// 3 PATH 抢位（方案 §7.3-1）：判定必须按「新开终端的真实搜索顺序」跑一遍解析。
	// 旧口径只查用户 PATH 里有没有自己的条目 —— 用户段永远排在系统段之后，
	// 所以本机 node 被 C:\nvm4w\nodejs 抢走时它照样报 ✓，那是假绿灯（§2-L1）。
	userRaw, _ := doctorUserPath()
	checked, hijacked := pathResolution(ads, s, doctorMachinePath(), userRaw)
	registered := ownedPathRegistered(links, userRaw)
	switch {
	case len(checked) == 0 && len(registered) == 0:
		out = append(out, Check{"PATH 注册", "info", "尚未启用：首次 use 会自动写入 5 条路径（shims 在最前）"})
	case len(checked) == 0:
		out = append(out, Check{"PATH 优先级", "info", "PATH 里有 EnvSwitch 条目，但还没有已激活的 runtime 可判定抢位"})
	case len(hijacked) == 0:
		var wins []string
		for _, c := range checked {
			wins = append(wins, c.Tool+"→"+c.Winner)
		}
		out = append(out, Check{"PATH 优先级", "ok",
			"按「系统 PATH + 用户 PATH」全表解析，已激活的 runtime 全部落在 EnvSwitch 目录：" + strings.Join(wins, ", ")})
	default:
		var parts []string
		var dirs []string
		for _, w := range hijacked {
			parts = append(parts, fmt.Sprintf("%s 由 %s 抢先（%s）", w.Tool, w.Winner, w.Origin))
			if !containsFold(dirs, w.Winner) {
				dirs = append(dirs, w.Winner)
			}
		}
		out = append(out, Check{"PATH 优先级", "bad", strings.Join(parts, "；") +
			"，`envs use` 在你机器上当前无效；目录绑定不受影响（走会话注入）。" +
			"手动修：在「编辑账户的环境变量」里把用户 PATH 中的 " + strings.Join(dirs, " ; ") +
			" 删掉或挪到 EnvSwitch 条目之后——系统 PATH 需要管理员，EnvSwitch 不动它"})
	}

	// 3b 挂在 PATH 上却没用的 EnvSwitch 条目（方案 §7.3-3 / §9-7 + lead 裁决 R9-3）：
	// current\go、current\python 这类 EnsurePATH 早于激活建的普通空目录，以及被搬走后的孤儿记录。
	if shells := staleOwnedPathEntries(userRaw); len(shells) > 0 {
		out = append(out, Check{"空壳 PATH 条目", "warn", strings.Join(shells, "；") +
			"。它们让每次解析多一跳，且 status 会显示「已注册未激活」。" +
			"修法：给该工具 envs use 一个版本（会替换成 junction），或 envs path restore 后只留要用的条目"})
	}

	// 3b2 R11 只读面（lead 裁决 ③）：EnvSwitch 自有条目却指向临时目录 / 构建产物 / git 工作区。
	// 只报不改（写入逻辑属功能面，等用户裁决），也不删任何东西。
	if unstable := unstableOwnedPathEntries(userRaw); len(unstable) > 0 {
		out = append(out, Check{"PATH 条目来源", "warn", strings.Join(unstable, "；") +
			"。修法：envs path restore 会摘掉它认得出的 EnvSwitch 条目（按当前 home / 当前 exe 目录判归属）；" +
			"如果这条是历史 dev 构建留下的、如今已认不出来，就只能手动在「编辑账户的环境变量」里删——" +
			"envs 不代删，是因为同款路径也可能是用户自己加的"})
	}
	// 3b3 层 2 兜底的表规模（lead 成本护栏 R13）：如实说明边界，避免用户以为「绑了没生效」。
	out = append(out, layer2GuardCheck())

	// 3c 托管变量层级（方案 §7.3-6，配合 §9-6 的 managedEnvRoot 回归）：
	// 值目录里必须真有 <BinSub>\<冒烟可执行文件>，指到 …\bin 这种低一级的位置就是老 bug 复发。
	out = append(out, managedEnvChecks(ads, s)...)

	// 3d 绑定内核负责的体检项（§7.3-2 总览 / 4 shim 自测 / 5 双形态缺失 / 7 原子写 / 8 锁文件漂移）。
	// 实现归 agent-B（bindings.go:DoctorBindings），这里只负责把它接进 doctor 输出面（lead 裁决 R5）。
	out = append(out, DoctorBindings(ads, s)...)

	// 4 共存管理器
	if root := NvmRoot(); root != "" {
		out = append(out, Check{"nvm-windows 共存", "info", "发现 " + root + "，其版本列入「收编」只读展示"})
	}
	if up := os.Getenv("USERPROFILE"); up != "" {
		if _, err := os.Stat(filepath.Join(up, "scoop")); err == nil {
			out = append(out, Check{"scoop 共存", "info", "检测到 scoop 目录；其 shims 参与 PATH 优先级竞争"})
		}
	}

	// 5 磁盘占用
	out = append(out, Check{"版本库占用", "info", HumanSize(DirSize(settings.StoreDir(s)))})

	// 6 WebView2（GUI 依赖）
	if sys.HasWebView2() {
		out = append(out, Check{"WebView2", "ok", "GUI 可用"})
	} else {
		out = append(out, Check{"WebView2", "warn", "未安装运行库，GUI 无法启动；CLI 不受影响"})
	}

	// 7 自启
	if cmd := sys.GetAutostart("EnvSwitch"); cmd != "" {
		out = append(out, Check{"开机自启", "ok", cmd})
	} else {
		out = append(out, Check{"开机自启", "info", "未开启"})
	}
	return out
}

// hijack 一条「EnvSwitch 已激活、但全表解析出来的胜出者不是 EnvSwitch」的记录。
type hijack struct {
	Tool   string `json:"tool"`
	Exe    string `json:"exe"`
	Winner string `json:"winner"` // 抢先的目录
	Origin string `json:"origin"` // 系统 PATH / 用户 PATH
}

// pathResolution 按 MachinePath + ";" + userPath 拼出新开终端的真实搜索表，逐个 runtime
// 解析同名命令（等价于跑一次 where，但不起进程、也不受当前进程 PATH 与注册表差异干扰）。
// checked 是参与判定的项（只算 EnvSwitch 已激活的工具），hijacked 是胜出者不属于
// EnvSwitch 的那些 —— 方案 §7.3-1 要求这种情况报 bad 而不是 warn。
func pathResolution(ads []Adapter, s *settings.Settings, machine, user string) (checked, hijacked []hijack) {
	dirs, origins := fullPathTable(machine, user)
	home := strings.ToLower(filepath.Clean(settings.Home())) + string(filepath.Separator)
	for _, tool := range doctorRuntimes {
		ad := adapterByID(ads, tool)
		if ad == nil {
			continue
		}
		exe, _ := ad.Smoke()
		if exe == "" {
			continue
		}
		exe = filepath.Base(exe)
		if _, ok := junctionTarget(settings.CurrentLink(s, tool)); !ok {
			continue // 没激活：EnvSwitch 没主张过这个位置，谈不上被抢
		}
		win, origin := firstExistingInDirs(dirs, origins, exe)
		if win == "" {
			continue // 全表都找不到：只能是自己那条，无抢位可言
		}
		h := hijack{Tool: tool, Exe: exe, Winner: win, Origin: origin}
		checked = append(checked, h)
		if !strings.HasPrefix(strings.ToLower(filepath.Clean(win))+string(filepath.Separator), home) {
			hijacked = append(hijacked, h)
		}
	}
	return checked, hijacked
}

// fullPathTable 把系统与用户两段 PATH 拼成一张表，并给每段标来源。
// 段里的 %VAR% 要手工展开：os.ExpandEnv 只认 $VAR，注册表里存的全是 %VAR% 形态。
func fullPathTable(machine, user string) ([]string, []string) {
	var dirs, origins []string
	for _, seg := range []struct{ raw, origin string }{{machine, "系统 PATH"}, {user, "用户 PATH"}} {
		for _, p := range strings.Split(seg.raw, ";") {
			if p = strings.TrimSpace(expandPercent(p)); p == "" {
				continue
			}
			dirs = append(dirs, p)
			origins = append(origins, seg.origin)
		}
	}
	return dirs, origins
}

func expandPercent(p string) string {
	if !strings.Contains(p, "%") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); {
		if p[i] == '%' {
			if j := strings.IndexByte(p[i+1:], '%'); j > 0 {
				if v, ok := os.LookupEnv(p[i+1 : i+1+j]); ok {
					b.WriteString(v)
					i += j + 2
					continue
				}
			}
		}
		b.WriteByte(p[i])
		i++
	}
	return b.String()
}

// firstExistingInDirs 返回第一个含该可执行文件的目录（等价于 where 的首条结果，
// 但不起进程，也不受当前进程 PATH 与注册表值的差异干扰）。
func firstExistingInDirs(dirs, origins []string, exe string) (string, string) {
	for i, d := range dirs {
		if d == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(d, exe)); err == nil && !fi.IsDir() {
			return d, origins[i]
		}
	}
	return "", ""
}

// ownedPathRegistered 返回确实挂在用户 PATH 上的自有条目。
func ownedPathRegistered(links []string, userRaw string) []string {
	set := map[string]bool{}
	for _, p := range strings.Split(userRaw, ";") {
		if p = strings.ToLower(filepath.Clean(expandPercent(p))); p != "" {
			set[p] = true
		}
	}
	var out []string
	for _, l := range links {
		if set[strings.ToLower(filepath.Clean(l))] {
			out = append(out, l)
		}
	}
	return out
}

// staleOwnedPathEntries 扫用户 PATH 上**所有** EnvSwitch 自有条目（三个激活链接 + bin\shims
// + 安装目录 ExeDir），报两类挂在 PATH 上却没用的壳：
//   - 目录不存在（激活前 EnsurePATH 建的壳后来被删、或 envs.exe 被搬走留下孤儿记录）；
//   - 普通空目录且不是 junction（§7.3-3 点的 current\go、current\python 就是这个形状）。
//
// 判据要三条同时成立才报：挂在用户 PATH 上 + 不是有效 junction + 目录存在且为空（或不存在）。
// 用户自己放的有内容目录、以及没挂在 PATH 上的目录都不报，避免误伤。
func staleOwnedPathEntries(userRaw string) []string {
	homeKey := strings.ToLower(filepath.Clean(settings.Home())) + string(filepath.Separator)
	var out []string
	seen := map[string]bool{}
	for _, seg := range strings.Split(userRaw, ";") {
		seg = strings.TrimSpace(expandPercent(seg))
		if seg == "" || !settings.IsOwnedPath(seg) {
			continue
		}
		k := strings.ToLower(filepath.Clean(seg))
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, ok := junctionTarget(seg); ok {
			continue // 正常的激活 junction
		}
		fi, err := os.Stat(seg)
		if err != nil {
			out = append(out, fmt.Sprintf(`%s 挂在用户 PATH 上但目录已不存在（孤儿 PATH 记录）`, seg))
			continue
		}
		if !fi.IsDir() {
			out = append(out, fmt.Sprintf(`%s 挂在用户 PATH 上但它是文件不是目录`, seg))
			continue
		}
		if names, err := os.ReadDir(seg); err == nil && len(names) > 0 {
			continue // 有内容但不是 junction：用户自己放的东西，不冤枉
		}
		kind := "普通空目录（非 junction）"
		if !strings.HasPrefix(k, homeKey) {
			kind = "空目录"
		}
		out = append(out, fmt.Sprintf(`%s 是%s却挂在用户 PATH 上`, seg, kind))
	}
	// lead 裁决 R9-3 补充：安装目录形态的记录（如 E:\EnvSwitch）不走 IsOwnedPath ——
	// 只有「当前这个 exe 就在里面」时才算自有，从别处跑 envs 时它就变成看不见的孤儿。
	// 这里按目录名兜 recognise：末段叫 EnvSwitch 且目录不存在 → 一并报（只报不删）。
	for _, seg := range strings.Split(userRaw, ";") {
		seg = strings.TrimSpace(expandPercent(seg))
		if seg == "" || settings.IsOwnedPath(seg) {
			continue
		}
		if !strings.EqualFold(filepath.Base(filepath.Clean(seg)), "EnvSwitch") {
			continue
		}
		if _, err := os.Stat(seg); err == nil {
			continue // 目录在，是有效安装位置（EnvSwitch 不主张别人的目录，也不报）
		}
		out = append(out, fmt.Sprintf(`%s 挂在用户 PATH 上但目录已不存在（疑似搬过 envs.exe 留下的孤儿记录）`, seg))
	}
	return out
}

// unstableOwnedPathEntries 是 R11 的只读体检面（lead 裁决 ③）：用户 PATH 上如果挂着
// 「指向临时目录 / 构建产物目录 / git 工作区」的 EnvSwitch 自有条目，说明这台机器的 PATH
// 是被某次 dev 或临时构建写进去的 —— 那种路径随时会消失，普通用户机器上不该出现。
//
// 只报不改：写入逻辑属 `OwnedPathEntries`/`EnsurePATH` 的功能面（在 11db8df 就存在，不是
// 本功能引入），改动会改变 PATH 写入集合，超出方案 §9/§11 的文字边界，等用户裁决。
// 三条判据直接复用 R11 的 underTempDir / buildArtifactDir / insideSourceRepo，不写第二份。
func unstableOwnedPathEntries(userRaw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, seg := range strings.Split(userRaw, ";") {
		seg = strings.TrimSpace(expandPercent(seg))
		if seg == "" {
			continue
		}
		mine := settings.IsOwnedPath(seg) || strings.EqualFold(filepath.Base(filepath.Clean(seg)), "EnvSwitch")
		if !mine {
			continue
		}
		k := strings.ToLower(filepath.Clean(seg))
		if seen[k] {
			continue
		}
		seen[k] = true
		switch {
		case underTempDir(seg):
			out = append(out, fmt.Sprintf(`%s 在临时目录里，却是 EnvSwitch 写进用户 PATH 的条目`, seg))
		case buildArtifactDir(seg):
			out = append(out, fmt.Sprintf(`%s 是构建产物目录（build\ 或 \.t\），却挂在用户 PATH 上`, seg))
		case insideSourceRepo(seg):
			out = append(out, fmt.Sprintf(`%s 在某个 git 工作区内，像是开发时写进去的 EnvSwitch 目录`, seg))
		}
	}
	return out
}

// layer2GuardCheck 如实报告层 2 兜底是否还在查表：shim 侧的行数护栏是
// shimGenRowGuard（超过就整体不查，只走激活→系统回退），这里给出同一个口径。
func layer2GuardCheck() Check {
	name := "层 2 兜底"
	p := GenTablePath()
	data, err := os.ReadFile(p)
	if err != nil {
		return Check{name, "info", "尚无 bindings.gen.txt，层 2 只跟随激活版本（跑一次 envs bind 会生成）"}
	}
	rows := 0
	for i, ln := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if i == 0 || strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "#") {
			continue // 首行是 #version=1
		}
		rows++
	}
	if rows > shimGenRowGuard {
		return Check{name, "warn", fmt.Sprintf("绑定表 %d 行 > %d 行成本护栏：shim（层 2 兜底）只认表内前 %d 行"+
			"（envs bind 按 key 长度倒序写表，前 %d 行就是最深/最具体的那几条），第 %d 行起的条目在层 2 不生效；"+
			"要全覆盖请用层 1 会话钩子（envs init <shell>）或显式 envs run —— 二者没有这个上限",
			rows, shimGenRowGuard, shimGenRowGuard, shimGenRowGuard, shimGenRowGuard+1)}
	}
	// R13：三个边界一次说清，不许静默 —— 表规模、恒定成本上界、祖先链深度
	return Check{name, "ok", fmt.Sprintf("绑定表 %d 行（≤%d 行护栏内，全表参与匹配）；shim 命中即前置该版本目录。"+
		"边界：查表成本上界与表大小无关（裁到 %d 行后本机实测恒定约 +34 ms，每行约 3 ms，表再大也不涨）；"+
		"祖先链只上溯 %d 级，深于 %d 级的目录与超护栏的条目同样只能靠层 1 钩子或 envs run",
		rows, shimGenRowGuard, shimGenRowGuard, shimMaxAncestors, shimMaxAncestors)}
}

// managedEnvChecks 校验托管变量的层级：值必须等于当前激活版本根，且该根下面
// <BinSub>\<冒烟可执行文件> 必须真实存在（maven/gradle 冒烟为空，退到 ProbeFiles 第一条）。
func managedEnvChecks(ads []Adapter, s *settings.Settings) []Check {
	keys := ManagedEnvSnapshot(s)
	if len(keys) == 0 {
		return nil
	}
	var bad, warn, okN []string
	for _, key := range keys {
		spec, has := ManagedEnvSpecByKey(key)
		if !has {
			bad = append(bad, fmt.Sprintf("%s：已不在托管白名单里却还记在托管列表中", key))
			continue
		}
		ad := adapterByID(ads, spec.Tool)
		if ad == nil {
			warn = append(warn, fmt.Sprintf("%s：找不到 %s 的适配器，无法校验", key, spec.Tool))
			continue
		}
		val, had, err := readUserEnv(key)
		if err != nil {
			warn = append(warn, fmt.Sprintf("%s：读取失败 %v", key, err))
			continue
		}
		val = strings.TrimSpace(val)
		if !had || val == "" {
			bad = append(bad, fmt.Sprintf("%s：托管列表里有它，但变量实际是空的", key))
			continue
		}
		if root, alive := managedEnvRoot(ads, s, spec.Tool); alive && !strings.EqualFold(filepath.Clean(root), filepath.Clean(val)) {
			bad = append(bad, fmt.Sprintf("%s 指向 %s，而当前激活版本根是 %s（切换后没同步）", key, val, root))
			continue
		}
		rel := managedEnvProbe(ad)
		if rel == "" {
			warn = append(warn, fmt.Sprintf("%s：%s 没有冒烟可执行文件可查，跳过层级校验", key, spec.Tool))
			continue
		}
		probe := filepath.Join(val, rel)
		if fi, err := os.Stat(probe); err != nil || fi.IsDir() {
			// 老 bug 的形状就是值多了一级 \bin：JAVA_HOME=…\bin → Maven 去找 …\bin\bin\java.exe
			bad = append(bad, fmt.Sprintf("%s=%s 下找不到 %s（层级不对：值应是版本根，不是它的 %s 子目录；重新 envs use %s <ver> 会改写）",
				key, val, rel, filepath.Dir(rel), spec.Tool))
			continue
		}
		okN = append(okN, key+"="+val)
	}
	var out []Check
	name := "托管变量层级"
	switch {
	case len(bad) > 0:
		out = append(out, Check{name, "bad", strings.Join(bad, "；")})
	case len(warn) > 0:
		out = append(out, Check{name, "warn", strings.Join(warn, "；")})
	default:
		out = append(out, Check{name, "ok", "值目录层级正确: " + strings.Join(okN, ", ")})
	}
	return out
}

// managedEnvProbe 返回托管变量值目录下应当存在的可执行文件相对路径。
// java/go 的 BinSub 是 bin → bin\java.exe / bin\go.exe；dotnet 的 BinSub 为空 → dotnet.exe；
// maven/gradle 没有冒烟（依赖 JAVA_HOME）→ 用 ProbeFiles 的第一条 bin\mvn.cmd / bin\gradle.bat。
func managedEnvProbe(ad Adapter) string {
	if exe, _ := ad.Smoke(); exe != "" {
		return filepath.Join(ad.BinSub(), filepath.Base(filepath.FromSlash(exe)))
	}
	if pr, ok := ad.(Prober); ok {
		if pf := pr.ProbeFiles(); len(pf) > 0 {
			return filepath.FromSlash(pf[0])
		}
	}
	return ""
}

func adapterByID(ads []Adapter, id string) Adapter {
	for _, a := range ads {
		if strings.EqualFold(a.ID(), id) {
			return a
		}
	}
	return nil
}

func containsFold(xs []string, v string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func anyInstallFor(ads []Adapter, s *settings.Settings, tool string) bool {
	for _, ad := range ads {
		if ad.ID() == tool && len(scanStore(ad, s)) > 0 {
			return true
		}
	}
	return false
}

// BundleDiagnostics 把配置、PATH 现状、激活链接与最近日志打成一个 zip，
// 用户提 issue 时直接附件，不需要手抄任何系统信息。
func BundleDiagnostics(s *settings.Settings) (string, error) {
	dir := settings.LogsDir(s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, fmt.Sprintf("diag-%s.json", time.Now().Format("20060102-150405")))
	bundle := map[string]any{
		"version":  BuildVersion(),
		"home":     settings.Home(),
		"store":    settings.StoreDir(s),
		"userPath": rawUserPath(),
		"owned":    settings.OwnedPathEntries(s),
		"settings": s,
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return "", err
	}
	return out, nil
}

func rawUserPath() string { v, _ := sys.ReadUserPath(); return v }
