package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MUHUIX/envswitch/internal/settings"
)

// 热更新：读 updates 分支 latest.json → 下载 zip → sha256 校验 → 解包暂存 →
// 旧 exe 让位 .old → 新文件就位 → 调用方带 --updated 重启 → 新进程 heartbeat 后清 .old。
// 安全边界：仅 HTTPS + sha256；不删 .old 前任何一步都可回滚。

const (
	defaultRepo     = "MUHUIX/EnvSwitch"
	defaultRawHost  = "https://raw.githubusercontent.com"
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
	// OnStage 汇报「下载百分比之外」的阶段（解包/替换），供 GUI 画进度；可为 nil。
	OnStage func(stage, msg string)
}

func NewSelfUpdate(s *settings.Settings) *SelfUpdate { return &SelfUpdate{s: s} }

func (u *SelfUpdate) homeDir() string { return settings.Home() }

// stage 是 OnStage 的静默包装：CLI 与测试不关心阶段，传 nil 即可。
func (u *SelfUpdate) stage(name, msg string) {
	if u.OnStage != nil {
		u.OnStage(name, msg)
	}
}

// Download 拉更新包、校验并解包（不应用）。返回解出的文件 map 名→临时路径。
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
	u.stage("unpack", "校验通过，正在解包…")
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
	u.stage("apply", "正在替换程序文件…")
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

// moveOver 把 src 就位为 dst。
// 同卷优先 os.Rename（原子、零拷贝）；跨卷时 Go 在 Windows 走 MoveFileEx 且不带
// MOVEFILE_COPY_ALLOWED，会直接报 ERROR_NOT_SAME_DEVICE（更新包暂存在 C:\ 的
// %LOCALAPPDATA%，程序却可能装在别的盘），因此退化为「复制到 dst 同目录临时文件
// → 核对大小 → 删 dst → 换名就位」。临时文件与 dst 同卷，末次 rename 必然成功；
// 中途任何一步失败都只留下 .incoming，不会留半个可执行文件在原位。
func moveOver(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	tmp := dst + ".incoming"
	os.Remove(tmp)
	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("跨卷复制失败: %w", err)
	}
	ss, err1 := os.Stat(src)
	ts, err2 := os.Stat(tmp)
	if err1 != nil || err2 != nil || ss.Size() != ts.Size() {
		os.Remove(tmp)
		return errors.New("复制结果大小与源不符，已放弃替换")
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		os.Remove(tmp)
		return fmt.Errorf("清理原位文件失败: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("换名就位失败: %w", err)
	}
	os.Remove(src) // 清掉跨卷复制留下的暂存原件
	return nil
}

// copyFile 复制到 dst 并在返回前关闭全部句柄 —— Windows 上句柄未关时源文件删不掉，
// 因此不能用 defer 把关闭拖到调用方之后。
func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	df, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		sf.Close()
		return err
	}
	_, cpErr := io.Copy(df, sf)
	clErr := df.Close()
	if sfErr := sf.Close(); sfErr != nil && cpErr == nil {
		cpErr = sfErr
	}
	if cpErr != nil {
		return cpErr
	}
	return clErr
}

// WriteHeartbeat / MarkGood / RollbackPending —— 启动侧三段式：
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

// RollbackPending 启动最早调用：存在 .old 且 heartbeat 新鲜 = 刚完成更新启动成功；
// 存在 .old 且 heartbeat 缺失 = 崩溃回滚窗口，交调用方决定（GUI 启动 5s 定时器调用）。
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
