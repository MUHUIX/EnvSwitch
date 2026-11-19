package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// doctor 三项（§7.3-1 抢位口径 / §7.3-3 空壳条目 / §7.3-6 托管层级）的分支覆盖。
// 全部走注入桩：绝不读写真实注册表（本任务硬约束），也不碰 %LOCALAPPDATA%\EnvSwitch。

type docAdapter struct {
	id     string
	binSub string
	exe    string
	probe  []string
}

func (a docAdapter) ID() string                                            { return a.id }
func (docAdapter) Title() string                                           { return "doc" }
func (docAdapter) Group() string                                           { return "runtime" }
func (docAdapter) FetchRemote(*settings.Settings) ([]RemoteVersion, error) { return nil, nil }
func (a docAdapter) BinSub() string                                        { return a.binSub }
func (a docAdapter) Smoke() (string, []string)                             { return a.exe, nil }

// ProbeFiles 让 docAdapter 同时实现 Prober（maven/gradle 那类没有冒烟的工具）。
func (a docAdapter) ProbeFiles() []string { return a.probe }

// touchExe 建目录并放一个同名空文件：判定只看存在性与「不是目录」，不需要真能执行。
func touchExe(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func stubDoctorPath(t *testing.T, machine, user string) {
	t.Helper()
	om, ou := doctorMachinePath, doctorUserPath
	doctorMachinePath = func() string { return machine }
	doctorUserPath = func() (string, error) { return user, nil }
	t.Cleanup(func() { doctorMachinePath, doctorUserPath = om, ou })
}

// junctionTo 建 home\current\<tool> → target 的测试 junction。
func junctionTo(t *testing.T, s *settings.Settings, tool, target string) string {
	t.Helper()
	link := settings.CurrentLink(s, tool)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sys.CreateJunction(link, target); err != nil {
		t.Fatalf("建测试 junction %s→%s: %v", link, target, err)
	}
	return link
}

// §7.3-1：系统段抢先 → 必须报「被抢」，且给出来源与抢先目录（旧口径在这里会报 ✓ 假绿灯）。
func TestPathResolutionReportsSystemHijack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	ours := touchExe(t, filepath.Join(home, "store", "node", "22.22.0"), "node.exe")
	link := junctionTo(t, s, "node", ours)
	nvm := touchExe(t, filepath.Join(t.TempDir(), "nvm4w", "nodejs"), "node.exe")

	stubDoctorPath(t, nvm+`;C:\Windows\system32`, strings.Join(settings.OwnedPathEntries(s), ";"))
	checked, hijacked := pathResolution([]Adapter{docAdapter{id: "node", exe: "node.exe"}}, s, nvm+";C:\\Windows\\system32", strings.Join(settings.OwnedPathEntries(s), ";"))
	if len(checked) != 1 {
		t.Fatalf("已激活的 node 应参与判定，实得 %d 项", len(checked))
	}
	if len(hijacked) != 1 {
		t.Fatalf("node 被外部目录抢先，应报 1 项，实得 %+v", hijacked)
	}
	h := hijacked[0]
	if filepath.Clean(h.Winner) != filepath.Clean(nvm) || h.Origin != "系统 PATH" {
		t.Fatalf("抢先目录/来源不对：%+v（期望 %s / 系统 PATH）", h, nvm)
	}
	_ = link

	// 同一条判定挂到 Doctor 上：等级必须是 bad，文案要点名「envs use 无效 + 目录绑定不受影响」
	out := Doctor([]Adapter{docAdapter{id: "node", exe: "node.exe"}}, s)
	var c *Check
	for i := range out {
		if out[i].Name == "PATH 优先级" {
			c = &out[i]
		}
	}
	if c == nil {
		t.Fatal("doctor 输出里没有 PATH 优先级项")
	}
	if c.Status != "bad" {
		t.Fatalf("被系统 PATH 抢位必须报 bad，实得 %q（%s）", c.Status, c.Detail)
	}
	for _, want := range []string{"nvm4w", "系统 PATH", "envs use", "目录绑定不受影响"} {
		if !strings.Contains(c.Detail, want) {
			t.Fatalf("文案缺关键字 %q：%s", want, c.Detail)
		}
	}
}

// §7.3-1 反向：胜出者确实在 EnvSwitch 目录下 → 不报抢位，Doctor 给 ok。
func TestPathResolutionOkWhenEnvSwitchWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	ours := touchExe(t, filepath.Join(home, "store", "node", "16.20.2"), "node.exe")
	junctionTo(t, s, "node", ours)
	user := strings.Join(settings.OwnedPathEntries(s), ";")

	checked, hijacked := pathResolution([]Adapter{docAdapter{id: "node", exe: "node.exe"}}, s, `C:\Windows\system32`, user)
	if len(checked) != 1 || len(hijacked) != 0 {
		t.Fatalf("EnvSwitch 自己胜出时不该报抢位：checked=%+v hijacked=%+v", checked, hijacked)
	}
	if !strings.HasPrefix(strings.ToLower(checked[0].Winner), strings.ToLower(home)) {
		t.Fatalf("胜出者应是 EnvSwitch 目录：%+v", checked[0])
	}
}

