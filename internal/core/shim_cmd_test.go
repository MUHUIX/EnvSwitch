package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// 层 2 shim 的真机行为测试（方案 §13-6 / §13-7 / §13-10 的机读版本）。
// 全部落在 t.TempDir() 里：绝不碰 %LOCALAPPDATA%\EnvSwitch 的真实数据。

const shimTestExe = `C:\EnvSwitch Test\envs.exe`

// stubShimExe 把生成期的 envs.exe 路径钉成固定串，方便断言「绝对路径注入位」。
func stubShimExe(t *testing.T) {
	t.Helper()
	old := shimExePath
	shimExePath = func() string { return shimTestExe }
	t.Cleanup(func() { shimExePath = old })
}

// shimHome 建一个假的 EnvSwitch home 并生成一条命令的双形态 shim，返回路径。
func shimHome(t *testing.T, tool, name, entry string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	ad := locJSAdapter{locAdapter: locAdapter{id: tool}, shimMap: map[string]string{name: entry}}
	if err := EnsureShims(&settings.Settings{}, []Adapter{ad}); err != nil {
		t.Fatalf("生成 shim: %v", err)
	}
	return home
}

// runCmd 用 cmd /d /c 跑一个 .cmd（/d 屏蔽 AutoRun，避免本机已装层 1 钩子时结果漂移）。
// 命令行手工拼：Go 会把内嵌引号转义成 \"，而 cmd 只认自己的引号规则，实测会把整条
// 命令当成一个不存在的程序名，所以必须走 SysProcAttr.CmdLine 绕过 Go 的转义。
func runCmd(t *testing.T, cwd, script, args string) (string, int) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("层 2 shim 是 Windows 形态，非 Windows 上跳过")
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	c := exec.Command(comspec)
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + comspec + `" /d /c "` + script + `" ` + args}
	if cwd != "" {
		c.Dir = cwd
	}
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	rc := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("起 cmd 失败: %v（输出：%s）", err, buf.String())
		}
		rc = ee.ExitCode()
	}
	return buf.String(), rc
}

// cleanPath 只留 System32：cmd 与 where 都找得到，但任何 node/npm 都不可能命中，
// 用来构造「全都没有」这条回退分支。
func cleanPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", filepath.Join(os.Getenv("SystemRoot"), "System32"))
}

