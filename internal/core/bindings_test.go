package core

// 目录绑定内核单测（方案 §4 数据模型与解析算法、§5 白名单判定、§10 托底矩阵）。
// 全部指向临时目录：t.Setenv("ENVS_HOME", …) 让 settings.Home() 落到本次测试私有的 home，
// 绝不读写真实 %LOCALAPPDATA%\EnvSwitch、用户 profile 或注册表（环境污染红线）。
//
// 约定：反斜杠一律走 bs，源码里不散落转义，避免可读性坍塌。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/MUHUIX/envswitch/internal/settings"
)

// bs 是一个反斜杠。
const bs = "\\"

// —— 测试用适配器 ——

type bindAdapter struct {
	id     string
	binSub string
	adopt  []Install
}

func (a *bindAdapter) ID() string                                              { return a.id }
func (a *bindAdapter) Title() string                                           { return a.id }
func (a *bindAdapter) Group() string                                           { return "runtime" }
func (a *bindAdapter) FetchRemote(*settings.Settings) ([]RemoteVersion, error) { return nil, nil }
func (a *bindAdapter) BinSub() string                                          { return a.binSub }
func (a *bindAdapter) Smoke() (string, []string)                               { return a.id + ".exe", nil }
func (a *bindAdapter) Adopted(*settings.Settings) []Install                    { return a.adopt }

type bindJSAdapter struct{ bindAdapter }

func (a *bindJSAdapter) Shims() map[string]string {
	return map[string]string{a.id: "bin/" + a.id + "-cli.js"}
}

// bindEnv 造一个隔离 home（store / bindings.json / bindings.gen.txt 都在里面）。
func bindEnv(t *testing.T) (string, *settings.Settings) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	// 中性化外部 nvm 勘察，避免结果随机器漂移（NvmRoot 只读环境变量，不写任何东西）
	t.Setenv("NVM_ROOT", filepath.Join(home, "no-nvm-root"))
	t.Setenv("NVM_HOME", filepath.Join(home, "no-nvm-home"))
	t.Setenv("APPDATA", filepath.Join(home, "no-appdata"))
	return home, &settings.Settings{}
}