// 没激活的工具不参与判定：否则新机器一装就一片红。
func TestPathResolutionSkipsInactiveTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	nvm := touchExe(t, filepath.Join(t.TempDir(), "nvm4w", "nodejs"), "node.exe")
	checked, hijacked := pathResolution([]Adapter{docAdapter{id: "node", exe: "node.exe"}}, s, nvm, "")
	if len(checked) != 0 || len(hijacked) != 0 {
		t.Fatalf("未激活工具不该参与判定：%+v / %+v", checked, hijacked)
	}
}

// §7.3-1 的解析必须等价于 where：取第一个含该文件的目录，且 %VAR% 段要先展开。
func TestFullPathTableAndExpandPercent(t *testing.T) {
	t.Setenv("ENVS_TEST_PROFILE", filepath.Join("C:", "Users", "tester"))
	dirs, origins := fullPathTable(`%ENVS_TEST_PROFILE%\a;C:\Windows`, `D:\b;;`)
	want := []string{filepath.Join("C:", "Users", "tester", "a"), `C:\Windows`, `D:\b`}
	if len(dirs) != len(want) {
		t.Fatalf("段数不对：%v", dirs)
	}
	for i, w := range want {
		if filepath.Clean(dirs[i]) != filepath.Clean(w) {
			t.Fatalf("第 %d 段：%q 期望 %q", i, dirs[i], w)
		}
	}
	if origins[0] != "系统 PATH" || origins[2] != "用户 PATH" {
		t.Fatalf("来源标注错：%v", origins)
	}
	// 未知变量原样保留，不吞成空串（吞了会让判定漏掉一条真实目录）
	if got := expandPercent(`%ENVS_TEST_NOPE_X%\y`); !strings.Contains(got, "%ENVS_TEST_NOPE_X%") {
		t.Fatalf("未知变量被吞掉了：%q", got)
	}
}

