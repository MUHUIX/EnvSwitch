package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

func TestValidateStoreRootRejects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	cases := []struct {
		name string
		path string
	}{
		{"空值", ""},
		{"网络路径", `\\server\share`},
		{"盘根", `D:\`},
		{"current 内部", filepath.Join(home, "current", "store")},
		{"bin 内部", filepath.Join(home, "bin", "store")},
		{"数据目录下的其他位置", filepath.Join(home, "downloads")},
		{"当前版本库本身", filepath.Join(home, "store")},
	}
	for _, c := range cases {
		if _, err := ValidateStoreRoot(s, c.path); err == nil {
			t.Fatalf("%s 应被拒绝: %q", c.name, c.path)
		}
	}
}

func TestValidateStoreRootRejectsNonEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStoreRoot(s, dir); err == nil {
		t.Fatal("非空目录应被拒绝，避免覆盖已有文件")
	}
}

func TestValidateStoreRootAcceptsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	dir := filepath.Join(t.TempDir(), "versions")
	got, err := ValidateStoreRoot(s, dir)
	if err != nil {
		t.Fatalf("空目录应通过: %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Fatalf("应返回规范路径: %q", got)
	}
}

// 同盘迁移：文件搬走、旧根消失、配置落盘、激活链接改指新根。
func TestMoveStoreSameVolumeRebuildsLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	verDir := filepath.Join(home, "store", "node", "22.22.0")
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(verDir, "node.exe"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := settings.CurrentLink(s, "node")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sys.CreateJunction(link, verDir); err != nil {
		t.Fatalf("创建测试 junction: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "envstore")
	ads := []Adapter{locAdapter{id: "node"}}
	if err := MoveStore(context.Background(), s, ads, dst, nil); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	moved := filepath.Join(dst, "node", "22.22.0", "node.exe")
	if b, err := os.ReadFile(moved); err != nil || string(b) != "fake" {
		t.Fatalf("文件未迁到新根: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "store")); !os.IsNotExist(err) {
		t.Fatalf("同盘迁移后旧版本库应已移走: %v", err)
	}
	if s.StorePath != dst {
		t.Fatalf("StorePath 未落盘: %q", s.StorePath)
	}
	target, ok := sys.ReadJunction(link)
	if !ok || filepath.Clean(target) != filepath.Clean(filepath.Join(dst, "node", "22.22.0")) {
		t.Fatalf("激活链接未改指新根: %q ok=%v", target, ok)
	}
}

// 空版本库换路径：只改配置，不报错。
func TestMoveStoreEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "empty-store")
	if err := MoveStore(context.Background(), s, nil, dst, nil); err != nil {
		t.Fatalf("空库迁移失败: %v", err)
	}
	if s.StorePath != dst {
		t.Fatalf("配置未更新: %q", s.StorePath)
	}
	if fi, err := os.Stat(dst); err != nil || !fi.IsDir() {
		t.Fatalf("目标目录未创建: %v", err)
	}
}

// java/go 这类 BinSub 非空的工具，链接指向 <ver>\bin，迁移后也必须落在
// 新根的同一级子目录，否则 PATH 上的 current\java 会指向不存在的路径。
func TestMoveStoreKeepsBinSubLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, "store", "java", "17.0.9", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "java.exe"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := settings.CurrentLink(s, "java")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sys.CreateJunction(link, binDir); err != nil {
		t.Fatalf("创建测试 junction: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "envstore")
	if err := MoveStore(context.Background(), s, []Adapter{locAdapter{id: "java", binSub: "bin"}}, dst, nil); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	target, ok := sys.ReadJunction(link)
	want := filepath.Join(dst, "java", "17.0.9", "bin")
	if !ok || filepath.Clean(target) != filepath.Clean(want) {
		t.Fatalf("链接应指向新根的 bin 目录: got %q want %q ok=%v", target, want, ok)
	}
}