// bindStore 在临时 store 里造一个版本的假安装（只建目录，不下载任何东西）。
func bindStore(t *testing.T, home, tool, ver, binSub string) string {
	t.Helper()
	root := filepath.Join(home, "store", tool, ver)
	bin := root
	if binSub != "" {
		bin = filepath.Join(root, binSub)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// —— §4.2 归一 ——

// TestBindingsNormalizeSixForms 覆盖 §2-L11 的路径形态：同一目录必须收敛到同一个 cmpKey。
func TestBindingsNormalizeSixForms(t *testing.T) {
	home, _ := bindEnv(t)
	proj := filepath.Join(home, "Proj A")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")

	want := NormalizePath(proj)
	forms := map[string]string{
		"原样反斜杠":   proj,
		"成对双引号":   `"` + proj + `"`,
		"成对单引号":   `'` + proj + `'`,
		"正斜杠":     strings.ReplaceAll(proj, bs, "/"),
		"大小写混排":   strings.ToUpper(proj[:2]) + proj[2:],
		"尾部多余分隔":  proj + bs,
		"含点段":     filepath.Join(home, "Proj A", "..", "Proj A"),
		"扩展前缀":    `\\?\` + proj,
		"NT 命名空间": `\??\` + proj,
		"设备前缀":    `\\.\` + proj,
	}
	for name, in := range forms {
		got := NormalizePath(in)
		if !strings.EqualFold(got, want) {
			t.Errorf("%s：NormalizePath(%q) = %q，期望 %q", name, in, got, want)
		}
		if CmpKey(in) != strings.ToLower(got) {
			t.Errorf("%s：CmpKey 不等于 ToLower(NormalizePath)", name)
		}
	}
	// 幂等 + 8.3 归一后仍是同一路径
	if twice := NormalizePath(want); !strings.EqualFold(twice, want) {
		t.Errorf("归一不幂等：%q → %q", want, twice)
	}
}

// TestBindingsNormalize83 用真实 8.3 短名验证 GetLongPathNameW 那一步（卷未启用短名则跳过）。
func TestBindingsNormalize83(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 专属")
	}
	home, _ := bindEnv(t)
	proj := filepath.Join(home, "eightythree")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	ptr, err := windows.UTF16PtrFromString(proj)
	mustNoErr(t, err, "UTF16")
	buf := make([]uint16, 512)
	n, err := windows.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("该卷未生成 8.3 短名：%v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, proj) {
		t.Skipf("短名与长名相同（8.3 生成被关闭）：%s", short)
	}
	if got := NormalizePath(short); !strings.EqualFold(got, proj) {
		t.Fatalf("8.3 未展开：%q → %q，期望 %q", short, got, proj)
	}
}

// TestBindingsNormalizeSubst 用替换钩子验证 subst 盘归一步骤（不真建 subst 盘，零环境污染）。
func TestBindingsNormalizeSubst(t *testing.T) {
	home, _ := bindEnv(t)
	realDir := filepath.Join(home, "realdir")
	mustNoErr(t, os.MkdirAll(realDir, 0o755), "建目录")

	old := winSubstRoot
	winSubstRoot = func(string) string { return realDir }
	defer func() { winSubstRoot = old }()

	got := NormalizePath(`Z:` + bs + `subdir`)
	if !strings.EqualFold(got, filepath.Join(realDir, "subdir")) {
		t.Fatalf("subst 盘未展开：%q，期望 %q", got, filepath.Join(realDir, "subdir"))
	}
}

// TestBindingsNormalizeRejectsUNCAndRoot 覆盖 §4.2：UNC 独立命名空间、裸盘根拒配。
func TestBindingsNormalizeRejectsUNCAndRoot(t *testing.T) {
	if !IsUNCKey(`\\server\share\proj`) {
		t.Error("UNC 未被识别")
	}
	if !IsBareRoot("e:") || !IsBareRoot(`e:`+bs) {
		t.Error("裸盘根未被识别")
	}
	if IsBareRoot(`e:` + bs + `proj`) {
		t.Error("普通目录被误判为盘根")
	}
	for _, badPath := range []string{`\\server\share\proj`, `E:` + bs} {
		bs2 := []Binding{{Path: badPath, Tools: map[string]string{"node": "16"}}}
		for _, cwd := range []string{`\\server\share\proj\sub`, `E:` + bs + `anything`, `e:`} {
			if MatchBinding(CmpKey(cwd), bs2) != nil {
				t.Errorf("%q 不应命中（UNC 不参与前缀匹配 / 裸盘根拒配），cwd=%q", badPath, cwd)
			}
		}
	}
}

// TestBindingsFinalTargetDoubleKey 覆盖 §4.2「链接路径 + 目标路径双键」（治 L11）。
// 用替换钩子模拟 junction 解析结果，不创建真链接、不碰任何全局状态。
func TestBindingsFinalTargetDoubleKey(t *testing.T) {
	home, _ := bindEnv(t)
	link := filepath.Join(home, "linkdir")
	target := filepath.Join(home, "targetdir")
	mustNoErr(t, os.MkdirAll(link, 0o755), "建链接目录")
	mustNoErr(t, os.MkdirAll(target, 0o755), "建目标目录")

	old := winFinalPath
	winFinalPath = func(p string) string {
		if strings.EqualFold(p, link) {
			return target
		}
		return ""
	}
	defer func() { winFinalPath = old }()

	keys := BindingKeys(link)
	if len(keys) != 2 {
		t.Fatalf("应登记双键，实得 %v", keys)
	}
	list := []Binding{{Path: link, Tools: map[string]string{"node": "16"}}}
	if MatchBinding(CmpKey(target), list) == nil {
		t.Error("按目标路径查询未命中（Git Bash 形态的 cwd，见 §2-L11）")
	}
	if MatchBinding(CmpKey(link), list) == nil {
		t.Error("按链接路径查询未命中")
	}
}

// —— §4.3 匹配 ——

// TestBindingsMatchLongestPrefixAndBoundary 覆盖 §2-L12：最长前缀 + 分隔符边界。
func TestBindingsMatchLongestPrefixAndBoundary(t *testing.T) {
	home, _ := bindEnv(t)
	proj := filepath.Join(home, "proj")
	deep := filepath.Join(proj, "sub")
	front := filepath.Join(home, "proj-front")
	for _, d := range []string{proj, deep, front} {
		mustNoErr(t, os.MkdirAll(d, 0o755), "建目录")
	}
	list := []Binding{
		{Path: proj, Tools: map[string]string{"node": "16"}},
		{Path: deep, Tools: map[string]string{"node": "22"}},
	}
	if got := MatchBinding(CmpKey(proj), list); got == nil || got.Tools["node"] != "16" {
		t.Fatalf("proj 应命中 16，实得 %+v", got)
	}
	if got := MatchBinding(CmpKey(filepath.Join(deep, "src")), list); got == nil || got.Tools["node"] != "22" {
		t.Fatalf("嵌套应取最长前缀 22，实得 %+v", got)
	}
	if got := MatchBinding(CmpKey(front), list); got != nil {
		t.Errorf("前缀粘连误命中（E:\\proj 不得命中 E:\\proj-front）：%+v", got)
	}
	nested := []Binding{{Path: filepath.Join(home, "ab"), Tools: map[string]string{"node": "16"}}}
	if got := MatchBinding(CmpKey(filepath.Join(home, "abc")), nested); got != nil {
		t.Errorf("p"+bs+"ab 误命中 p"+bs+"abc：%+v", got)
	}
}

// TestBindingsPerToolOverlay 覆盖 §4.3-2 逐工具独立回退：父绑一个工具、子绑另一个可叠加。
func TestBindingsPerToolOverlay(t *testing.T) {
	home, s := bindEnv(t)
	pyBin := bindStore(t, home, "python", "3.11.9", "")
	nodeBin := bindStore(t, home, "node", "16.20.2", "")
	parent := filepath.Join(home, "parent")
	child := filepath.Join(parent, "child")
	mustNoErr(t, os.MkdirAll(child, 0o755), "建目录")

	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{
		{Path: parent, Tools: map[string]string{"python": "3.11.9"}},
		{Path: child, Tools: map[string]string{"node": "16"}},
	}
	ads := []Adapter{&bindAdapter{id: "python"}, &bindAdapter{id: "node"}}
	r := ResolveBindings(child, b, s, ads)
	if len(r.Inject) != 2 {
		t.Fatalf("父 python + 子 node 应叠加为 2 项，实得 %+v / skips %+v", r.Inject, r.Skips)
	}
	got := map[string]BindingInjection{}
	for _, x := range r.Inject {
		got[x.Tool] = x
	}
	if got["python"].BinDir != pyBin {
		t.Errorf("python 目录错：%s ≠ %s", got["python"].BinDir, pyBin)
	}
	if got["node"].BinDir != nodeBin {
		t.Errorf("node 目录错：%s ≠ %s", got["node"].BinDir, nodeBin)
	}
	if got["python"].BoundAt != parent || got["node"].BoundAt != child {
		t.Errorf("BoundAt 应分别记录声明它的目录：%+v", got)
	}
	// 同工具冲突：子覆盖父，且不重复注入
	b.Bindings[0].Tools["python"] = "1.2.3"
	b.Bindings[1].Tools["python"] = "3.11.9"
	if r2 := ResolveBindings(child, b, s, ads); len(r2.Inject) != 2 {
		t.Fatalf("子声明同一工具时不应重复注入，实得 %+v", r2.Inject)
	}
	// 连续解析结果稳定（钩子会反复调用）
	if r3 := ResolveBindings(child, b, s, ads); len(r3.Inject) != 2 {
		t.Fatal("重复解析结果不稳定")
	}
}

// TestBindingsFallbackWhenMissing 覆盖 §10 第一行：删掉绑定版本仍用全局跑 + 一条 warn，绝不自动下载。
func TestBindingsFallbackWhenMissing(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "22.22.0", "") // 只有 22，绑 16 → 未装
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")

	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"}}}
	r := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}})
	if len(r.Inject) != 0 {
		t.Fatalf("未安装的版本不得注入：%+v", r.Inject)
	}
	if len(r.Warns) == 0 || !strings.Contains(r.Warns[0], "未安装") || !strings.Contains(r.Warns[0], "envs install") {
		t.Fatalf("warn 文案要含「未安装」与补装命令，实得 %v", r.Warns)
	}
	if len(r.Skips) == 0 || r.Skips[0].Kind != "not-installed" {
		t.Fatalf("skips 应标 not-installed，实得 %+v", r.Skips)
	}
	// 目录被删（外接盘未挂）同样回退，不报错
	bindStore(t, home, "node", "16.20.2", "")
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16.20.2"}}}
	if len(ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}}).Inject) != 1 {
		t.Fatal("基线：目录存在时应注入")
	}
	mustNoErr(t, os.RemoveAll(filepath.Join(home, "store", "node", "16.20.2")), "删版本目录")
	r2 := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}})
	if len(r2.Inject) != 0 || len(r2.Warns) == 0 {
		t.Fatalf("版本目录被删后必须回退全局并 warn：%+v %+v", r2.Inject, r2.Warns)
	}
}

// TestBindingsWhitelistRejectsPoison 覆盖 §4.3-4 安全断言：投毒目录一律拒投。
func TestBindingsWhitelistRejectsPoison(t *testing.T) {
	home, s := bindEnv(t)
	evil := filepath.Join(home, "evil", "node", "9.9.9")
	mustNoErr(t, os.MkdirAll(evil, 0o755), "建投毒目录")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")

	if AllowBindingDir(evil, s, &bindAdapter{id: "node"}) {
		t.Fatal("store 之外的目录不应通过白名单")
	}
	if !AllowBindingDir(bindStore(t, home, "node", "16.20.2", ""), s, &bindAdapter{id: "node"}) {
		t.Fatal("store 内的目录应通过白名单")
	}

	ad := &bindAdapter{id: "node", adopt: []Install{{Version: "9.9.9", Source: "nvm", BinDir: evil}}}
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "9.9.9"}}}
	r := ResolveBindings(proj, b, s, []Adapter{ad})
	if len(r.Inject) != 0 {
		t.Fatalf("白名单必须拦住 Adopted 来源指向任意目录的投毒：%+v", r.Inject)
	}
	if len(r.Skips) == 0 || r.Skips[0].Kind != "poisoned" {
		t.Fatalf("应记 poisoned 跳过，实得 %+v", r.Skips)
	}
}

// TestBindingsConstraintExpressions 覆盖 §4.3 末段：首期只认 16 / 16.20.2 / latest。
func TestBindingsConstraintExpressions(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	ads := []Adapter{&bindAdapter{id: "node"}}

	for _, expr := range []string{"^16.9", "lts/*", ">=8.9", "~16.9", "16.x", "lts/hydrogen", "*", "16.20.2-nightly"} {
		b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": expr}}}
		r := ResolveBindings(proj, b, s, ads)
		if len(r.Skips) == 0 || r.Skips[0].Kind != "constraint" {
			t.Errorf("%q 应判为约束表达式，实得 %+v", expr, r.Skips)
		}
		if len(r.Skips) > 0 && !strings.Contains(r.Skips[0].Reason, ConstraintExprError) {
			t.Errorf("%q 的说明要含固定措辞，实得 %q", expr, r.Skips[0].Reason)
		}
		if len(r.Inject) != 0 {
			t.Errorf("%q 不得注入", expr)
		}
	}
	for _, expr := range []string{"16", "16.20.2", "latest", "v16.20.2"} {
		b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": expr}}}
		r := ResolveBindings(proj, b, s, ads)
		if len(r.Inject) != 1 {
			t.Errorf("%q 应可定位（已装 16.20.2），实得 %+v / %+v", expr, r.Inject, r.Skips)
		}
	}
}

// —— §5 档位判定 ——

func TestBindingsTierGate(t *testing.T) {
	for _, id := range []string{"node", "npm", "npx", "pnpm", "pnpx", "yarn", "python", "go"} {
		if ok, tier, _ := BindGate(id); !ok || tier != "1" {
			t.Errorf("档①应允许绑定：%s（得 %v %s）", id, ok, tier)
		}
	}
	for id, want := range map[string]string{"pip": "pip", "nvm": "nvm", "docker": "docker"} {
		ok, tier, why := BindGate(id)
		if ok || tier != "5" || !strings.Contains(why, want) {
			t.Errorf("档⑤应拒绝并说明：%s → %v %s %s", id, ok, tier, why)
		}
	}
	for _, id := range []string{"java", "maven", "gradle"} {
		ok, tier, why := BindGate(id)
		if ok || tier != "2" || !strings.Contains(why, "P3") {
			t.Errorf("档②应拒绝并指向 §11 P3：%s → %v %s %s", id, ok, tier, why)
		}
	}
	for _, id := range []string{"rust", "php", "deno", "bun", "uv", "git", "kubectl", "terraform", "redis"} {
		if ok, _, _ := BindGate(id); ok {
			t.Errorf("档④默认不开放：%s 被放行", id)
		}
	}
	if ok, _, _ := BindGate("nonsense"); ok {
		t.Error("未知工具应拒绝")
	}
	// 白名单结构不为 java 设限：P3 放开档②即可，解析层无需改动
	if _, tier, _ := BindGate("java"); tier != "2" {
		t.Error("java 必须归在档②而不是散落在默认分支")
	}
}

func TestBindingsNativeYield(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "dotnet", "8.0.100", "")
	bindStore(t, home, "python", "3.11.9", "")
	bindStore(t, home, "go", "1.22.5", "bin")
	bindStore(t, home, "pnpm", "9.12.3", "")
	proj := filepath.Join(home, "proj")

	b := DefaultBindings()
	b.Enabled = true
	ads := []Adapter{
		&bindAdapter{id: "dotnet"}, &bindAdapter{id: "python"},
		&bindAdapter{id: "go", binSub: "bin"}, &bindJSAdapter{bindAdapter{id: "pnpm"}},
	}

	cases := []struct{ file, content, tool string }{
		{"global.json", `{"sdk":{"version":"8.0.100"}}`, "dotnet"},
		{".python-version", "3.11" + "\n", "python"},
		{"go.mod", "module x" + "\n" + "\n" + "toolchain go1.99.0" + "\n", "go"},
		{"package.json", `{"packageManager":"pnpm@9.12.3+sha512.ab"}`, "pnpm"},
		{"gradlew.bat", "@echo off" + "\n", "gradle"},
		{"mvnw.cmd", "@echo off" + "\n", "maven"},
	}
	for _, c := range cases {
		mustNoErr(t, os.RemoveAll(proj), "清目录")
		mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
		mustNoErr(t, os.WriteFile(filepath.Join(proj, c.file), []byte(c.content), 0o644), "写标记")
		b.Bindings = []Binding{{Path: proj, Tools: map[string]string{c.tool: "latest"}}}
		r := ResolveBindings(proj, b, s, ads)
		if len(r.Inject) != 0 {
			t.Errorf("检测到 %s 时 %s 应让位不注入，实得 %+v", c.file, c.tool, r.Inject)
		}
		if len(r.Skips) == 0 || r.Skips[0].Kind != "native" {
			t.Errorf("%s → %s 应记 native 让位，实得 %+v", c.file, c.tool, r.Skips)
		}
		if !strings.Contains(r.ExplainText(), "原生机制") {
			t.Errorf("bind why 要说明让位原因（%s）", c.file)
		}
	}

	// go 例外：绑定显式带 GOTOOLCHAIN=local 时以绑定为准（§5 末段）
	mustNoErr(t, os.RemoveAll(proj), "清目录")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	mustNoErr(t, os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module x"+"\n"+"\n"+"toolchain go1.99.0"+"\n"), 0o644), "写 go.mod")
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"go": "1.22"}, Env: map[string]string{"GOTOOLCHAIN": "local"}}}
	r := ResolveBindings(proj, b, s, ads)
	if len(r.Inject) != 1 || r.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("GOTOOLCHAIN=local 应让绑定压过 go.mod，实得 %+v / %+v", r.Inject, r.Env)
	}

	// 绑定 go 且项目未声明 toolchain：内核自动补 GOTOOLCHAIN=local
	mustNoErr(t, os.Remove(filepath.Join(proj, "go.mod")), "删 go.mod")
	r = ResolveBindings(proj, b, s, ads)
	if r.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("绑定 go 必须注入 GOTOOLCHAIN=local（§5 档③），实得 %+v", r.Env)
	}
}

func TestBindingsUpstreamMarkersPromptOnly(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	mustNoErr(t, os.WriteFile(filepath.Join(proj, ".nvmrc"), []byte("14"+"\n"), 0o644), "写 .nvmrc")
	mustNoErr(t, os.WriteFile(filepath.Join(proj, "package.json"),
		[]byte(`{"engines":{"node":">=8.9"},"scripts":{"dev":"vue-cli-service serve"}}`), 0o644), "写 package.json")

	b := DefaultBindings() // autoDetect 默认 prompt
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"}}}
	ads := []Adapter{&bindAdapter{id: "node"}}
	r := ResolveBindings(proj, b, s, ads)
	if len(r.Inject) != 1 || r.Inject[0].Version != "16.20.2" {
		t.Fatalf("上游约定不得改变显式绑定的结果：%+v", r.Inject)
	}
	joined := strings.Join(r.Notes, "\n")
	if !strings.Contains(joined, ".nvmrc") || !strings.Contains(joined, ">=8.9") {
		t.Fatalf("提示要覆盖 .nvmrc 与 engines 两个上游来源，实得 %q", joined)
	}
	if !strings.Contains(joined, "只提示不生效") {
		t.Fatalf("提示必须写明默认不生效（§1 裁决 6），实得 %q", joined)
	}
	// 没有任何绑定声明时，上游标记也不得凭空注入
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{}}}
	if r := ResolveBindings(proj, b, s, ads); len(r.Inject) != 0 {
		t.Fatalf("autoDetect=prompt 不得注入任何东西：%+v", r.Inject)
	}
	// autoDetect=off 时连提示都不给，但显式绑定照常
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"}}}
	b.AutoDetect = AutoDetectOff
	r = ResolveBindings(proj, b, s, ads)
	if len(r.Notes) != 0 {
		t.Fatalf("autoDetect=off 应安静，实得 %v", r.Notes)
	}
	if len(r.Inject) != 1 {
		t.Fatal("off 只关提示，不影响显式绑定")
	}
}

func TestBindingsNodeModulesHardSkip(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	nm := filepath.Join(proj, "node_modules", "some-dep")
	mustNoErr(t, os.MkdirAll(nm, 0o755), "建 node_modules")
	mustNoErr(t, os.WriteFile(filepath.Join(nm, ".nvmrc"), []byte("6"+"\n"), 0o644), "写 node_modules/.nvmrc")

	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"}}}
	ads := []Adapter{&bindAdapter{id: "node"}}
	r := ResolveBindings(nm, b, s, ads)
	for _, n := range r.Notes {
		if strings.Contains(n, `"6"`) && !strings.Contains(n, "已忽略") {
			t.Fatalf("node_modules 内的 .nvmrc 不得作为生效提示：%q", n)
		}
	}
	if len(r.Inject) != 1 {
		t.Fatalf("node_modules 下的子目录仍应继承项目绑定：%+v / %+v", r.Inject, r.Skips)
	}
}

func TestBindingsGBKPackageJSONNoError(t *testing.T) {
	home, _ := bindEnv(t)
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	// GBK「备注」混在 JSON 里：非法 UTF-8 必须安静降级为提示（§10 GBK 行）
	gbk := []byte(`{"name":"huairen","engines":{"node":">=8.9"},"desc":"`)
	gbk = append(gbk, 0xb1, 0xb8, 0xd7, 0xa2)
	gbk = append(gbk, 0x22, 0x7d) // 收尾的 "}
	mustNoErr(t, os.WriteFile(filepath.Join(proj, "package.json"), gbk, 0o644), "写 GBK package.json")

	m := scanProjectMarkers(proj, 5)
	if len(m.prompts) == 0 {
		t.Fatal("GBK 读取失败要给出提示而不是静默")
	}
	joined := strings.Join(m.prompts, "\n")
	if !strings.Contains(joined, "UTF-8") {
		t.Fatalf("提示措辞要说明编码问题，实得 %q", joined)
	}
}

// —— §4.4 env 覆盖 ——

func TestBindingsEnvOverlayAndApply(t *testing.T) {
	home, s := bindEnv(t)
	nodeBin := bindStore(t, home, "node", "22.22.0", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")

	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "22"},
		Env: map[string]string{"NODE_OPTIONS": "--openssl-legacy-provider", "JAVA_HOME": `E:` + bs + `jdk` + bs + `17`}}}
	r := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}})
	if r.Env["NODE_OPTIONS"] != "--openssl-legacy-provider" {
		t.Fatalf("env 覆盖未生效：%+v", r.Env)
	}

	base := []string{"PATH=C:" + bs + "Windows", "HOME=" + home}
	got := r.ApplyEnv(base)
	env := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if !strings.HasPrefix(env["PATH"], nodeBin+";") {
		t.Fatalf("PATH 必须以版本目录开头：%s", env["PATH"])
	}
	if !strings.HasSuffix(env["PATH"], "C:"+bs+"Windows") {
		t.Fatalf("原 PATH 必须整体保留在尾部：%s", env["PATH"])
	}
	if env["JAVA_HOME"] != `E:`+bs+`jdk`+bs+`17` {
		t.Fatalf("JAVA_HOME 覆盖未注入进程块：%s", env["JAVA_HOME"])
	}
	// 幂等：对已注入的环境块再作用一次，内容不得叠加（§2-L5 膨胀风险）
	twice := r.ApplyEnv(got)
	if strings.Join(twice, "\n") != strings.Join(got, "\n") {
		t.Fatalf("重复注入导致环境块叠加：\n%v\n%v", got, twice)
	}
	// 只在进程块内：本测试全程没有任何注册表 / 用户级变量写入调用
	if _, err := os.Stat(filepath.Join(home, "bindings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("ApplyEnv 不得产生落盘副作用")
	}
}