// writeBindingTable 按接口契约生成扁平绑定表：UTF-8 无 BOM、CRLF、首行 #version=1、
// 数据行 cmpKey<TAB>tool<TAB>binDir<TAB>envPairs。
func writeBindingTable(t *testing.T, home string, rows [][4]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#version=1\r\n")
	for _, r := range rows {
		b.WriteString(strings.Join(r[:], "\t") + "\r\n")
	}
	p := filepath.Join(home, shimBindingTable)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestShimDumpBody 不是断言，是排障窗口：ENVS_DUMP_SHIM=1 时把生成正文打到日志，
// 用来核对模板在 Go 字符串拼接后是否还是合法 bat（%% 转义、GOTO 是否漏行都在这看）。
func TestShimDumpBody(t *testing.T) {
	if os.Getenv("ENVS_DUMP_SHIM") != "1" {
		t.Skip("设 ENVS_DUMP_SHIM=1 才打印生成正文")
	}
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	d, err := os.ReadFile(filepath.Join(home, "bin", "shims", "npm.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("\n%s", strings.ReplaceAll(string(d), "\r\n", "\n"))
}

// pathValue 取出子进程自报的 PATH 头部（统一大写，便于不区分大小写比前缀）。
func pathValue(t *testing.T, out string) string {
	t.Helper()
	for _, ln := range strings.Split(out, "\r\n") {
		if v, ok := strings.CutPrefix(strings.ToUpper(ln), "PATHFULL="); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("输出里没有 PATHFULL 行：%q", out)
	return ""
}

// fakeVersionDir 造一个「版本目录」形态：自带 <name>.cmd 启动器，打印标记、参数与
// 自己看到的 PATH 头部，然后按指定码退出 —— 用来验前置 PATH 与退出码透传。
func fakeVersionDir(t *testing.T, name string, rc int) string {
	t.Helper()
	dir := t.TempDir()
	body := "@ECHO off\r\n" +
		"ECHO MARK=" + name + " ARGS=%*\r\n" +
		"ECHO PATHFULL=%PATH%\r\n" +
		"EXIT /B " + itoa(rc) + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, name+".cmd"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

// §6.2-1 / §9-1：未激活 + 无绑定 → 回退系统同名命令，退出码透传（不再是 78）。
func TestShimCmdFallsBackToSystemCommand(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "zzprobe", "zzprobe", "bin/zz.js")
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "zzprobe.cmd"),
		[]byte("@ECHO off\r\nECHO SYS=%*\r\nEXIT /B 7\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", probe+";"+os.Getenv("PATH"))

	out, rc := runCmd(t, home, filepath.Join(home, "bin", "shims", "zzprobe.cmd"), "a b")
	t.Logf("输出：%s rc=%d", out, rc)
	if !strings.Contains(out, "SYS=a b") {
		t.Fatalf("没有回退到系统同名命令：%q", out)
	}
	if rc != 7 {
		t.Fatalf("退出码没透传，期望 7，实得 %d（原缺陷是恒 78）", rc)
	}
}

// §6.2-1 / §10：连系统同名命令都没有 → 只提示，退出码不得是 78。
func TestShimCmdNothingFoundHints(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "zznosuchtool", "zznosuchtool", "bin/zz.js")
	cleanPath(t) // 干净 PATH：where 一定找不到
	out, rc := runCmd(t, home, filepath.Join(home, "bin", "shims", "zznosuchtool.cmd"), "--version")
	t.Logf("输出：%s rc=%d", out, rc)
	if rc == 78 {
		t.Fatal("§9-1 未修：仍然 EXIT /B 78")
	}
	if rc != 1 {
		t.Fatalf("全都没有时期望 cmd 原生的 1，实得 %d", rc)
	}
	if !strings.Contains(out, `EnvSwitch: zznosuchtool is not available`) || !strings.Contains(out, shimTestExe) {
		t.Fatalf("提示语缺绝对路径 helper：%q", out)
	}
}

// §6.2 层 2 + §4.3-1：命中绑定表 → 前置版本目录、跑该目录自带启动器、透传其码。
func TestShimCmdBoundDirectoryWins(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	proj := t.TempDir()
	bound := fakeVersionDir(t, "npm", 42)
	// 同时造一个「已激活」的独立 npm 包：绑定命中必须优先于它
	act := filepath.Join(home, "current", "npm", "bin")
	if err := os.MkdirAll(act, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(act, "npm-cli.js"), []byte("console.log('ACTIVATED')\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBindingTable(t, home, [][4]string{{strings.ToLower(proj), "node", bound, ""}})

	out, rc := runCmd(t, proj, filepath.Join(home, "bin", "shims", "npm.cmd"), "run dev")
	t.Logf("输出：%s rc=%d", out, rc)
	if !strings.Contains(out, "MARK=npm ARGS=run dev") {
		dump, _ := os.ReadFile(filepath.Join(home, "bin", "shims", "npm.cmd"))
		tbl, _ := os.ReadFile(filepath.Join(home, shimBindingTable))
		t.Logf("生成的 shim 正文：\n%s\n---- 表 ----\n%q", dump, tbl)
		t.Fatalf("没有走绑定目录的启动器：%q", out)
	}
	if rc != 42 {
		t.Fatalf("绑定启动器的退出码没透传：%d", rc)
	}
	if !strings.HasPrefix(pathValue(t, out), strings.ToUpper(bound)) {
		t.Fatalf("子进程的 PATH 头部没有前置绑定目录：%q", out)
	}
	if strings.Contains(out, "ACTIVATED") {
		t.Fatal("绑定命中时不该跑独立激活包")
	}
}

// §13-6：绑定表里的注入串不得执行；坏行整行丢弃后仍要正常回退。
func TestShimCmdInjectionRowsDropped(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	proj := t.TempDir()
	k := strings.ToLower(proj)
	rows := [][4]string{
		// 方案 §13-6 原样的载荷：引号 + &
		{k, "node", `zz" & echo [PWNED_Q] & "`, ""},
		{k, "node", `e:" & echo [PWNED_DIR] & "`, ""},
		{k + `" & echo [PWNED_KEY] & "`, "node", `e:\bound_key`, ""},
		{k, "node", `e:\bad%PATH%x`, ""},                                          // 值含 % → 会在 PATH 里二次展开
		{k, "node", `e:\p|f`, ""},                                                 // 管道
		{k, "node", `e:\p^&g`, ""},                                                // 转义符
		{k, "node", `e:\p(1)`, ""},                                                // 括号块
		{k, "node", `e:\p;a=b`, ""},                                               // 分号：会往 PATH 里塞第二条目录
		{k, "no&de", `e:\bound_tool`, ""},                                         // 工具名带 &
		{k, "node", `e:\bound_pairs`, `A=1%26B=2`},                                // envPairs 里裸 & 已按契约 v2 编码成 %26
		{strings.ToLower(filepath.VolumeName(proj)), "node", `e:\bound_root`, ""}, // 裸盘根拒配（§4.2）
		{"e:\\bare", "node", `e:\bound_none`, ""},                                 // 不命中
	}
	writeBindingTable(t, home, rows)
	cleanPath(t) // 让它最终走 :envs_missing，便于断言「什么都没被执行」

	out, rc := runCmd(t, proj, filepath.Join(home, "bin", "shims", "npm.cmd"), "")
	t.Logf("输出：%s rc=%d", out, rc)
	for _, bad := range []string{"[PWNED_Q]", "[PWNED_DIR]", "[PWNED_KEY]", "bound_", "e:\\p", "was unexpected"} {
		if strings.Contains(out, bad) {
			t.Fatalf("注入/坏行未被丢弃，输出含 %q：%q", bad, out)
		}
	}
	if rc == 78 {
		t.Fatal("§9-1 未修：仍然 exit 78")
	}
	if rc != 1 {
		t.Fatalf("所有行都该被丢弃 → 期望 1，实得 %d（输出 %q）", rc, out)
	}
}

// 闸门不能过紧：带空格与中文的合法路径必须照样命中（真实项目就在 E:\公司项目\… 下，方案 §2-L14）。
func TestShimCmdSpaceAndCjkPathsStillHit(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	base := t.TempDir()
	proj := filepath.Join(base, "Proj A", "项目")
	front := filepath.Join(base, "Proj A-front")
	for _, d := range []string{proj, front} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bound := fakeVersionDir(t, "npm", 42)
	writeBindingTable(t, home, [][4]string{{strings.ToLower(proj), "node", bound, ""}})

	out, rc := runCmd(t, proj, filepath.Join(home, "bin", "shims", "npm.cmd"), "run dev")
	t.Logf("输出：%s rc=%d", out, rc)
	if !strings.Contains(out, "MARK=npm ARGS=run dev") || rc != 42 {
		t.Fatalf("带空格/中文的合法绑定被闸门误杀：out=%q rc=%d", out, rc)
	}
	cleanPath(t)
	out2, rc2 := runCmd(t, front, filepath.Join(home, "bin", "shims", "npm.cmd"), "")
	if strings.Contains(out2, "MARK=npm") || rc2 != 1 {
		t.Fatalf("前缀边界在带空格路径上失效：%q rc=%d", out2, rc2)
	}
}

// §2-L12 / §13-7：分隔符边界 —— 绑 E:\x\proj 时 E:\x\proj-front 不得命中。
func TestShimCmdPrefixBoundary(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	parent := t.TempDir()
	proj := filepath.Join(parent, "proj")
	front := filepath.Join(parent, "proj-front")
	for _, d := range []string{proj, front} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bound := fakeVersionDir(t, "npm", 42)
	writeBindingTable(t, home, [][4]string{{strings.ToLower(proj), "node", bound, ""}})

	// 子目录内：命中
	if out, _ := runCmd(t, proj, filepath.Join(home, "bin", "shims", "npm.cmd"), ""); !strings.Contains(out, "MARK=npm") {
		t.Fatalf("绑定目录本身应命中：%q", out)
	}
	// 同名前缀的兄弟目录：不得命中
	cleanPath(t)
	out, rc := runCmd(t, front, filepath.Join(home, "bin", "shims", "npm.cmd"), "")
	t.Logf("兄弟目录输出：%s rc=%d", out, rc)
	if strings.Contains(out, "MARK=npm") {
		t.Fatalf("§13-7 违例：E:\\..proj-front 命中了 ..proj 的绑定：%q", out)
	}
	if rc != 1 {
		t.Fatalf("未命中时应回退，期望 1，实得 %d", rc)
	}
}

// §4.3-1 嵌套：子目录绑定覆盖父目录绑定（最深匹配胜出）。
func TestShimCmdDeepestBindingWins(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	parent := t.TempDir()
	child := filepath.Join(parent, "sub")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	shallow := fakeVersionDir(t, "npm", 1)
	deep := fakeVersionDir(t, "npm", 2)
	rows := [][4]string{
		{strings.ToLower(child), "node", deep, ""},
		{strings.ToLower(parent), "node", shallow, ""},
	}
	// 故意把深层行放前面再排一次，确认结果与表内顺序无关
	if err := os.WriteFile(filepath.Join(home, shimBindingTable),
		[]byte(tableText(rows)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runCmd(t, child, filepath.Join(home, "bin", "shims", "npm.cmd"), "")
	t.Logf("输出：%s", out)
	if !strings.Contains(out, "MARK=npm ARGS=") || strings.Contains(out, "EXIT") {
		t.Fatalf("应有绑定命中：%q", out)
	}
	// 两条 fakeVersionDir 的 rc 不同，用 rc 判定谁胜出
	if rc := runRC(t, child, filepath.Join(home, "bin", "shims", "npm.cmd")); rc != 2 {
		// 换顺序再验一次：把浅层放前面
		sort.Slice(rows, func(i, j int) bool { return len(rows[i][0]) < len(rows[j][0]) })
		if err := os.WriteFile(filepath.Join(home, shimBindingTable), []byte(tableText(rows)), 0o644); err != nil {
			t.Fatal(err)
		}
		if rc2 := runRC(t, child, filepath.Join(home, "bin", "shims", "npm.cmd")); rc2 != 2 {
			t.Fatalf("最深匹配未胜出：顺序A rc=%d 顺序B rc=%d（期望都是 2）", rc, rc2)
		}
	}
}

func runRC(t *testing.T, cwd, script string) int {
	t.Helper()
	_, rc := runCmd(t, cwd, script, "")
	return rc
}

func tableText(rows [][4]string) string {
	var b strings.Builder
	b.WriteString("#version=1\r\n")
	for _, r := range rows {
		b.WriteString(strings.Join(r[:], "\t") + "\r\n")
	}
	return b.String()
}

// 裁决 §1-2 + 任务 6：只激活 node 时，npm 用它自带的 npm-cli，不再单独切 npm-cli。
func TestShimCmdRidesActivatedNodeOwnNpm(t *testing.T) {
	stubShimExe(t)
	if runtime.GOOS != "windows" {
		t.Skip("Windows 形态")
	}
	resolved, err := exec.LookPath("node")
	if err != nil {
		t.Skip("本机没有 node，无法验「npm 随激活 node 自带」这条")
	}
	nodeDir := filepath.Dir(resolved)
	entryJS := filepath.Join(nodeDir, "node_modules", "npm", "bin", "npm-cli.js")
	if _, err := os.Stat(entryJS); err != nil {
		t.Skipf("本机 node 目录不自带 npm-cli（%s），跳过", entryJS)
	}
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	link := filepath.Join(home, "current", "node")
	if err := sys.CreateJunction(link, nodeDir); err != nil {
		t.Fatalf("建测试 junction: %v", err)
	}
	out, rc := runCmd(t, home, filepath.Join(home, "bin", "shims", "npm.cmd"), "--version")
	t.Logf("npm --version → %s (rc=%d)", strings.TrimSpace(out), rc)
	if rc != 0 {
		t.Fatalf("激活 node 自带 npm 经 shim 调用失败 rc=%d：%q", rc, out)
	}
	if !regexp.MustCompile(`\d+\.\d+\.\d+`).MatchString(out) {
		t.Fatalf("输出不像版本号：%q", out)
	}
}

// §2-L10：查表的额外成本目标 <25 ms —— 同一分支下比「无表 / 24 行表」的差值。
func TestShimCmdLookupCost(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "zzperf", "zzperf", "bin/zz.js")
	cleanPath(t)
	cwd := t.TempDir()
	shim := filepath.Join(home, "bin", "shims", "zzperf.cmd")

	tblPath := filepath.Join(home, shimBindingTable)
	sample := func() time.Duration {
		var ts []time.Duration
		for i := 0; i < 7; i++ {
			s := time.Now()
			runCmd(t, cwd, shim, "")
			ts = append(ts, time.Since(s))
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
		return ts[len(ts)/2]
	}
	// 交替取样：先把「第一批吃冷启动 / 杀软首扫」的份额摊平
	_ = os.WriteFile(tblPath, []byte("#version=1\r\n"), 0o644)
	sample()
	_ = os.Remove(tblPath)
	sample()

	base := sample()
	var rows24 [][4]string
	for i := 0; i < 24; i++ {
		rows24 = append(rows24, [4]string{strings.ToLower(filepath.Join(cwd, "nope", itoa(i))), "node", `e:\x` + itoa(i), ""})
	}
	_ = os.WriteFile(tblPath, []byte(tableText(rows24)), 0o644)
	full := sample()
	hit := append([][4]string{{strings.ToLower(cwd), "node", `e:\xhit`, ""}}, rows24...)
	_ = os.WriteFile(tblPath, []byte(tableText(hit)), 0o644)
	early := sample()
	_ = os.WriteFile(tblPath, []byte(tableText(rows24[:3])), 0o644)
	three := sample()
	// 8 行采样点：用于给 lead 一个每行成本系数，好定护栏取值
	var rows8 [][4]string
	for i := 0; i < 8; i++ {
		rows8 = append(rows8, [4]string{strings.ToLower(filepath.Join(cwd, "n8", itoa(i))), "node", `e:\n8` + itoa(i), ""})
	}
	_ = os.WriteFile(tblPath, []byte(tableText(rows8)), 0o644)
	eight := sample()
	var rows120 [][4]string
	for i := 0; i < 120; i++ {
		rows120 = append(rows120, [4]string{strings.ToLower(filepath.Join(cwd, "nope", "longlonglong", itoa(i))), "node", `e:\y` + itoa(i), ""})
	}
	_ = os.WriteFile(tblPath, []byte(tableText(rows120)), 0o644)
	huge := sample()
	_ = os.Remove(tblPath)
	base2 := sample()

	t.Logf("cmd+回退基线 p50=%v（复测 %v）", base, base2)
	t.Logf("3 行未命中 p50=%v → 额外 %v", three, three-base)
	t.Logf("8 行未命中 p50=%v → 额外 %v（每行系数 ≈ %v）", eight, eight-base, (eight-base)/8)
	t.Logf("24 行未命中（护栏裁到 12 行）p50=%v → 额外 %v", full, full-base)
	t.Logf("120 行未命中（同一上界）p50=%v → 额外 %v", huge, huge-base)
	t.Logf("24 行 + 首行命中当前目录（STOP 早停）p50=%v → 额外 %v", early, early-base)
	if three-base > 25*time.Millisecond {
		t.Fatalf("小表（3 行）就超了 §2-L10 的 25ms 目标：%v", three-base)
	}
	if full-base > 120*time.Millisecond {
		t.Fatalf("24 行成本失控：%v", full-base)
	}
	// 护栏的意义就在这里：表从 24 行涨到 120 行，额外成本不该继续涨
	if huge-full > 25*time.Millisecond {
		t.Fatalf("护栏没起作用：120 行比 24 行多花 %v（应接近 0）", huge-full)
	}
}

// §6.2-4：双形态同时落盘、内容幂等、无扩展名形态是 LF + POSIX shebang。
func TestShimDualFormFiles(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	cmdPath := filepath.Join(home, "bin", "shims", "npm.cmd")
	shPath := filepath.Join(home, "bin", "shims", "npm")
	cmd1, err := os.ReadFile(cmdPath)
	if err != nil {
		t.Fatalf("缺 .cmd 形态: %v", err)
	}
	sh1, err := os.ReadFile(shPath)
	if err != nil {
		t.Fatalf("缺无扩展名形态（Git Bash 只认这条，§2-L7）: %v", err)
	}
	if !strings.HasPrefix(string(sh1), "#!/usr/bin/env sh\n") || bytes.Contains(sh1, []byte{'\r'}) {
		t.Fatalf("无扩展名形态必须 shebang 开头且纯 LF:\n%.80s", sh1)
	}
	if !bytes.Contains(cmd1, []byte("\r\n")) || bytes.Contains(cmd1, []byte("\r\r\n")) {
		t.Fatal(".cmd 形态必须是纯 CRLF")
	}
	if !strings.Contains(string(cmd1), `SET "EXE=`+shimTestExe+`"`) {
		t.Fatal(".cmd 没内嵌 envs.exe 绝对路径（§9-5）")
	}
	if !strings.Contains(string(sh1), `EXE="/c/envswitch test/envs.exe"`) &&
		!strings.Contains(string(sh1), "envs.exe") {
		t.Fatal("无扩展名形态没内嵌 envs.exe 绝对路径")
	}
	// 幂等：第二次生成不得改写文件
	fi1, _ := os.Stat(cmdPath)
	time.Sleep(30 * time.Millisecond)
	if err := EnsureShims(&settings.Settings{}, []Adapter{locJSAdapter{locAdapter: locAdapter{id: "npm"}, shimMap: map[string]string{"npm": "bin/npm-cli.js"}}}); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(cmdPath)
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("内容未变仍重写了文件，幂等口径破了")
	}
}

// 与 agent-B 写侧真实产物逐字对齐：第 4 列是 percent-encode 过的
// （实测行 `<cmpKey><TAB>node<TAB>E:\nvm\v16.20.2<TAB>NODE_OPTIONS=--max-old-space-size%3D4096`），
// 读侧闸门不得把它当坏行丢掉。
func TestShimCmdAcceptsRealGenTableRow(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	proj := t.TempDir()
	bound := fakeVersionDir(t, "npm", 42)
	rows := [][4]string{
		{strings.ToLower(proj), "node", bound, "NODE_OPTIONS=--max-old-space-size%3D4096"},
		{strings.ToLower(t.TempDir()), "go", `e:\go1`, "GOPATH=C%3A%5CGo"},
	}
	writeBindingTable(t, home, rows)
	out, rc := runCmd(t, proj, filepath.Join(home, "bin", "shims", "npm.cmd"), "-v")
	t.Logf("输出：%s rc=%d", out, rc)
	if !strings.Contains(out, "MARK=npm ARGS=-v") || rc != 42 {
		t.Fatalf("契约 v2 真实形态的行被误杀：out=%q rc=%d", out, rc)
	}
	// bat 侧只捕获不应用（lead R2）：envPairs 不得进子进程环境
	if strings.Contains(out, "4096") {
		t.Fatalf("层 2 不该应用 envPairs：%q", out)
	}
}

// lead 成本护栏（确定性口径）：shim 只认表里的**前 shimGenRowGuard 行**。
// B 的写侧按 key 长度倒序生成表 → 前 N 行就是最深/最具体的绑定，所以「裁掉尾部」
// 而不是「整表作废」，同一张表的行为才可预期。
//
// 同时这条用例守住 L17 那个真 bug：从被 CALL 出去的子程序里 GOTO 跳外部标签，
// cmd 会让命令被执行两遍 → 这里硬性断言 MARK 恰好出现一次。
func TestShimCmdRowGuardLimit(t *testing.T) {
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	proj := t.TempDir()
	bound := fakeVersionDir(t, "npm", 42)
	k := strings.ToLower(proj)
	shim := filepath.Join(home, "bin", "shims", "npm.cmd")

	filler := func(n int, tag string) [][4]string {
		var rows [][4]string
		for i := 0; i < n; i++ {
			rows = append(rows, [4]string{strings.ToLower(filepath.Join(proj, "f"+tag+itoa(i))), "node", `e:\f` + itoa(i), ""})
		}
		return rows
	}
	// A) 命中行在第 1 行、总行数超过护栏 → 仍然命中，且只执行一次
	rowsA := append([][4]string{{k, "node", bound, ""}}, filler(shimGenRowGuard, "a")...)
	writeBindingTable(t, home, rowsA)
	out, rc := runCmd(t, proj, shim, "")
	if n := strings.Count(out, "MARK=npm"); n != 1 {
		t.Fatalf("绑定命令被执行了 %d 次（应为 1 次，L17 双执行回归）：%q", n, out)
	}
	if rc != 42 {
		t.Fatalf("护栏不应裁掉前 %d 行内的命中：rc=%d out=%q", shimGenRowGuard, rc, out)
	}
	// B) 命中行落在第 13 行（= 护栏之外）→ 必须被裁掉，回落到激活/系统链
	rowsB := append(filler(shimGenRowGuard, "b"), [4]string{k, "node", bound, ""})
	writeBindingTable(t, home, rowsB)
	cleanPath(t)
	out2, rc2 := runCmd(t, proj, shim, "")
	t.Logf("命中行在第 %d 行 → rc=%d：%s", len(rowsB), rc2, out2)
	if strings.Contains(out2, "MARK=npm") {
		t.Fatalf("超过护栏 %d 行的条目不该在层 2 生效：%q", shimGenRowGuard, out2)
	}
	if rc2 != 1 {
		t.Fatalf("停用后回落到系统链，期望 1，实得 %d", rc2)
	}
	// C) 深目录（走满 8 级祖先）× 60 行大表：成本上界与深度和表大小无关
	deep := proj
	for i := 0; i < 10; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([][4]string, 0, 60)
	for i := 0; i < 60; i++ {
		big = append(big, [4]string{strings.ToLower(filepath.Join(deep, "no", itoa(i))), "node", `e:\b` + itoa(i), ""})
	}
	writeBindingTable(t, home, big)
	var ts []time.Duration
	for i := 0; i < 5; i++ {
		s := time.Now()
		out3, _ := runCmd(t, deep, shim, "")
		ts = append(ts, time.Since(s))
		if strings.Contains(out3, "MARK=npm") {
			t.Fatalf("60 行大表在深目录下也不该命中：%q", out3)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	t.Logf("最坏组合（10 级深 cwd × 60 行表）p50=%v —— 护栏生效则应接近基线，不随表大小线性增长", ts[len(ts)/2])
	if ts[len(ts)/2] > 400*time.Millisecond {
		t.Fatalf("护栏没有把成本压成常数：p50=%v", ts[len(ts)/2])
	}
}

// §13-1 / §13-4 口径（lead 2026-11-19 补充）：层 2 兜底是否真生效，判据必须是子进程的
// process.execPath 落在绑定版本目录内 —— 本机全局 node 恰好与某个版本同号会掩盖一切，
// 「命令跑通了」与「npm -v 字符串对」都不算证据。
// 表由 agent-B 的写侧口径手工构形（4 列 + 第 4 列 percent-encode），node 目录取真机 nvm 根。
func TestShimCmdBoundVersionAppliesToExecPath(t *testing.T) {
	stubShimExe(t)
	if runtime.GOOS != "windows" {
		t.Skip("Windows 形态")
	}
	// 找一个与「系统当前 node」不同号的真实 node 目录作为绑定目标，才有判别性
	cur, err := exec.LookPath("node")
	if err != nil {
		t.Skip("等待前置条件：本机没有 node")
	}
	curDir := filepath.Dir(cur)
	var other string
	for _, root := range []string{`E:\nvm`, filepath.Join(os.Getenv("APPDATA"), "nvm")} {
		matches, _ := filepath.Glob(filepath.Join(root, "v*"))
		for _, m := range matches {
			if strings.EqualFold(filepath.Dir(m), filepath.Clean(root)) && !strings.EqualFold(m, curDir) {
				if _, e := os.Stat(filepath.Join(m, "node.exe")); e == nil {
					other = m
					break
				}
			}
		}
		if other != "" {
			break
		}
	}
	if other == "" {
		t.Skipf("等待前置条件：找不到与 %s 不同的第二个 node 目录，无法构成判别性断言", curDir)
	}
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "probe.js"),
		[]byte("console.log('EXEC='+process.execPath+' VER='+process.version)\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "package.json"),
		[]byte("{\n  \"name\": \"fx\",\n  \"version\": \"1.0.0\",\n  \"scripts\": { \"probe\": \"node probe.js\" }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBindingTable(t, home, [][4]string{{strings.ToLower(proj), "node", other, `NODE_OPTIONS=--max-old-space-size%3D4096`}})

	out, rc := runCmd(t, proj, filepath.Join(home, "bin", "shims", "npm.cmd"), "run probe")
	t.Logf("绑定 %s 后 npm run probe → rc=%d\n%s", other, rc, out)
	if rc != 0 {
		t.Fatalf("npm run probe 失败 rc=%d：%q", rc, out)
	}
	want := strings.ToUpper(filepath.Clean(other))
	if !strings.Contains(strings.ToUpper(out), "EXEC="+want) {
		t.Fatalf("子进程 execPath 没落到绑定目录（期望前缀 %s）：%q", want, out)
	}
	// 对照：同一 shim 在无绑定的目录里必须给出另一个 node
	bare := t.TempDir()
	for _, f := range []string{"probe.js", "package.json"} {
		b, _ := os.ReadFile(filepath.Join(proj, f))
		if err := os.WriteFile(filepath.Join(bare, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out2, _ := runCmd(t, bare, filepath.Join(home, "bin", "shims", "npm.cmd"), "run probe")
	t.Logf("对照（无绑定目录）→ %s", strings.TrimSpace(out2))
	if strings.Contains(strings.ToUpper(out2), "EXEC="+want) {
		t.Fatalf("对照组也命中了绑定目录，说明匹配条件写宽了：%q", out2)
	}
}

// R11 守门人（lead 2026-11-19 裁定的根因修复）：写 shim 的 exe 若处在临时/开发位置，
// 绝不允许把那个路径烤进用户机器 —— 真实 home 上曾出现过 10 个 shim 指向已删除的
// %TEMP%\envs_real\envs.exe。go test 的二进制本身就落在 %TEMP% 下，正是同一类构建，
// 所以这条断言在普通 `go test` 下就在覆盖真实场景，零污染、不起子进程。
func TestShimNeverBakesTempBuildPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	exeDir := settings.ExeDir()
	resolved := defaultShimExePath()
	t.Logf("os.Executable 目录=%s 稳定=%v 解析出的注入位=%s", exeDir, stableInstallDir(exeDir), resolved)

	if stableInstallDir(exeDir) {
		if filepath.Clean(resolved) != filepath.Clean(filepath.Join(exeDir, "envs.exe")) {
			t.Fatalf("稳定安装位应原样烘焙自身目录：%s → %s", exeDir, resolved)
		}
	} else {
		want := filepath.Join(home, "bin", "envs.exe")
		if filepath.Clean(resolved) != filepath.Clean(want) {
			t.Fatalf("临时/开发构建必须落约定位置 %s，实得 %s", want, resolved)
		}
	}
	// 恒定不变式：注入位只能来自「稳定安装位」或「约定 home\bin」，绝不能来自其它不稳定目录
	cmd := shimScript("npm", "bin/npm-cli.js", "npm", resolved)
	sh := posixShimScript("npm", "bin/npm-cli.js", "npm", resolved)
	exeDirLower := strings.ToLower(filepath.Clean(exeDir)) + string(filepath.Separator)
	for kind, body := range map[string]string{"cmd": cmd, "sh": sh} {
		if !stableInstallDir(exeDir) && strings.Contains(strings.ToLower(body), exeDirLower) {
			t.Fatalf("%s 形态把不稳定的 exe 目录（临时/开发构建）烤进了用户机器的 shim：%s", kind, exeDir)
		}
		if strings.Contains(strings.ToLower(body), `\build\`) || strings.Contains(strings.ToLower(body), `/build/`) ||
			strings.Contains(strings.ToLower(body), `\.t\`) {
			t.Fatalf("%s 形态出现了构建产物目录路径（R11 违例）", kind)
		}
	}
	if !strings.Contains(cmd, "conventional helper location") {
		t.Fatal("cmd 头部 REM 没向用户说明 EXE 为什么是约定位置（R11-4）")
	}
	if !strings.Contains(sh, "conventional location") {
		t.Fatal("sh 头部注释没向用户说明 EXE 为什么是约定位置（R11-4）")
	}
}

// EnsureShims 落盘后的成品也要守同一条：成品里出现的是解析出的注入位，而不是写它的那台
// 机器上 exe 所在的（可能临时的）目录。注意 ENVS_HOME 被测试指到 temp 时，约定位置本身
// 也在 temp 下 —— 那是对的，判据必须是「不得来自 exe 自身目录」而不是「不得含 temp」。
func TestEnsureShimsDroppedFilesBakeResolvedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	ad := locJSAdapter{locAdapter: locAdapter{id: "npm"}, shimMap: map[string]string{"npm": "bin/npm-cli.js"}}
	if err := EnsureShims(&settings.Settings{}, []Adapter{ad}); err != nil {
		t.Fatal(err)
	}
	wantWin := strings.ToLower(filepath.Clean(defaultShimExePath()))
	wantSh := strings.ToLower(toPosixPath(defaultShimExePath()))
	exeDir := strings.ToLower(filepath.Clean(settings.ExeDir())) + string(filepath.Separator)
	for _, name := range []string{"npm.cmd", "npm"} {
		b, err := os.ReadFile(filepath.Join(home, "bin", "shims", name))
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(string(b))
		// .cmd 用盘符形态，无扩展名形态用 /c/… 的 POSIX 形态（§2-L7）
		want := wantWin
		if name != "npm.cmd" {
			want = wantSh
		}
		if !strings.Contains(low, want) {
			t.Fatalf("%s 里没有解析出的注入位 %s", name, want)
		}
		if !stableInstallDir(settings.ExeDir()) && strings.Contains(low, exeDir) {
			t.Fatalf("%s 把不稳定的 exe 目录烤进了成品 shim：%s", name, exeDir)
		}
	}
}

// 稳定位判据本身：三条各自都要生效（测试机上 temp 与源码工作区会重叠，所以拆着测）。
func TestStableInstallDirPredicates(t *testing.T) {
	tmp := filepath.Clean(os.TempDir())
	if !underTempDir(filepath.Join(tmp, "go-build12345", "b001")) || !underTempDir(tmp) {
		t.Fatal("os.TempDir() 及其子目录必须判为不稳定")
	}
	if underTempDir(`C:\Program Files\EnvSwitch`) {
		t.Fatal("正常安装位不该判为 temp")
	}
	for _, s := range []string{`D:\work\EnvSwitch\build\bin`, `D:\work\EnvSwitch\.t`, `X:\build\out`} {
		if !buildArtifactDir(s) {
			t.Fatalf("%s 应判为构建产物目录", s)
		}
	}
	if buildArtifactDir(`C:\Program Files\EnvSwitch\bin`) {
		t.Fatal("普通安装目录被误判为构建产物")
	}
	// insideSourceRepo：本次测试的工作目录就在仓库工作区内（.git 在上溯路径上）
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if !insideSourceRepo(wd) {
		t.Skipf("工作区 %s 上溯找不到 .git（非常规 checkout），跳过", wd)
	}
	// 自己造的 .git 目录（不在 temp 判据之外，但能单独验证 stat 分支）
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !insideSourceRepo(filepath.Join(repo, "internal", "core")) {
		t.Fatal(".git 应沿父链被识别")
	}
	if !stableInstallDir(`C:\Program Files\EnvSwitch`) {
		t.Fatal("程序安装目录应判为稳定")
	}
}

// §6.2 三铁律 + §9-1/§9-5 的全局机读守卫（两种形态一起查）。
func TestShimIronLaws(t *testing.T) {
	stubShimExe(t)
	cases := map[string]string{
		"cmd": shimScript("npm", "bin/npm-cli.js", "npm", shimTestExe),
		"sh":  posixShimScript("npm", "bin/npm-cli.js", "npm", `C:\EnvSwitch Test\envs.exe`),
	}
	for kind, s := range cases {
		// 只看可执行行：模板的 REM 注释里会写明「No EXIT /B 78 any more」，那是说明不是代码
		code := strings.Join(nonCommentLines(s), "\n")
		if strings.Contains(code, "EXIT /B 78") || strings.Contains(code, "exit 78") {
			t.Fatalf("%s 形态仍残留 78（§9-1）", kind)
		}
		if strings.Contains(code, ") ELSE (") {
			t.Fatalf("%s 形态用了 IF(...)ELSE(...) 括号块（§6.2 铁律①）", kind)
		}
		if !strings.Contains(s, "%~dp0..\\..") && !strings.Contains(s, "$SELF/../..") {
			t.Fatalf("%s 形态没有用 %%%%~dp0 / $0 自定位 home（§6.2-2）", kind)
		}
		if kind == "cmd" && !strings.Contains(s, "%%~fI") {
			t.Fatalf("cmd 形态丢了 %%~fI 展平（§6.2 铁律②）")
		}
		// §9-5：可执行行里不得出现裸名 envs 调用；只有内嵌的绝对路径 EXE 变量。
		// （注释里当然会写「a bare "envs" is shadowed by…」，所以只查 code。）
		for _, bare := range []string{`call envs `, `call "envs" `, `"envs" `, `!ENVS! `, `"%ENVS%"`, `$ENVS `, `"$ENVS"`} {
			if strings.Contains(code, bare) {
				t.Fatalf("%s 形态出现裸名 envs 调用 %q（§9-5 会被 cmd 隐式当前目录抢先）", kind, bare)
			}
		}
		if !strings.Contains(s, "EnvSwitch:") {
			t.Fatalf("%s 形态没有英文提示语（§6.2 铁律③）", kind)
		}
		for _, ln := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
			if strings.HasPrefix(ln, "REM ") || strings.HasPrefix(ln, "#") {
				continue
			}
			if hasNonASCII(ln) {
				t.Fatalf("%s 形态的可执行行含非 ASCII（bat 按 GBK 解码必乱，铁律③）：%q", kind, ln)
			}
		}
	}
}

func hasNonASCII(s string) bool {
	for _, r := range s {
		if r > 0x7f {
			return true
		}
	}
	return false
}

// nonCommentLines 滤掉 REM / # 开头的注释行与空行，让断言只针对真正会被执行的内容。
func nonCommentLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "REM ") || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// findPosixShell 定位 Git for Windows 的 sh.exe。
// 教训（lead 2026-11-19 指正）：只查 PATH 与 C 盘 Program Files 不够 —— 本机 Git 装在 D:\Git，
// 别人装 E 盘或用户目录同样常见，所以判据要覆盖非标准安装位，探不到才允许 SKIP。
func findPosixShell() string {
	var candidates []string
	for _, c := range []string{"sh", "bash"} {
		if p, err := exec.LookPath(c); err == nil {
			candidates = append(candidates, p)
		}
	}
	if g, err := exec.LookPath("git"); err == nil {
		// git.exe 常规在 <root>\cmd\git.exe
		candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(g)), "bin", "sh.exe"))
		// 或从 --exec-path（<root>/mingw64/libexec/git-core）反推安装根
		if out, err := exec.Command(g, "--exec-path").Output(); err == nil {
			root := strings.TrimSpace(string(out))
			for i := 0; i < 3 && root != ""; i++ {
				root = filepath.Dir(root)
			}
			if root != "" && len(root) <= 3 {
				candidates = append(candidates,
					filepath.Join(root, "bin", "sh.exe"),
					filepath.Join(root, "usr", "bin", "sh.exe"))
			}
		}
	}
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if base := os.Getenv(key); base != "" {
			candidates = append(candidates,
				filepath.Join(base, `Git\bin\sh.exe`),
				filepath.Join(base, `Git\usr\bin\sh.exe`))
		}
	}
	for _, drv := range []string{"C:", "D:", "E:", "F:"} {
		for _, sub := range []string{`\Git\bin\sh.exe`, `\git\bin\sh.exe`, `\Program Files\Git\bin\sh.exe`} {
			candidates = append(candidates, drv+sub)
		}
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// runSh 用 sh 直接执行无扩展名形态（MSYS 收得下正斜杠路径）。
func runSh(t *testing.T, sh, cwd, script, args string) (string, int) {
	t.Helper()
	c := exec.Command(sh, filepath.ToSlash(script))
	if args != "" {
		c.Args = append(c.Args, strings.Fields(args)...)
	}
	c.Dir = cwd
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	rc := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("起 sh 失败: %v（%s）", err, buf.String())
		}
		rc = ee.ExitCode()
	}
	return buf.String(), rc
}

func shInline(t *testing.T, sh, cwd, inline string, env []string) (string, int) {
	t.Helper()
	c := exec.Command(sh, "-c", inline)
	c.Dir = cwd
	if env != nil {
		c.Env = env
	}
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	rc := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else {
			t.Fatalf("起 sh 失败: %v", err)
		}
	}
	return buf.String(), rc
}

// §6.2-4 / §2-L7 的决定性断言：Git Bash 不做 PATHEXT 补全，只发 .cmd 时命令根本不存在；
// 双形态落地后 `command -v npm` 必须命中我们那个无扩展名入口。
func TestShimShVisibleToGitBash(t *testing.T) {
	sh := findPosixShell()
	if sh == "" {
		t.Skip("真·等待前置条件：翻遍 PATH / git --exec-path / 四个盘符常见目录都没找到 sh.exe")
	}
	t.Logf("sh = %s", sh)
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	shims := filepath.Join(home, "bin", "shims")
	env := append([]string{}, "PATH="+shims+";C:\\Windows\\System32")
	out, rc := shInline(t, sh, home, `command -v npm; echo RC=$?`, env)
	t.Logf("command -v npm → %s (rc=%d)", strings.TrimSpace(out), rc)
	got := strings.TrimSpace(strings.Split(out, "\n")[0])
	// Git Bash 可能把 %TEMP% 挂成 /tmp，所以只比与挂载写法无关的两点：末段是 npm、路径里有 shims
	if !strings.HasSuffix(strings.ToLower(got), "/npm") || !strings.Contains(strings.ToLower(got), "/shims/") {
		t.Fatalf("Git Bash 里没命中无扩展名 shim（§6.2-4 失效）：got=%q", got)
	}
	if strings.Contains(strings.ToLower(got), ".cmd") {
		t.Fatalf("MSYS 不该补 PATHEXT 命中 .cmd：%q", got)
	}
}

// §9-1 在 POSIX 侧同样成立：未激活 → 回退系统同名命令并透传退出码，不是 78。
func TestShimShFallsBackToSystemCommand(t *testing.T) {
	sh := findPosixShell()
	if sh == "" {
		t.Skip("找不到 sh.exe")
	}
	stubShimExe(t)
	home := shimHome(t, "zzshprobe", "zzshprobe", "bin/zz.js")
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "zzshprobe"),
		[]byte("#!/usr/bin/env sh\necho SYS=$*\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// probe 目录要在 PATH 上才算「系统同名命令」；shims 目录本身由 $0 定位，不需要在 PATH 上
	t.Setenv("PATH", probe+";C:\\Windows\\System32")
	shims := filepath.Join(home, "bin", "shims")
	out, rc := runSh(t, sh, home, filepath.Join(shims, "zzshprobe"), "a b")
	t.Logf("输出：%s rc=%d", out, rc)
	if !strings.Contains(out, "SYS=a b") {
		t.Fatalf("无扩展名形态没有回退系统同名命令：%q", out)
	}
	if rc != 7 || rc == 78 {
		t.Fatalf("退出码没透传（期望 7，实得 %d）：%q", rc, out)
	}
}

// 连系统同名命令都没有 → POSIX 惯例 127，且绝不残留 78。
func TestShimShNothingFound(t *testing.T) {
	sh := findPosixShell()
	if sh == "" {
		t.Skip("找不到 sh.exe")
	}
	stubShimExe(t)
	home := shimHome(t, "zzshnosuch", "zzshnosuch", "bin/zz.js")
	// PATH 只留 System32：command -v 一定落空
	shims := filepath.Join(home, "bin", "shims")
	c := exec.Command(sh, filepath.ToSlash(filepath.Join(shims, "zzshnosuch")))
	c.Env = []string{"PATH=C:\\Windows\\System32", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	_ = c.Run()
	rc := c.ProcessState.ExitCode()
	t.Logf("输出：%s rc=%d", buf.String(), rc)
	if rc == 78 {
		t.Fatal("§9-1 未修：POSIX 侧仍退 78")
	}
	if rc != 127 {
		t.Fatalf("POSIX 找不到命令应 127，实得 %d（%q）", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "EnvSwitch: zzshnosuch is not available") ||
		!strings.Contains(buf.String(), "envs.exe") {
		t.Fatalf("提示语缺绝对 helper 路径：%q", buf.String())
	}
}

// 裁决 §1-2 在 Git Bash 侧同样成立：只激活 node，npm 就用它自带的 npm-cli。
func TestShimShRidesActivatedNodeOwnNpm(t *testing.T) {
	sh := findPosixShell()
	if sh == "" {
		t.Skip("找不到 sh.exe")
	}
	stubShimExe(t)
	resolved, err := exec.LookPath("node")
	if err != nil {
		t.Skip("本机没有 node")
	}
	nodeDir := filepath.Dir(resolved)
	if _, e := os.Stat(filepath.Join(nodeDir, "node_modules", "npm", "bin", "npm-cli.js")); e != nil {
		t.Skipf("本机 node 不自带 npm-cli：%s", nodeDir)
	}
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	if err := os.MkdirAll(filepath.Join(home, "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sys.CreateJunction(filepath.Join(home, "current", "node"), nodeDir); err != nil {
		t.Fatalf("建测试 junction: %v", err)
	}
	out, rc := runSh(t, sh, home, filepath.Join(home, "bin", "shims", "npm"), "--version")
	t.Logf("sh 里 npm --version → %s rc=%d", strings.TrimSpace(out), rc)
	if rc != 0 || !regexp.MustCompile(`\d+\.\d+\.\d+`).MatchString(out) {
		t.Fatalf("POSIX 侧没跟随激活 node 自带的 npm：rc=%d out=%q", rc, out)
	}
}

// 层 2 的定位（§1-1）：层 1 钩子把版本目录前置后，命令必须落在那个目录，
// 我们的 shim 不许反过来遮挡它。
func TestShimShDoesNotShadowInjectedVersionDir(t *testing.T) {
	sh := findPosixShell()
	if sh == "" {
		t.Skip("找不到 sh.exe")
	}
	stubShimExe(t)
	home := shimHome(t, "npm", "npm", "bin/npm-cli.js")
	bound := t.TempDir()
	// 版本目录自带的 POSIX 入口（node 发行包就是这么发的，§2-L7）
	if err := os.WriteFile(filepath.Join(bound, "npm"), []byte("#!/usr/bin/env sh\necho BOUND=$*\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bound+";"+filepath.Join(home, "bin", "shims")+";C:\\Windows\\System32")
	out, _ := shInline(t, sh, home, `command -v npm`, nil)
	got := strings.ToLower(filepath.ToSlash(strings.TrimSpace(out)))
	// 注意：Git Bash 会把 %TEMP% 挂成 /tmp，所以不能拿 Windows 前缀比 —— 这里比「末两级」
	// （<绑定目录名>/npm）并断言不是 shims 里那个，两者都与挂载写法无关。
	wantTail := "/" + strings.ToLower(filepath.Base(bound)) + "/npm"
	if !strings.HasSuffix(got, wantTail) {
		t.Fatalf("前置的版本目录没压住 shims（层 1 会被层 2 遮挡）：got=%q want 结尾 %q", got, wantTail)
	}
	if strings.Contains(got, "/shims/") {
		t.Fatalf("命中的仍是 shims：%q", got)
	}
	out2, rc2 := shInline(t, sh, home, `npm run probe`, nil)
	t.Logf("PATH 头部命中后由 bound 执行：%s rc=%d", strings.TrimSpace(out2), rc2)
	if !strings.Contains(out2, "BOUND=run probe") {
		t.Fatalf("没执行到绑定目录里的入口：%q", out2)
	}
}

// 旧探测面（只查 PATH + C 盘标准位）在本机会误判「没有 sh」——这条把教训固化成断言。
func TestFindPosixShellCoversNonStandardDrive(t *testing.T) {
	sh := findPosixShell()
	if sh == "" {
		t.Skip("这台机器确实没有 Git for Windows")
	}
	if !filepath.IsAbs(sh) {
		t.Fatalf("定位结果必须是绝对路径：%q", sh)
	}
	t.Logf("定位到 sh：%s（盘符 %s，非 C 盘标准位置也要能命中）", sh, filepath.VolumeName(sh))
}
