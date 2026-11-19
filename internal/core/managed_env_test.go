package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// 用内存替换注册表读写：测试绝不碰真实 HKCU\Environment。
func stubUserEnv(t *testing.T) map[string]string {
	t.Helper()
	store := map[string]string{}
	oldRead, oldWrite, oldDelete, oldBroadcast := readUserEnv, writeUserEnv, deleteUserEnv, broadcastEnv
	readUserEnv = func(k string) (string, bool, error) {
		v, ok := store[k]
		return v, ok, nil
	}
	writeUserEnv = func(k, v string) error {
		store[k] = v
		return nil
	}
	deleteUserEnv = func(k string) error {
		delete(store, k)
		return nil
	}
	broadcastEnv = func() {}
	t.Cleanup(func() {
		readUserEnv, writeUserEnv, deleteUserEnv, broadcastEnv = oldRead, oldWrite, oldDelete, oldBroadcast
	})
	return store
}

func TestSetManagedEnvRequiresActiveTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	stubUserEnv(t)
	s := &settings.Settings{}
	if err := SetManagedEnv(s, nil, "JAVA_HOME", true); err == nil {
		t.Fatal("没有激活 java 时不应允许托管 JAVA_HOME")
	}
	if err := SetManagedEnv(s, nil, "NOT_A_VAR", true); err == nil {
		t.Fatal("白名单外的变量应被拒绝")
	}
}

// 托管 → 切换（值不变，因为指向 current）→ 关闭后恢复原值。
func TestSetManagedEnvOnOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	env := stubUserEnv(t)
	env["JAVA_HOME"] = `C:\OldJdk`

	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	javaDir := filepath.Join(home, "store", "java", "17.0.9")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := settings.CurrentLink(s, "java")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sys.CreateJunction(link, javaDir); err != nil {
		t.Fatalf("创建测试 junction: %v", err)
	}

	if err := SetManagedEnv(s, []Adapter{locAdapter{id: "java"}}, "JAVA_HOME", true); err != nil {
		t.Fatalf("托管失败: %v", err)
	}
	if env["JAVA_HOME"] != javaDir {
		t.Fatalf("托管值应指向安装根: %q", env["JAVA_HOME"])
	}
	if !IsManagedEnv(s, "JAVA_HOME") {
		t.Fatal("托管列表未记录")
	}
	if bak := s.EnvBackup["JAVA_HOME"]; !bak.Had || bak.Value != `C:\OldJdk` {
		t.Fatalf("原值未备份: %+v", bak)
	}

	if err := SetManagedEnv(s, []Adapter{locAdapter{id: "java"}}, "JAVA_HOME", false); err != nil {
		t.Fatalf("解除托管失败: %v", err)
	}
	if env["JAVA_HOME"] != `C:\OldJdk` {
		t.Fatalf("未恢复原值: %q", env["JAVA_HOME"])
	}
	if IsManagedEnv(s, "JAVA_HOME") {
		t.Fatal("托管列表未清理")
	}
}

// 目标工具的最后一个版本被删、junction 已摘：同步时自动恢复并取消托管。
func TestSyncManagedEnvDropsDeadTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	env := stubUserEnv(t)
	env["GOROOT"] = filepath.Join(home, "current", "go")
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	s.ManagedEnv = []string{"GOROOT"}
	s.EnvBackup["GOROOT"] = settings.EnvBackup{Had: true, Value: `C:\Go`}

	dropped, err := SyncManagedEnv(s, []Adapter{locAdapter{id: "go", binSub: "bin"}})
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != "GOROOT" {
		t.Fatalf("应报告自动取消的变量: %+v", dropped)
	}
	if env["GOROOT"] != `C:\Go` {
		t.Fatalf("应恢复原值: %q", env["GOROOT"])
	}
	if IsManagedEnv(s, "GOROOT") {
		t.Fatal("应移出托管列表")
	}
	if _, ok := s.EnvBackup["GOROOT"]; ok {
		t.Fatal("备份应在恢复后清理")
	}
}

// java/go/maven 的 current 链接指向 <ver>\bin，托管变量必须上提到版本根，
// 否则 JAVA_HOME 会变成 ...\bin，Maven 去找 bin\bin\java.exe。
func TestManagedEnvRootLiftsBinSub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	verDir := filepath.Join(home, "store", "java", "17.0.9")
	binDir := filepath.Join(verDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := settings.CurrentLink(s, "java")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sys.CreateJunction(link, binDir); err != nil {
		t.Fatalf("创建测试 junction: %v", err)
	}

	root, ok := managedEnvRoot([]Adapter{locAdapter{id: "java", binSub: "bin"}}, s, "java")
	if !ok || filepath.Clean(root) != filepath.Clean(verDir) {
		t.Fatalf("托管根应为版本根: %q ok=%v", root, ok)
	}
}