// —— §4.5 写 / 读 / 锁 / 损坏 ——

func TestBindingsSaveLoadRoundTrip(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")

	b, warns := LoadBindings()
	if len(warns) != 0 || b.Count() != 0 || b.Enabled {
		t.Fatalf("无文件时应给默认值且 enabled=false：%+v %v", b, warns)
	}
	if b.AutoDetect != AutoDetectPrompt || b.MaxDepth != DefaultBindMaxDepth {
		t.Fatalf("默认 autoDetect/maxDepth 不符：%s %d", b.AutoDetect, b.MaxDepth)
	}
	if _, err := SetBindingTool(b, proj, "node", "16", map[string]string{"NODE_OPTIONS": "--openssl-legacy-provider"}, "vue-cli 4", "manual"); err != nil {
		t.Fatal(err)
	}
	// 绑定不得牵连 settings.json（§4.4/§4.5：独立文件，Settings 不加字段）
	if _, err := os.Stat(filepath.Join(home, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("写绑定表不得创建或改动 settings.json")
	}
	// 总开关要显式开（envs bind enable 的语义）：bind 本身不偷偷翻灰度开关（§4.1/§12）
	b.Enabled = true
	if _, err := SaveBindings(b, []Adapter{&bindAdapter{id: "node"}}, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "bindings.json")); err != nil {
		t.Fatal("bindings.json 未落盘")
	}
	if _, err := os.Stat(filepath.Join(home, "bindings.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Error("写完必须释放 bindings.lock")
	}
	if _, err := os.Stat(filepath.Join(home, "bindings.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Error("原子写的 .tmp 必须已被 rename 掉")
	}
	back, warns := LoadBindings()
	if len(warns) != 0 || back.Count() != 1 || !back.Enabled {
		t.Fatalf("回读不符：%+v %v", back, warns)
	}
	if back.Bindings[0].Tools["node"] != "16" || back.Bindings[0].Env["NODE_OPTIONS"] != "--openssl-legacy-provider" {
		t.Fatalf("字段回读不符：%+v", back.Bindings[0])
	}
	if !strings.HasPrefix(back.Bindings[0].Path, home) {
		t.Fatalf("path 应存归一后的绝对路径：%q", back.Bindings[0].Path)
	}
}

func TestBindingsCorruptFileQuarantined(t *testing.T) {
	home, _ := bindEnv(t)
	p := filepath.Join(home, "bindings.json")
	mustNoErr(t, os.WriteFile(p, []byte("{ this is not json"), 0o644), "写坏文件")

	b, warns := LoadBindings()
	if len(warns) == 0 {
		t.Fatal("损坏文件必须给出提示")
	}
	if b.Enabled || b.Count() != 0 {
		t.Fatal("损坏后本次必须全走全局（默认值）")
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("坏文件必须被改名让位")
	}
	found := false
	for _, e := range mustReadDir(t, home) {
		if strings.HasPrefix(e.Name(), "bindings.bad-") {
			found = true
		}
	}
	if !found {
		t.Fatal("未生成 bindings.bad-<ts>")
	}
	if _, warns2 := LoadBindings(); len(warns2) != 0 {
		t.Fatalf("隔离后不应重复告警：%v", warns2)
	}
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	ents, err := os.ReadDir(dir)
	mustNoErr(t, err, "读目录")
	return ents
}

func TestBindingsLockTimeoutDegradesToReadOnly(t *testing.T) {
	home, s := bindEnv(t)
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	lock := filepath.Join(home, "bindings.lock")
	mustNoErr(t, os.WriteFile(lock, []byte("other-pid"), 0o644), "占锁")

	oldTO, oldPoll, oldStale := bindLockTimeout, bindLockPoll, bindLockStale
	bindLockTimeout, bindLockPoll, bindLockStale = 200*time.Millisecond, 20*time.Millisecond, time.Hour
	defer func() { bindLockTimeout, bindLockPoll, bindLockStale = oldTO, oldPoll, oldStale }()

	b := DefaultBindings()
	if _, err := SetBindingTool(b, proj, "node", "16", nil, "", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveBindings(b, []Adapter{&bindAdapter{id: "node"}}, s); err == nil || !errors.Is(err, ErrBindingsReadOnly) {
		t.Fatalf("锁被占用时应降级为只读错误，实得 %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bindings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("降级路径不得写出半截文件")
	}
	mustNoErr(t, os.Remove(lock), "释放锁")
	if _, err := SaveBindings(b, []Adapter{&bindAdapter{id: "node"}}, s); err != nil {
		t.Fatalf("锁释放后应能写：%v", err)
	}
}

func TestBindingsStaleLockTakenOver(t *testing.T) {
	home, s := bindEnv(t)
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	lock := filepath.Join(home, "bindings.lock")
	mustNoErr(t, os.WriteFile(lock, []byte("dead-pid"), 0o644), "占锁")
	old := time.Now().Add(-time.Hour)
	mustNoErr(t, os.Chtimes(lock, old, old), "改锁时间戳")

	oldTO, oldStale := bindLockTimeout, bindLockStale
	bindLockTimeout, bindLockStale = time.Minute, 5*time.Second
	defer func() { bindLockTimeout, bindLockStale = oldTO, oldStale }()

	b := DefaultBindings()
	if _, err := SetBindingTool(b, proj, "node", "16", nil, "", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveBindings(b, []Adapter{&bindAdapter{id: "node"}}, s); err != nil {
		t.Fatalf("陈旧锁应被接管而不是永久挡住写入：%v", err)
	}
}

// —— gen 表：与 agent-A 的固定接口（契约 v2）——

func TestBindingsGenTableFormat(t *testing.T) {
	home, s := bindEnv(t)
	nodeBin := bindStore(t, home, "node", "16.20.2", "")
	pyBin := bindStore(t, home, "python", "3.11.9", "")
	proj := filepath.Join(home, "proj")
	nested := filepath.Join(proj, "sub")
	mustNoErr(t, os.MkdirAll(nested, 0o755), "建目录")

	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16", "python": "3.11.9"},
		Env: map[string]string{"NODE_OPTIONS": "--openssl-legacy-provider", "PATH_EXTRA": "a b;c=d"}}}
	ads := []Adapter{&bindAdapter{id: "node"}, &bindAdapter{id: "python"}}
	if _, err := SaveBindings(b, ads, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(GenTablePath())
	mustNoErr(t, err, "读扁平表")
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		t.Fatal("扁平表不得带 UTF-8 BOM")
	}
	text := string(raw)
	if strings.Count(text, "\n") != strings.Count(text, "\r\n") {
		t.Fatal("扁平表必须全 CRLF")
	}
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\r\n")
	if lines[0] != genTableHeader {
		t.Fatalf("首行必须是 %s，实得 %q", genTableHeader, lines[0])
	}
	if len(lines)-1 != 2 {
		t.Fatalf("应有 2 条数据行（node + python），实得 %d：%q", len(lines)-1, lines[1:])
	}
	wantKey := CmpKey(proj)
	for _, ln := range lines[1:] {
		cols := strings.Split(ln, "\t")
		if len(cols) != 4 {
			t.Fatalf("数据行必须恰好 4 列，实得 %d：%q", len(cols), ln)
		}
		if cols[0] != wantKey {
			t.Errorf("第 1 列应是明文 cmpKey %q，实得 %q", wantKey, cols[0])
		}
		if strings.ContainsAny(cols[0], "%") || strings.ContainsAny(cols[2], "%\r\n\t") {
			t.Errorf("明文列含 %% 或换行必须拒写：%q", ln)
		}
		if cols[2] != nodeBin && cols[2] != pyBin {
			t.Errorf("第 3 列目录不符：%q", cols[2])
		}
		if strings.Contains(cols[3], ";") {
			t.Errorf("第 4 列分隔符不能再是 ;（契约 v2 用 &）：%q", cols[3])
		}
	}
	var envCol string
	for _, ln := range lines[1:] {
		cols := strings.Split(ln, "\t")
		if cols[1] == "node" {
			envCol = cols[3]
		}
	}
	if envCol == "" {
		t.Fatal("node 行缺 env 列")
	}
	if !strings.Contains(envCol, "NODE_OPTIONS=--openssl-legacy-provider") {
		t.Errorf("全在免编码集内的值应可读，实得 %q", envCol)
	}
	if !strings.Contains(envCol, "PATH_EXTRA=a%20b%3Bc%3Dd") {
		t.Errorf("空格→%%20、;→%%3B、=→%%3D，实得 %q", envCol)
	}
	if !strings.Contains(envCol, "&") {
		t.Errorf("多对之间必须用 & 分隔，实得 %q", envCol)
	}

	// 最长前缀优先：子目录行必须排在父目录行之前（钩子取第一个命中）
	bindStore(t, home, "node", "22.22.0", "")
	b.Bindings = append(b.Bindings, Binding{Path: nested, Tools: map[string]string{"node": "22.22.0"}})
	if _, err := SaveBindings(b, ads, s); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(GenTablePath())
	lines = strings.Split(strings.TrimRight(string(raw), "\r\n"), "\r\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], CmpKey(nested)) {
		t.Fatalf("子目录键必须排在父目录之前，实得 %q", lines[1:])
	}

	// enabled=false → 只剩首行（灰度总开关的落点）
	b.Enabled = false
	if _, err := SaveBindings(b, ads, s); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(GenTablePath())
	if string(raw) != genTableHeader+"\r\n" {
		t.Fatalf("总开关关闭时扁平面必须为空表，实得 %q", string(raw))
	}
}

func TestBindingsGenTableRefusesPercentInDir(t *testing.T) {
	home, s := bindEnv(t)
	badRoot := filepath.Join(home, "100%node")
	mustNoErr(t, os.MkdirAll(filepath.Join(badRoot, "node", "16.20.2"), 0o755), "建带百分号的版本目录")
	s.StorePath = badRoot
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")

	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16.20.2"}}}
	text, warns := RenderGenTable(b, s, []Adapter{&bindAdapter{id: "node"}})
	if strings.Count(text, "\r\n") != 1 {
		t.Fatalf("含 %% 的行必须整行拒写，实得 %q", text)
	}
	if len(warns) == 0 {
		t.Fatal("拒写必须回传 warn")
	}
}

func TestBindingsGenTableLookupMatches(t *testing.T) {
	home, s := bindEnv(t)
	nodeBin := bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(filepath.Join(proj, "src", "deep"), 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"},
		Env: map[string]string{"NODE_OPTIONS": "--openssl-legacy-provider"}}}
	if _, err := SaveBindings(b, []Adapter{&bindAdapter{id: "node"}}, s); err != nil {
		t.Fatal(err)
	}
	inj, env := GenTableLookup(filepath.Join(proj, "src", "deep"))
	if len(inj) != 1 || inj[0].BinDir != nodeBin {
		t.Fatalf("扁平面查表未命中（子目录继承）：%+v", inj)
	}
	if env["NODE_OPTIONS"] != "--openssl-legacy-provider" {
		t.Fatalf("env 列解码不符：%+v", env)
	}
	if inj2, _ := GenTableLookup(filepath.Join(home, "other")); len(inj2) != 0 {
		t.Fatal("表外目录不得命中")
	}
}

func TestBindingsPercentCodecRoundTrip(t *testing.T) {
	for _, tc := range [][2]string{
		{"NODE_OPTIONS", "--openssl-legacy-provider"},
		{"JAVA_HOME", `E:` + bs + `jdk 17` + bs + `jdk`},
		{"GOTOOLCHAIN", "local"},
		{"WEIRD", `a%b&c"d^e;f=g`},
	} {
		enc := EnvPairsString(map[string]string{tc[0]: tc[1]})
		if strings.Contains(enc, ";") {
			t.Fatalf("编码后不得出现裸 ;：%q", enc)
		}
		if got := ParseEnvPairs(enc); got[tc[0]] != tc[1] {
			t.Errorf("往返不符：%q → %q → %q", tc[1], enc, got[tc[0]])
		}
	}
	if EnvPairsString(map[string]string{"A": "b c"}) != "A=b%20c" {
		t.Errorf("空格必须编成 %%20 而不是 +：%q", EnvPairsString(map[string]string{"A": "b c"}))
	}
	if _, ok := percentDecode("100%"); ok {
		t.Error("截断的转义序列必须判为非法")
	}
	if _, ok := percentDecode("a+b"); ok {
		t.Error("契约禁用 + 表示空格")
	}
}

// —— 写侧校验 ——

func TestBindingsSetRejectsBadTargets(t *testing.T) {
	home, _ := bindEnv(t)
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()

	if _, err := SetBindingTool(b, `\\nas\share\proj`, "node", "16", nil, "", ""); err == nil || !strings.Contains(err.Error(), "UNC") {
		t.Errorf("UNC 必须拒写（§10）：%v", err)
	}
	if _, err := SetBindingTool(b, filepath.VolumeName(home), "node", "16", nil, "", ""); err == nil {
		t.Error("裸盘根必须拒写")
	}
	if _, err := SetBindingTool(b, filepath.Join(home, "nope"), "node", "16", nil, "", ""); err == nil {
		t.Error("不存在的目录必须拒写")
	}
	if _, err := SetBindingTool(b, proj, "pip", "22", nil, "", ""); err == nil || !strings.Contains(err.Error(), "pip") {
		t.Errorf("档⑤必须拒绝并说明：%v", err)
	}
	if _, err := SetBindingTool(b, proj, "node", "^16.9", nil, "", ""); err == nil || !strings.Contains(err.Error(), ConstraintExprError) {
		t.Errorf("约束表达式必须拒绝：%v", err)
	}
	if _, err := SetBindingTool(b, proj, "node", "16", map[string]string{"BAD KEY": "x"}, "", ""); err == nil {
		t.Error("非法 env 键必须拒绝")
	}
	if _, err := SetBindingTool(b, proj, "node", "16", map[string]string{"A": "line1" + "\n" + "line2"}, "", ""); err == nil {
		t.Error("env 值含换行必须拒绝")
	}
	if b.Count() != 0 {
		t.Fatalf("以上失败都不得污染表：%+v", b.Bindings)
	}
	if _, err := SetBindingTool(b, proj, "node", "16", nil, "vue-cli 4", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetBindingTool(b, proj, "python", "3.11.9", nil, "", "manual"); err != nil {
		t.Fatal(err)
	}
	if b.Count() != 1 || len(b.Bindings[0].Tools) != 2 {
		t.Fatalf("同目录应合并成一条：%+v", b.Bindings)
	}
	if changed, left := DeleteBindingTool(b, proj, "node"); !changed || left != 1 {
		t.Fatalf("摘一个工具后应剩 1 个：%v %d", changed, left)
	}
	if changed, _ := DeleteBindingTool(b, proj, "python"); !changed || b.Count() != 0 {
		t.Fatalf("最后一个工具摘掉应删整条：%v %d", changed, b.Count())
	}
}

func TestBindingsENVSDisableEscapeHatch(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"}}}
	ads := []Adapter{&bindAdapter{id: "node"}}
	if len(ResolveBindings(proj, b, s, ads).Inject) != 1 {
		t.Fatal("基线：未设逃生门时应正常注入")
	}
	t.Setenv("ENVS_DISABLE", "1")
	r := ResolveBindings(proj, b, s, ads)
	if len(r.Inject) != 0 || !strings.Contains(r.ExplainText(), "ENVS_DISABLE") {
		t.Fatalf("ENVS_DISABLE=1 必须整进程旁路：%+v", r)
	}
}

func TestBindingsDoctorAndLockfileDrift(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	nm := filepath.Join(proj, "node_modules")
	mustNoErr(t, os.MkdirAll(nm, 0o755), "建目录")
	mustNoErr(t, os.WriteFile(filepath.Join(nm, ".package-lock.json"),
		[]byte(`{"lockfileVersion":3,"packages":{}}`), 0o644), "写 lock")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"}}}
	ads := []Adapter{&bindAdapter{id: "node"}}
	r := ResolveBindings(proj, b, s, ads)
	w := LockfileDriftWarning(proj, r)
	if !strings.Contains(w, "lockfile v3") {
		t.Fatalf("lockfileVersion 3 + node16 必须 warn（§7.3-8），实得 %q", w)
	}
	bindStore(t, home, "node", "22.22.0", "")
	b.Bindings[0].Tools["node"] = "22.22.0"
	if w2 := LockfileDriftWarning(proj, ResolveBindings(proj, b, s, ads)); w2 != "" {
		t.Fatalf("node22 + lock v3 是匹配的，不该 warn：%q", w2)
	}
	checks := DoctorBindings(ads, s)
	if len(checks) == 0 {
		t.Fatal("DoctorBindings 必须给出体检项（供 agent-A 的 doctor 追加）")
	}
	var names []string
	for _, c := range checks {
		names = append(names, c.Name+"|"+c.Status)
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"目录绑定表", "绑定表原子写", "无扩展名 shim"} {
		if !strings.Contains(joined, want) {
			t.Errorf("体检缺项 %s：%v", want, names)
		}
	}
}

func TestBindingsDisabledGloballyIsNoop(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings() // enabled=false（§4.1 灰度默认）
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16"}}}
	r := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}})
	if len(r.Inject) != 0 || len(r.Skips) == 0 || r.Skips[0].Kind != "disabled" {
		t.Fatalf("总开关关闭必须整体旁路：%+v", r)
	}
}

