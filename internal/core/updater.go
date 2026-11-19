package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HUIdada1/envswitch/internal/settings"
)

// 热更新：读 updates 分支 latest.json → 下载 zip → sha256 校验 →
// 旧 exe 改名 .old → 新文件就位 → 调用方带 --updated 重启 → 新进程 heartbeat 后清 .old。
// 安全边界：仅 HTTPS + sha256；不删 .old 前任何一步都可回滚。

const (
	defaultRepo    = "MUHUIX/EnvSwitch"
	defaultRawHost = "https://raw.githubusercontent.com"
	updateHeartbeat = "update\\heartbeat"
	updateOldSuffix = ".old"
)

type UpdateManifest struct {
	Version string                      `json:"version"`
	Date    string                      `json:"date"`
	Notes   string                      `json:"notes_zh"`
	Assets  map[string]UpdateAssetEntry `json:"assets"` // 键 windows/amd64
}

type UpdateAssetEntry struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// manifestURL latest.json 固定走 updates 分支；ENVS_UPDATE_MANIFEST_URL 可整体覆盖
// （企业私有镜像 / 集成测试桩）；代理前缀兜底 raw 直连失败场景。
func manifestURL(proxy string) string {
	if v := os.Getenv("ENVS_UPDATE_MANIFEST_URL"); v != "" {
		return v
	}
	u := fmt.Sprintf("%s/%s/updates/latest.json", defaultRawHost, defaultRepo)
	if proxy != "" {
		if !strings.HasSuffix(proxy, "/") {
			proxy += "/"
		}
		return proxy + u
	}
	return u
}

// CheckUpdate 比较当前版本与 manifest；无更新返回 nil。
func CheckUpdate(ctx context.Context, proxy, current string) (*UpdateManifest, *UpdateAssetEntry, error) {
	var m UpdateManifest
	if err := GetJSON(ctx, manifestURL(proxy), &m); err != nil {
		return nil, nil, err
	}
	entry, ok := m.Assets["windows/amd64"]
	if !ok {
		return nil, nil, W(ErrNet, "清单缺 windows/amd64 资产")
	}
	if CompareVersions(strings.TrimPrefix(m.Version, "v"), strings.TrimPrefix(current, "v")) <= 0 {
		return nil, nil, nil
	}
	return &m, &entry, nil
}

// SelfUpdate 更新包的下载/应用/回滚编排（home 取 settings.Home()，支持 ENVS_HOME 隔离）。
type SelfUpdate struct {
	s *settings.Settings
}

func NewSelfUpdate(s *settings.Settings) *SelfUpdate { return &SelfUpdate{s: s} }

func (u *SelfUpdate) homeDir() string { return settings.Home() }

// Download 拉更新包并校验（不应用）。返回解出的文件 map 名→临时路径。
func (u *SelfUpdate) Download(ctx context.Context, asset *UpdateAssetEntry, progress Progress) (map[string]string, error) {
	dst := filepath.Join(u.homeDir(), "update", "package.zip")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	os.Remove(dst)
	if err := FetchURL(ctx, WithProxy(u.s.Proxy, asset.URL), dst, asset.SHA256, progress); err != nil {
		return nil, err
	}
	outDir := filepath.Join(u.homeDir(), "update", "staged")
	os.RemoveAll(outDir)
	if err := Unpack(dst, outDir); err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, name := range []string{"EnvSwitch.exe", "envs.exe", "SHA256SUMS.txt"} {
		p := filepath.Join(outDir, name)
		if _, err := os.Stat(p); err == nil {
			files[name] = p
		}
	}
	if len(files) == 0 {
		return nil, W(ErrVerify, "更新包内没有可执行文件")
	}
	return files, nil
}

// Apply 自替换当前进程 exe 与同目录 envs.exe（GUI 更新自身）。
// 成功后调用方应立刻以 --updated 重启。
func (u *SelfUpdate) Apply(files map[string]string, exePath string) error {
	base := filepath.Base(exePath)
	selfSrc, ok := files[base]
	if !ok {
		return W(ErrVerify, "更新包不含当前主程序 %s", base)
	}
	dir := filepath.Dir(exePath)
	old := exePath + updateOldSuffix
	os.Remove(old)
	// 运行中 exe 不能覆写但可以改名
	if err := os.Rename(exePath, old); err != nil {
		return fmt.Errorf("让位旧版本失败: %w", err)
	}
	if err := moveOver(selfSrc, exePath); err != nil {
		_ = os.Rename(old, exePath) // 回滚
		return fmt.Errorf("放置新版本失败: %w", err)
	}
	// 兄弟 CLI 顺手更新
	if sibling, ok := files[siblingName(base)]; ok {
		_ = moveOver(sibling, filepath.Join(dir, siblingName(base)))
	}
	return nil
}

func siblingName(base string) string {
	if strings.EqualFold(base, "EnvSwitch.exe") {
		return "envs.exe"
	}
	return "EnvSwitch.exe"
}

func moveOver(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	return os.Rename(src, dst)
}

// WriteHeartbeat / MarkUpdateGood / SweepOld —— 启动侧三段式：
// 新进程启动即 heartbeat；存活 5s 判成功清 .old；启动时若见残留 .old 且无 heartbeat，回滚。

func (u *SelfUpdate) WriteHeartbeat() error {
	p := filepath.Join(u.homeDir(), updateHeartbeat)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(time.Now().Format(time.RFC3339)), 0o644)
}

func (u *SelfUpdate) UpdateSucceeded() bool {
	data, err := os.ReadFile(filepath.Join(u.homeDir(), updateHeartbeat))
	if err != nil {
		return false
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	return err == nil && time.Since(ts) < 10*time.Minute
}

func (u *SelfUpdate) MarkGood() {
	os.Remove(filepath.Join(u.homeDir(), updateHeartbeat))
}

// RollbackIfNeeded 启动最早调用：.old 存在且本次 heartbeat 已写但版本仍启动失败的外部表现
// 无法在本进程内自证，因此语义收敛为——存在 .old 且 heartbeat 新鲜 = 刚完成更新启动成功，删 .old；
// 存在 .old 且 heartbeat 缺失且距上次更新 <24h = 崩溃回滚窗口，交调用方决定（GUI 启动 5s 定时器调用）。
func (u *SelfUpdate) RollbackPending(exePath string) bool {
	old := exePath + updateOldSuffix
	if _, err := os.Stat(old); err != nil {
		return false
	}
	return !u.UpdateSucceeded()
}

// DoRollback 把 .old 换回原位并启动。
func (u *SelfUpdate) DoRollback(exePath string) error {
	old := exePath + updateOldSuffix
	if err := os.Rename(exePath, exePath+".broken"); err != nil {
		return err
	}
	if err := os.Rename(old, exePath); err != nil {
		_ = os.Rename(exePath+".broken", exePath)
		return err
	}
	os.Remove(exePath + ".broken")
	u.MarkGood()
	return nil
}