// §7.3-3：空壳目录挂在 PATH 上才报；有内容 / 是 junction / 没挂 PATH 都不报。
func TestEmptyShellEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	goLink := settings.CurrentLink(s, "go")
	pyLink := settings.CurrentLink(s, "python")
	nodeLink := settings.CurrentLink(s, "node")
	for _, d := range []string{goLink, pyLink} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// node 做成正常 junction（指向含 node.exe 的目录）
	ours := touchExe(t, filepath.Join(home, "store", "node", "22.22.0"), "node.exe")
	junctionTo(t, s, "node", ours)
	// python 里塞个文件：那是用户自己放的东西，不能当空壳冤枉
	if err := os.WriteFile(filepath.Join(pyLink, " readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// §7.3-3 + lead 裁决 R9-3：判据覆盖所有 EnvSwitch 自有条目，不只是 current\ 三个链接
	user := strings.Join([]string{goLink, pyLink, nodeLink,
		settings.CurrentLink(s, "rust"), // 自有前缀但目录不存在 → 孤儿记录
		settings.ShimsDir(s)}, ";")      // shims 还没生成 → 也算壳
	got := staleOwnedPathEntries(user)
	t.Logf("报告条目：%v", got)
	if len(got) != 3 {
		t.Fatalf("应报 3 条（go 空壳 / current\\rust 不存在 / bin\\shims 不存在），实得 %d：%v", len(got), got)
	}
	var hasShell, hasMissing bool
	for _, g := range got {
		if strings.Contains(g, "current"+string(filepath.Separator)+"go") && strings.Contains(g, "空目录") {
			hasShell = true
		}
		if strings.Contains(g, "rust") && strings.Contains(g, "已不存在") {
			hasMissing = true
		}
	}
	if !hasShell || !hasMissing {
		t.Fatalf("两类壳都要报出来（空壳 + 孤儿）：%v", got)
	}
	// 不挂在 PATH 上 → 不报
	if got := staleOwnedPathEntries(`C:\Other`); len(got) != 0 {
		t.Fatalf("没挂 PATH 不该报：%v", got)
	}
	// 真机形状回归：像 current\go 与 current\python 那样「普通空目录 + 挂在 PATH 上」的两条
	// 必须同时报出来 —— 这一条盯的是我上一版误判「目录不存在」的事实错误（lead 裁决 R9）。
	p1 := filepath.Join(home, "current", "jdk")
	p2 := filepath.Join(home, "current", "node22")
	for _, d := range []string{p1, p2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := staleOwnedPathEntries(strings.Join([]string{p1, p2}, ";")); len(got) != 2 {
		t.Fatalf("两条空壳都应报，实得 %v", got)
	}
	// lead 裁决 R9-3：安装目录形态（末段叫 EnvSwitch）不在 IsOwnedPath 口径内，
	// 从别处跑 envs 时它就是看不见的孤儿记录 —— 这里按目录名补识别，只报不删。
	orphan := staleOwnedPathEntries(`Z:\EnvSwitch;C:\Windows`)
	if len(orphan) != 1 || !strings.Contains(orphan[0], "Z:\\EnvSwitch") || !strings.Contains(orphan[0], "孤儿") {
		t.Fatalf("应报出搬失的安装目录记录，实得 %v", orphan)
	}
	if _, err := os.Stat(`E:\EnvSwitch`); err == nil {
		if got := staleOwnedPathEntries(`E:\EnvSwitch`); len(got) != 0 {
			t.Fatalf("本机确实存在的安装目录不应被报成孤儿：%v", got)
		}
	}
	// doctor 集成：等级 warn 且给修法
	stubDoctorPath(t, `C:\Windows\system32`, user)
	out := Doctor([]Adapter{docAdapter{id: "go", exe: "go.exe", binSub: "bin"}}, s)
	var found bool
	for _, c := range out {
		if c.Name == "空壳 PATH 条目" {
			found = true
			if c.Status != "warn" || !strings.Contains(c.Detail, "envs path restore") {
				t.Fatalf("空壳项等级/文案不对：%+v", c)
			}
		}
	}
	if !found {
		t.Fatal("doctor 输出里没有空壳 PATH 条目项")
	}
}

// §7.3-6：托管变量的层级校验 —— 值指到 …\bin 就是老 bug 复发，必须 bad。
func TestManagedEnvChecksLevels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	env := stubUserEnv(t)
	verDir := filepath.Join(home, "store", "java", "17.0.9")
	binDir := filepath.Join(verDir, "bin")
	if _, err := os.Stat(filepath.Join(touchExe(t, binDir, "java.exe"), "")); err != nil {
		t.Fatal(err)
	}
	s := &settings.Settings{EnvBackup: map[string]settings.EnvBackup{}}
	junctionTo(t, s, "java", binDir) // java 的链接落在 <ver>\bin
	ads := []Adapter{docAdapter{id: "java", binSub: "bin", exe: "java.exe"}}

	// ① 值多了一级 \bin（方案 §1-7 修掉的形状，回归护栏）
	s.ManagedEnv = []string{"JAVA_HOME"}
	env["JAVA_HOME"] = binDir
	out := managedEnvChecks(ads, s)
	if len(out) != 1 || out[0].Status != "bad" {
		t.Fatalf("值指向 …\\bin 必须报 bad：%+v", out)
	}
	if !strings.Contains(out[0].Detail, "JAVA_HOME") {
		t.Fatalf("文案应点名变量：%s", out[0].Detail)
	}

	// ② 值等于版本根且根下有 bin\java.exe → ok
	env["JAVA_HOME"] = verDir
	out = managedEnvChecks(ads, s)
	if len(out) != 1 || out[0].Status != "ok" {
		t.Fatalf("层级正确应报 ok：%+v", out)
	}

	// ③ 托管列表里有、变量却是空的 → bad
	delete(env, "JAVA_HOME")
	out = managedEnvChecks(ads, s)
	if len(out) != 1 || out[0].Status != "bad" || !strings.Contains(out[0].Detail, "空的") {
		t.Fatalf("变量为空应报 bad：%+v", out)
	}

	// ④ 白名单外的键 → bad 并说明
	s.ManagedEnv = []string{"NOT_A_VAR"}
	out = managedEnvChecks(ads, s)
	if len(out) != 1 || out[0].Status != "bad" {
		t.Fatalf("白名单外的托管键应报 bad：%+v", out)
	}

	// ⑤ maven 这类冒烟为空：退到 ProbeFiles 第一条
	mvnVer := filepath.Join(home, "store", "maven", "3.9.9")
	if err := os.MkdirAll(filepath.Join(mvnVer, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mvnVer, "bin", "mvn.cmd"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mvnLink := settings.CurrentLink(s, "maven")
	if err := os.MkdirAll(filepath.Dir(mvnLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sys.CreateJunction(mvnLink, filepath.Join(mvnVer, "bin")); err != nil {
		t.Fatal(err)
	}
	s.ManagedEnv = []string{"MAVEN_HOME"}
	env["MAVEN_HOME"] = mvnVer
	mads := []Adapter{docAdapter{id: "maven", binSub: "bin", exe: "", probe: []string{"bin/mvn.cmd"}}}
	out = managedEnvChecks(mads, s)
	if len(out) != 1 || out[0].Status != "ok" {
		t.Fatalf("maven 用 ProbeFiles 校验应报 ok：%+v", out)
	}

	// ⑥ 没有冒烟也没有 ProbeFiles 的工具（locAdapter）→ warn 跳过，不冤枉成 bad
	s.ManagedEnv = []string{"JAVA_HOME"}
	env["JAVA_HOME"] = verDir
	out = managedEnvChecks([]Adapter{docAdapter{id: "java", binSub: "bin", exe: ""}}, s)
	if len(out) != 1 || out[0].Status != "warn" {
		t.Fatalf("无件可查时应 warn 跳过：%+v", out)
	}

	// ⑦ 未托管任何变量 → 不出项
	s.ManagedEnv = nil
	if out := managedEnvChecks(ads, s); out != nil {
		t.Fatalf("没有托管变量时不该出项：%+v", out)
	}
}

// R11 只读面（lead 裁决 ③）：EnvSwitch 自有条目指向临时目录 / 构建产物 / git 工作区 → 报出来，
// 但 doctor 不改写、不删除（写入逻辑属功能面，等用户裁决）。判据与 R11 生成期共用同一套。
func TestUnstableOwnedPathEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		raw  string
		want int
	}{
		// 本机 ENVS_HOME 就在 temp 下 → home\current\go 这种自有条目必须被抓出（正是 dev 构建写进 PATH 的形状）
		{"temp 下的自有激活链接", settings.CurrentLink(&settings.Settings{}, "go"), 1},
		{"当前 exe 目录（本机同样在 temp）", settings.ExeDir(), 1},
		{"自有且干净的 shims 目录", `Q:\Tools\EnvSwitch\bin\shims`, 0}, // 不在自有集合里（home 不是它）→ 不报
		{"构建产物目录但非自有", `D:\work\EnvSwitch\build\bin`, 0},
		{"普通系统目录", `C:\Windows\system32`, 0},
		{"仓库工作区但非自有", wd, 0},
	}
	for _, c := range cases {
		got := unstableOwnedPathEntries(c.raw)
		if len(got) != c.want {
			t.Fatalf("%s：期望 %d 条，实得 %v", c.name, c.want, got)
		}
	}
	// 三个判据本身在 TestStableInstallDirPredicates 里逐条验过；这里再确认「自有 + 在 git 工作区」
	// 这一组合会落到 insideSourceRepo 分支（用真实的 exe 目录构造：本机在 temp，先命中 temp 分支，
	// 所以改判一个只带 .git 的构造目录）。
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(repo, "EnvSwitch")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	if !insideSourceRepo(install) {
		t.Fatal("insideSourceRepo 判据本身失效")
	}
	if got := unstableOwnedPathEntries(install); len(got) == 0 {
		t.Fatalf("末段叫 EnvSwitch 且在 git 工作区里的条目应被报出来：%v", got)
	}
	// doctor 集成：temp 下的自有条目要让「PATH 条目来源」出 warn
	user := settings.CurrentLink(&settings.Settings{}, "go")
	stubDoctorPath(t, `C:\Windows\system32`, user)
	out := Doctor([]Adapter{docAdapter{id: "go", exe: "go.exe", binSub: "bin"}}, &settings.Settings{})
	var hit *Check
	for i := range out {
		if out[i].Name == "PATH 条目来源" {
			hit = &out[i]
		}
	}
	if hit == nil || hit.Status != "warn" {
		t.Fatalf("temp 下的自有条目应让「PATH 条目来源」报 warn：%+v", hit)
	}
	for _, want := range []string{"envs path restore", "手动"} {
		if !strings.Contains(hit.Detail, want) {
			t.Fatalf("修法文案缺 %q：%s", want, hit.Detail)
		}
	}
}

// R5 接线：agent-B 的 §7.3-2 总览必须出现在 envs doctor 输出里，且只接一次。
func TestDoctorWiresBindingOverviewOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	stubUserEnv(t)
	stubDoctorPath(t, `C:\Windows\system32`, "")
	s := &settings.Settings{}
	out := Doctor([]Adapter{docAdapter{id: "node", exe: "node.exe"}}, s)
	n := 0
	for _, c := range out {
		if c.Name == "目录绑定表" {
			n++
		}
		if c.Name == "无扩展名 shim" || c.Name == "绑定表原子写" {
			n++ // 这两项也归 DoctorBindings，只要求出现
		}
	}
	if n == 0 {
		t.Fatalf("DoctorBindings 的体检项没有接进 doctor：%+v", out)
	}
	// 接线不能重复：同名的总览项只允许一次
	seen := map[string]int{}
	for _, c := range out {
		if c.Name == "目录绑定表" {
			seen[c.Name]++
		}
	}
	if seen["目录绑定表"] > 1 {
		t.Fatalf("§7.3-2 总览被接了 %d 次（CLI 侧应不要再调一次）", seen["目录绑定表"])
	}
}