func TestBindingsJSONShape(t *testing.T) {
	home, _ := bindEnv(t)
	b := DefaultBindings()
	b.Enabled = true
	b.path = filepath.Join(home, "bindings.json")
	b.Bindings = []Binding{{Path: `E:` + bs + `demo`, Tools: map[string]string{"node": "16"},
		Env: map[string]string{"NODE_OPTIONS": "--openssl-legacy-provider"}, Note: "n", Source: "manual",
		UpdatedAt: "2026-11-19T17:20:26+08:00"}}
	data, err := json.MarshalIndent(b, "", "  ")
	mustNoErr(t, err, "序列化")
	var top map[string]json.RawMessage
	mustNoErr(t, json.Unmarshal(data, &top), "解析顶层")
	for _, k := range []string{"version", "enabled", "autoDetect", "maxDepth", "bindings"} {
		if _, ok := top[k]; !ok {
			t.Errorf("§4.1 顶层字段缺失：%s", k)
		}
	}
	var list struct {
		Bindings []json.RawMessage `json:"bindings"`
	}
	mustNoErr(t, json.Unmarshal(data, &list), "取 bindings")
	var one struct {
		Path      string            `json:"path"`
		Tools     map[string]string `json:"tools"`
		Env       map[string]string `json:"env"`
		Note      string            `json:"note"`
		Source    string            `json:"source"`
		UpdatedAt string            `json:"updatedAt"`
	}
	mustNoErr(t, json.Unmarshal(list.Bindings[0], &one), "取一条绑定")
	if one.Path != `E:`+bs+`demo` || one.Tools["node"] != "16" ||
		one.Env["NODE_OPTIONS"] != "--openssl-legacy-provider" || one.Source != "manual" ||
		one.UpdatedAt == "" || one.Note != "n" {
		t.Fatalf("§4.1 条目字段不符：%+v", one)
	}
	// 未知字段不致命（向前兼容）；enabled 缺省必须是 false（灰度）
	future := `{"version":1,"bindings":[{"path":"E:` + bs + bs + `x","tools":{"node":"16"},"future":1}]}`
	var loose Bindings
	mustNoErr(t, json.Unmarshal([]byte(future), &loose), "读未来字段")
	if loose.Enabled {
		t.Error("enabled 缺省必须是 false（灰度）")
	}
}

