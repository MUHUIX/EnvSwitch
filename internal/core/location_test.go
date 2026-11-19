package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MUHUIX/envswitch/internal/settings"
)

type locAdapter struct {
	id     string
	binSub string
}

func (a locAdapter) ID() string                                            { return a.id }
func (locAdapter) Title() string                                           { return "test" }
func (locAdapter) Group() string                                           { return "runtime" }
func (locAdapter) FetchRemote(*settings.Settings) ([]RemoteVersion, error) { return nil, nil }
func (a locAdapter) BinSub() string                                        { return a.binSub }
func (locAdapter) Smoke() (string, []string)                               { return "", nil }

type locJSAdapter struct {
	locAdapter
	shimMap map[string]string
}

func (a locJSAdapter) Shims() map[string]string { return a.shimMap }

// 默认布局：store 在 home\store 下，新版本落点用 <版本号> 占位，
// PATH 条目只陈述事实（node 属于自动维护的三个运行时）。
func TestLocateDefaultStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	if err := os.MkdirAll(filepath.Join(home, "store", "node", "22.22.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	loc := Locate(locAdapter{id: "node"}, s)
	if loc.StoreDir != filepath.Join(home, "store") {
		t.Fatalf("版本库根目录: %q", loc.StoreDir)
	}
	if loc.StoreCustom {
		t.Fatal("未设置 storePath 时不应标记为自定义")
	}
	if loc.ToolStoreDir != filepath.Join(home, "store", "node") {
		t.Fatalf("工具目录: %q", loc.ToolStoreDir)
	}
	if want := filepath.Join(home, "store", "node", "<版本号>"); loc.VersionHint != want {
		t.Fatalf("新版本落点模板: %q", loc.VersionHint)
	}
	if len(loc.PathEntries) != 1 || loc.PathEntries[0].Role != "激活链接" {
		t.Fatalf("node 应有一条激活链接条目: %+v", loc.PathEntries)
	}
	if !strings.Contains(loc.PathEntries[0].Note, "自动写入") {
		t.Fatalf("node 的 PATH 说明应提到自动写入: %q", loc.PathEntries[0].Note)
	}
}

// 自定义版本库 + JS 工具：目录跟着 storePath 走，PATH 条目换成 shims 目录。
func TestLocateCustomStoreAndShims(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "versions")
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{StorePath: custom}

	loc := Locate(locJSAdapter{
		locAdapter: locAdapter{id: "npm"},
		shimMap:    map[string]string{"npm": "bin/npm-cli.js"},
	}, s)
	if !loc.StoreCustom || loc.StoreDir != custom {
		t.Fatalf("自定义版本库: custom=%v dir=%q", loc.StoreCustom, loc.StoreDir)
	}
	if loc.ToolStoreDir != filepath.Join(custom, "npm") {
		t.Fatalf("工具目录应跟随自定义版本库: %q", loc.ToolStoreDir)
	}
	if len(loc.Shims) != 1 || loc.Shims[0].Name != "npm.cmd" {
		t.Fatalf("shim 列表: %+v", loc.Shims)
	}
	if len(loc.PathEntries) != 1 || loc.PathEntries[0].Role != "命令 shim" {
		t.Fatalf("js 工具的 PATH 条目应为 shim 目录: %+v", loc.PathEntries)
	}
	if !strings.Contains(loc.PathEntries[0].Note, "current\\npm") {
		t.Fatalf("shim 说明应指出指向激活链接: %q", loc.PathEntries[0].Note)
	}
}

// 可托管变量要带上目标链接与开关能力，不可托管的变量不能出现 manageKey。
func TestLocateManagedEnvFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}

	loc := Locate(locAdapter{id: "go", binSub: "bin"}, s)
	var goroot, gopath *EnvVar
	for i := range loc.EnvVars {
		switch loc.EnvVars[i].Key {
		case "GOROOT":
			goroot = &loc.EnvVars[i]
		case "GOPATH":
			gopath = &loc.EnvVars[i]
		}
	}
	if goroot == nil || goroot.ManageKey != "GOROOT" || goroot.ManageTool != "go" {
		t.Fatalf("GOROOT 应可托管: %+v", goroot)
	}
	wantTarget := filepath.Join(home, "current", "go")
	if goroot.Target != wantTarget {
		t.Fatalf("托管目标应为激活链接: %q", goroot.Target)
	}
	if gopath == nil || gopath.ManageKey != "" {
		t.Fatalf("GOPATH 不应出现托管开关: %+v", gopath)
	}
}

// 来源判定：当前链接 / 版本库 / 外部目录 / 不存在。
func TestEnvVarOrigin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	if got := envVarOrigin(filepath.Join(home, "current", "go"), s); !strings.Contains(got, "当前激活版本") {
		t.Fatalf("current 判定: %q", got)
	}
	if got := envVarOrigin(filepath.Join(home, "store", "go", "1.26.5"), s); !strings.Contains(got, "版本库") {
		t.Fatalf("store 判定: %q", got)
	}
	outside := t.TempDir()
	if got := envVarOrigin(outside, s); !strings.Contains(got, "外部安装目录") {
		t.Fatalf("外部目录判定: %q", got)
	}
	if got := envVarOrigin(filepath.Join(home, "nope"), s); !strings.Contains(got, "不存在") {
		t.Fatalf("不存在判定: %q", got)
	}
}

// maven 页的 JAVA_HOME 是共享变量：已托管时不能渲染空值，ManageKey 也必须为空
// （开关只放在 java 页，避免两处都能改）。
func TestLocateSharedManagedVarUsesRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	env := stubUserEnv(t)
	javaHome := filepath.Join(home, "store", "java", "17.0.9")
	env["JAVA_HOME"] = javaHome
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	s.ManagedEnv = []string{"JAVA_HOME"}

	loc := Locate(locAdapter{id: "maven", binSub: "bin"}, s)
	var hit *EnvVar
	for i := range loc.EnvVars {
		if loc.EnvVars[i].Key == "JAVA_HOME" {
			hit = &loc.EnvVars[i]
		}
	}
	if hit == nil {
		t.Fatal("maven 页应展示 JAVA_HOME")
	}
	if hit.ManageKey != "" {
		t.Fatalf("共享变量不应出现托管开关: %+v", hit)
	}
	if !hit.Managed || hit.Value != javaHome || !hit.Set {
		t.Fatalf("共享托管变量应显示注册表实际值: %+v", hit)
	}
}