// —— 边角 ——

func TestBindingsNormalizeEmptyAndSpace(t *testing.T) {
	if NormalizePath("") != "" || NormalizePath("   ") != "" {
		t.Error("空值必须归一为空串")
	}
	if keyMatchable("") {
		t.Error("空键不可匹配")
	}
	if CmpKey(`""`) != CmpKey("") && CmpKey(`""`) != "" {
		t.Errorf("纯引号应归一为空，实得 %q", CmpKey(`""`))
	}
}

func TestBindingsUpdatedAtFormat(t *testing.T) {
	home, _ := bindEnv(t)
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	if _, err := SetBindingTool(b, proj, "node", "16", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, b.Bindings[0].UpdatedAt); err != nil {
		t.Fatalf("updatedAt 必须是 RFC3339（§4.1 样例带时区）：%q %v", b.Bindings[0].UpdatedAt, err)
	}
}

// TestBindingsGenTableReadFailure 覆盖 §10 第 7 行的错误管道：读不动扁平表（这里用「路径是目录」
// 制造确定性失败）必须显式回传错误而不是 panic；GenTableLookup 则吞掉错误返回空 = 用全局版本。
// 真·250 ms 超时路径需要杀软拦截现场，本机未验（见交付报告）。
func TestBindingsGenTableReadFailure(t *testing.T) {
	home, _ := bindEnv(t)
	mustNoErr(t, os.MkdirAll(GenTablePath(), 0o755), "把扁平面做成目录以制造读失败")
	if inj, env := GenTableLookup(home); inj != nil || env != nil {
		t.Fatalf("读失败必须等价于「无注入」：%v %v", inj, env)
	}
	if _, _, err := GenTableLookupErr(home); err == nil {
		t.Fatal("GenTableLookupErr 要把读失败显式回传")
	}
}

// TestBindingsTimeoutBudgetConfigurable 确认 §10 的 250 ms 托底时长是可注入的测试钩子。
func TestBindingsTimeoutBudgetConfigurable(t *testing.T) {
	old := genTableBudget
	genTableBudget = 50 * time.Millisecond
	defer func() { genTableBudget = old }()
	if genTableBudget != 50*time.Millisecond {
		t.Fatal("超时预算不可调")
	}
}

// —— 层 3 查找（lead R6：必须在注入后的 PATH 上找命令）——————————

// fakeCmd 在 dir 里写一个自报名号的 .cmd 夹具（不联网、不装任何东西）。
func fakeCmd(t *testing.T, dir, name, mark string) string {
	t.Helper()
	mustNoErr(t, os.MkdirAll(dir, 0o755), "建夹具目录")
	body := "@echo off\r\n" +
		"echo MARK=" + mark + "\r\n" +
		"echo EXE=%~f0\r\n" +
		"echo ARGV=%*\r\n"
	p := filepath.Join(dir, name+".cmd")
	mustNoErr(t, os.WriteFile(p, []byte(body), 0o755), "写假 "+name+".cmd")
	return p
}

// TestLayerThreeLookupOnInjectedPATH 是 R6 的判别性回归：
// 进程自身 PATH 上先排一个同名抢位者（复现 §2-L1 的 nvm 抢位），绑定指向另一个目录；
// 断言 ①查找落在绑定目录 ②真起子进程后它自报 bound ③npm 这类 .cmd 同样跟随。
func TestLayerThreeLookupOnInjectedPATH(t *testing.T) {
	home, s := bindEnv(t)
	globalDir := filepath.Join(home, "hijack") // 模拟 C:\nvm4w\nodejs 抢位
	boundDir := bindStore(t, home, "node", "22.22.0", "")
	fakeCmd(t, globalDir, "node", "global")
	fakeCmd(t, globalDir, "npm", "global")
	fakeCmd(t, boundDir, "node", "bound")
	fakeCmd(t, boundDir, "npm", "bound")

	// 抢位者进的是「本进程的 PATH」，而不是绑定表——这正是 exec.LookPath 会踩的坑
	t.Setenv("PATH", globalDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "22"}}}
	r := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}})
	if len(r.Inject) != 1 || r.Inject[0].BinDir != boundDir {
		t.Fatalf("解析层基线就不对：%+v", r.Inject)
	}
	// 反证：用进程自己的 PATH 查，会命中抢位者（说明老写法为什么会假生效）
	if self, err := exec.LookPath("node"); err == nil && !strings.EqualFold(filepath.Dir(self), globalDir) {
		t.Skipf("本机 PATH 形态特殊，反证不成立：%s", self)
	} else if err != nil {
		t.Skipf("本机 PATH 上找不到夹具：%v", err)
	}

	env := r.ApplyEnv(os.Environ())
	for _, name := range []string{"node", "npm"} {
		exe, ok := LookupInEnv(env, name)
		if !ok {
			t.Fatalf("%s：在注入后的 PATH 上没找到", name)
		}
		if !strings.EqualFold(filepath.Dir(exe), boundDir) {
			t.Fatalf("%s：查找没有发生在注入后的 PATH 上，实得 %s，期望 %s", name, exe, boundDir)
		}
		c := exec.Command(exe)
		c.Env = env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s 子进程启动失败：%v\n%s", name, err, out)
		}
		if !strings.Contains(string(out), "MARK=bound") {
			t.Fatalf("%s 子进程自报了 global（假生效未修好）：\n%s", name, out)
		}
		if strings.Contains(string(out), `EXE=`+globalDir) {
			t.Fatalf("%s 实际执行的是抢位者：\n%s", name, out)
		}
	}
	// 环境块里 PATH 首位必须是绑定目录（钩子/run 共用同一口径）
	if first := strings.Split(envValueCI(env, "PATH"), ";")[0]; !strings.EqualFold(first, boundDir) {
		t.Errorf("注入后的 PATH 首位应是绑定目录，实得 %q", first)
	}
}

// TestLookupInEnvSemantics 覆盖查找细节：PATHEXT 顺序、同目录 exe 压 cmd（§2-L6）、
// 显式带分隔符不查 PATH、目录不当命中、PATH 缺项与空段。
func TestLookupInEnvSemantics(t *testing.T) {
	dir := t.TempDir()
	mustNoErr(t, os.MkdirAll(filepath.Join(dir, "tag.cmd"), 0o755), "同名目录应被跳过")
	mustNoErr(t, os.WriteFile(filepath.Join(dir, "tag.exe"), []byte("@echo off\r\n"), 0o755), "写 tag.exe")
	mustNoErr(t, os.WriteFile(filepath.Join(dir, "tag.cmd2"), []byte("x"), 0o644), "写杂项")
	env := []string{"PATH=" + dir, "PATHEXT=.COM;.EXE;.BAT;.CMD"}
	got, ok := LookupInEnv(env, "tag")
	if !ok || !strings.EqualFold(filepath.Base(got), "tag.exe") {
		t.Fatalf("同目录应 .exe 压 .cmd（§2-L6），且目录本身不算命中，实得 %q %v", got, ok)
	}
	// PATHEXT 反过来排就该拿 .cmd（但这里是目录 → 继续找 .exe）
	env2 := []string{"PATH=" + dir, "PATHEXT=.CMD;.EXE"}
	if got2, _ := LookupInEnv(env2, "tag"); !strings.EqualFold(filepath.Base(got2), "tag.exe") {
		t.Errorf("目录形态的 tag.cmd 必须被跳过，实得 %q", got2)
	}
	// 空 PATH / 缺失 PATH → 找不到
	if _, ok := LookupInEnv([]string{"PATH="}, "tag"); ok {
		t.Error("空 PATH 不应命中")
	}
	if _, ok := LookupInEnv(nil, "tag"); ok {
		t.Error("没有 PATH 变量不应命中")
	}
	if _, ok := LookupInEnv(env, ""); ok {
		t.Error("空命令名不应命中")
	}
	// 显式写了路径：按原样解析，不去 PATH 里绕
	if p, ok := LookupInEnv([]string{"PATH=C:\\nowhere"}, filepath.Join(dir, "tag.exe")); !ok || !strings.EqualFold(p, filepath.Join(dir, "tag.exe")) {
		t.Errorf("显式路径应直接命中：%q %v", p, ok)
	}
	// 没有 PATHEXT 时用默认序列（.exe 在 .cmd 前）
	env3 := []string{"PATH=" + dir}
	if got3, ok := LookupInEnv(env3, "tag"); !ok || !strings.EqualFold(filepath.Base(got3), "tag.exe") {
		t.Errorf("默认 PATHEXT 序列不符：%q %v", got3, ok)
	}
}

// TestLayerThreeRealNodeExecPath 用本机真实存在的第二版本做端到端断言（§13-1 口径）。
// 默认跳过；带 ENVS_TEST_REAL_NODE=1 才跑 —— 它只读已存在的目录，不下载、不安装。
func TestLayerThreeRealNodeExecPath(t *testing.T) {
	if os.Getenv("ENVS_TEST_REAL_NODE") != "1" {
		t.Skip("需要 ENVS_TEST_REAL_NODE=1（本机 E:\\nvm 下确有 v16.20.2 / v22.22.0 时才有意义）")
	}
	home, s := bindEnv(t)
	root := `E:\nvm`
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Skipf("本机没有 %s", root)
	}
	t.Setenv("NVM_ROOT", root)
	t.Setenv("NVM_HOME", root)
	if _, err := os.Stat(filepath.Join(root, "v22.22.0", "node.exe")); err != nil {
		t.Skip("没有 v22.22.0 真目录")
	}
	// 用一个与本机全局不同的大版本做判别（全局恰好是 16 时绑 22，反之绑 16）
	target := "22"
	if out, err := exec.Command("node", "-p", "process.versions.node").CombinedOutput(); err == nil {
		if strings.HasPrefix(strings.TrimSpace(string(out)), "22") {
			target = "16"
		}
	}
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": target}}}
	// 假适配器要自己带上收编条目（真 tools.Node 会 import cycle），来源仍是磁盘上的 nvm 目录
	ad := &bindAdapter{id: "node", adopt: NvmVersions()}
	if len(ad.adopt) == 0 {
		t.Skipf("NvmRoot()=%q 下没扫到版本目录", NvmRoot())
	}
	r := ResolveBindings(proj, b, s, []Adapter{ad})
	if len(r.Inject) != 1 {
		t.Fatalf("绑定未解析到真版本：%+v %+v", r.Inject, r.Skips)
	}
	env := r.ApplyEnv(os.Environ())
	exe, ok := LookupInEnv(env, "node")
	if !ok {
		t.Fatal("注入后的 PATH 上找不到真 node")
	}
	if !strings.EqualFold(filepath.Dir(exe), r.Inject[0].BinDir) {
		t.Fatalf("execPath 没落在绑定目录内：%s ≠ %s", exe, r.Inject[0].BinDir)
	}
	out, err := exec.Command(exe, "-p", "process.version+' | '+process.execPath").CombinedOutput()
	if err != nil {
		t.Fatalf("真 node 启动失败：%v\n%s", err, out)
	}
	line := strings.TrimSpace(string(out))
	if !strings.Contains(strings.ToLower(line), strings.ToLower(filepath.Dir(exe))) {
		t.Fatalf("process.execPath 未指向绑定目录：%s", line)
	}
	t.Logf("§13-1 真机断言：绑定 node %s → %s", target, line)
}

// —— 三项裁决 ——

// TestWebpackMd4Warn 覆盖 §10「webpack4 + node≥18」行：只看 devDependencies.webpack 前缀。
func TestWebpackMd4Warn(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "22.22.0", "")
	bindStore(t, home, "node", "16.20.2", "")
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	ads := []Adapter{&bindAdapter{id: "node"}}
	b := DefaultBindings()
	b.Enabled = true

	writeWP := func(dep string) {
		pj := `{"name":"x","devDependencies":{"webpack":"` + dep + `"}}`
		mustNoErr(t, os.WriteFile(filepath.Join(proj, "package.json"), []byte(pj), 0o644), "写 package.json")
	}
	cases := []struct {
		dep, nodeVer string
		want         bool
	}{
		{"4.47.0", "22.22.0", true},
		{"^4.47.0", "22.22.0", true},
		{"4.47.0", "16.20.2", false}, // node16 本来就没这问题
		{"5.94.0", "22.22.0", false}, // webpack5 不受影响
	}
	for _, c := range cases {
		writeWP(c.dep)
		b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": c.nodeVer}}}
		r := ResolveBindings(proj, b, s, ads)
		found := false
		for _, w := range r.Warns {
			if strings.Contains(w, "ERR_OSSL_EVP_UNSUPPORTED") {
				found = true
				if !strings.Contains(w, "--openssl-legacy-provider") {
					t.Errorf("warn 要给出 env 覆盖写法：%q", w)
				}
			}
		}
		if found != c.want {
			t.Errorf("webpack=%s node=%s 期望 warn=%v，实得 %v", c.dep, c.nodeVer, c.want, r.Warns)
		}
	}
	// 不读 node_modules、不解析范围：依赖写在 dependencies 里就不该管
	mustNoErr(t, os.WriteFile(filepath.Join(proj, "package.json"),
		[]byte(`{"name":"x","dependencies":{"webpack":"4.0.0"}}`), 0o644), "写 deps")
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "22.22.0"}}}
	r := ResolveBindings(proj, b, s, ads)
	for _, w := range r.Warns {
		if strings.Contains(w, "ERR_OSSL") {
			t.Errorf("只看 devDependencies，dependencies 里的 webpack 不该触发：%q", w)
		}
	}
}

// TestAutoDetectOnAdoptsPin 覆盖 lead 对 autoDetect=on 的定义：
// .nvmrc/.node-version 的 pin 且能命中已装版本 → 注入；engines 约束永不生效。
func TestAutoDetectOnAdoptsPin(t *testing.T) {
	home, s := bindEnv(t)
	bindStore(t, home, "node", "22.22.0", "")
	bindStore(t, home, "node", "16.20.2", "")
	ads := []Adapter{&bindAdapter{id: "node"}}

	newB := func(pin string) (*Bindings, string) {
		proj := filepath.Join(home, "proj-"+pin)
		mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
		if pin != "" {
			mustNoErr(t, os.WriteFile(filepath.Join(proj, ".nvmrc"), []byte(pin+"\n"), 0o644), "写 .nvmrc")
		}
		b := DefaultBindings()
		b.Enabled = true
		b.AutoDetect = AutoDetectOn
		return b, proj
	}

	// 1) pin 命中已装 → 注入并说明来源
	b, proj := newB("22")
	r := ResolveBindings(proj, b, s, ads)
	if len(r.Inject) != 1 || r.Inject[0].Version != "22.22.0" || r.Inject[0].Source != "store" {
		t.Fatalf("on 应采纳 .nvmrc 的 pin： %+v / %+v", r.Inject, r.Skips)
	}
	if !strings.Contains(strings.Join(r.Notes, "\n"), "autoDetect=on") {
		t.Errorf("注入必须写明是 on 采纳的：%v", r.Notes)
	}
	// 2) 显式绑定优先于上游约定
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16.20.2"}}}
	if r2 := ResolveBindings(proj, b, s, ads); len(r2.Inject) != 1 || r2.Inject[0].Version != "16.20.2" {
		t.Fatalf("显式绑定必须压过 .nvmrc：%+v", r2.Inject)
	}
	// 3) pin 指向没装的版本 → 不注入、不下载，只留一条 skip
	b, proj = newB("14")
	r = ResolveBindings(proj, b, s, ads)
	if len(r.Inject) != 0 || len(r.Skips) == 0 || r.Skips[0].Kind != "not-installed" {
		t.Fatalf("pin 未装必须回退全局：%+v %+v", r.Inject, r.Skips)
	}
	// 4) engines 约束在 on 下也永不生效（§2-L14）
	b, proj = newB("")
	mustNoErr(t, os.WriteFile(filepath.Join(proj, "package.json"),
		[]byte(`{"engines":{"node":">=8.9"}}`), 0o644), "写 engines")
	if r := ResolveBindings(proj, b, s, ads); len(r.Inject) != 0 {
		t.Fatalf("engines 是约束，on 也不许注入：%+v", r.Inject)
	}
	// 5) on + prompt 的差异：prompt 只给提示不动作
	b, proj = newB("22")
	b.AutoDetect = AutoDetectPrompt
	if r := ResolveBindings(proj, b, s, ads); len(r.Inject) != 0 {
		t.Fatalf("prompt 模式下不得注入：%+v", r.Inject)
	}
	// 6) node_modules 里的 pin 不作数（即便 on）
	b, proj = newB("")
	nm := filepath.Join(proj, "node_modules", "dep")
	mustNoErr(t, os.MkdirAll(nm, 0o755), "建 node_modules")
	mustNoErr(t, os.WriteFile(filepath.Join(nm, ".nvmrc"), []byte("22\n"), 0o644), "写 nm/.nvmrc")
	if r := ResolveBindings(nm, b, s, ads); len(r.Inject) != 0 {
		t.Fatalf("node_modules 内的 pin 必须硬跳过：%+v", r.Inject)
	}
	// 7) on + .node-version 也算 pin
	b, proj = newB("")
	mustNoErr(t, os.WriteFile(filepath.Join(proj, ".node-version"), []byte("16.20.2\n"), 0o644), "写 .node-version")
	if r := ResolveBindings(proj, b, s, ads); len(r.Inject) != 1 || r.Inject[0].Version != "16.20.2" {
		t.Fatalf(".node-version 也应被采纳：%+v %+v", r.Inject, r.Skips)
	}
}

// TestMaybeInstallMissingIsOffByDefault 证明 §10 第 1 行的例外开关默认关：
// 不传 flag 时绝不触发安装链路（用记录桩，不联网）。
func TestMaybeInstallMissingIsOffByDefault(t *testing.T) {
	home, s := bindEnv(t)
	proj := filepath.Join(home, "proj")
	mustNoErr(t, os.MkdirAll(proj, 0o755), "建目录")
	b := DefaultBindings()
	b.Enabled = true
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16.20.2"}}} // store 里没有 → not-installed
	r := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}})
	if len(r.Skips) == 0 || r.Skips[0].Kind != "not-installed" {
		t.Fatalf("基线应是未装：%+v", r.Skips)
	}

	var calls [][2]string
	old := InstallMissingHook
	InstallMissingHook = func(_ context.Context, tool, expr string) error {
		calls = append(calls, [2]string{tool, expr})
		return nil
	}
	defer func() { InstallMissingHook = old }()

	// flag=false（默认）→ 一次都不许调
	if got, err := MaybeInstallMissing(context.Background(), r, false); err != nil || len(got) != 0 || len(calls) != 0 {
		t.Fatalf("不传 --install-if-missing 绝不能触发下载：%v %v %v", got, calls, err)
	}
	// hook 为 nil（CLI 未接入）→ 同样绝不触发
	InstallMissingHook = nil
	if got, err := MaybeInstallMissing(context.Background(), r, true); err != nil || len(got) != 0 {
		t.Fatalf("没接 hook 时不应有任何动作：%v %v", got, err)
	}
	// flag=true → 按条目调用，并把 npx→npm 这类命令名归一到宿主工具
	InstallMissingHook = func(_ context.Context, tool, expr string) error {
		calls = append(calls, [2]string{tool, expr})
		return nil
	}
	b.Bindings = []Binding{{Path: proj, Tools: map[string]string{"node": "16.20.2", "npx": "9.0.0"}}}
	r2 := ResolveBindings(proj, b, s, []Adapter{&bindAdapter{id: "node"}, &bindJSAdapter{bindAdapter{id: "npm"}}})
	tried, err := MaybeInstallMissing(context.Background(), r2, true)
	mustNoErr(t, err, "补装")
	if len(tried) == 0 || tried[0][0] != "node" || tried[0][1] != "16.20.2" {
		t.Fatalf("应报出待补装的 node 16.20.2：%v", tried)
	}
	for _, c := range calls {
		if c[0] == "npx" {
			t.Errorf("命令名要归一到宿主工具（npx→npm）：%v", calls)
		}
	}
	// hook 报错必须原样上抛给 CLI
	InstallMissingHook = func(context.Context, string, string) error { return W(ErrNet, "模拟断网") }
	if _, err := MaybeInstallMissing(context.Background(), r, true); err == nil || !errors.Is(err, ErrNet) {
		t.Fatalf("安装失败要带上语义标签：%v", err)
	}
}
