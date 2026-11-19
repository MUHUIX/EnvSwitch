// Package app 是绑定层：把 core 的能力整形成 GUI 可直接消费的 DTO 与动作，
// 不实现任何业务规则（规则全部在 core）。
package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	wrt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/netproxy"
	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
	"github.com/MUHUIX/envswitch/internal/tools"
)

// App 单实例服务。binds 方法名即前端 window.go.app.App.<Method>。
type App struct {
	ctx          context.Context
	mu           sync.Mutex
	updateMu     sync.Mutex // 保护 lastUpdateCheck（后台定时检查与前端查询并发）
	s            *settings.Settings
	quitting     atomic.Bool // 本次关闭=真退的一次性内存旗标（托盘退出/热更新重启），不落盘
	ads          []core.Adapter
	busy         map[string]context.CancelFunc // tool -> 进行中的安装任务（同工具串行）
	updateStopCh chan struct{}
	selfApplying atomic.Bool // 防重复点击「立即更新」并发跑两次自替换
	storeMoving  atomic.Bool // 版本库迁移中：给前端/入口一个快速拒绝标志

	// opsMu 保护"版本库 + junction"这类跨文件操作：Use/Remove/Install/托管
	// 持读锁，ChangeStore 持写锁。storeMoving 只能挡住"已经看到标志"的调用，
	// 挡不住"检查通过后才开始"的窗口，所以真正的互斥要靠这把锁。
	opsMu sync.RWMutex
}

func New() *App {
	s, err := settings.Load()
	if err != nil {
		s = &settings.Settings{Version: 1, IndexCacheMinutes: 10, Mirrors: map[string]string{}}
	}
	return &App{s: s, ads: tools.All(), busy: map[string]context.CancelFunc{}}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	_ = core.EnsureShims(a.s, a.ads) // 模板升级自愈
	a.startUpdateScheduler()
}

func (a *App) startUpdateScheduler() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updateStopCh != nil {
		close(a.updateStopCh)
		a.updateStopCh = nil
	}
	if !a.s.AutoCheckUpdate {
		return
	}
	stopCh := make(chan struct{})
	a.updateStopCh = stopCh

	go func() {
		// 启动后延迟 3 秒做首次自动检查
		select {
		case <-time.After(3 * time.Second):
			a.CheckUpdateNow()
		case <-stopCh:
			return
		}

		// 开启后每小时自动检查更新（仅检测通知，绝不自动安装，安装由用户做决定）
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.CheckUpdateNow()
			case <-stopCh:
				return
			}
		}
	}()
}

func (a *App) stopUpdateScheduler() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updateStopCh != nil {
		close(a.updateStopCh)
		a.updateStopCh = nil
	}
}

// —— DTO ——

type ToolView struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Kind      string       `json:"kind"` // runtime | js
	Group     string       `json:"group"`
	Current   *InstallView `json:"current"`
	Lamp      string       `json:"lamp"`        // ok / idle / up / off / err —— 设计语言§6 状态灯语义
	Remote    string       `json:"remoteState"` // fresh / stale / pending / error
	Installed []string     `json:"installed"`   // 本机可用版本列表（快捷切换）
}

type InstallView struct {
	Version  string `json:"version"`
	Source   string `json:"source"`
	Size     string `json:"size"`
	Editable bool   `json:"editable"`
	Active   bool   `json:"active"`
}

type RowView struct {
	Version string `json:"version"`
	Source  string `json:"source"` // store / nvm / detected
	Size    string `json:"size"`
	RootDir string `json:"rootDir"` // 安装目录（store 为 store\<tool>\<ver>，nvm 为版本目录）
	BinDir  string `json:"binDir"`
	Active  bool   `json:"active"`
}

type RemoteRow struct {
	Version   string `json:"version"`
	Label     string `json:"label"`
	Date      string `json:"date"`
	Size      string `json:"size"`
	Installed bool   `json:"installed"`
}

type ProgressEvent struct {
	Tool    string  `json:"tool"`
	Stage   string  `json:"stage"` // download / verify / unpack / smoke / done / error
	Version string  `json:"version"`
	Pct     float64 `json:"pct"`
	Speed   string  `json:"speed"`
	Message string  `json:"message"`
}

type CheckView struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type SettingsView struct {
	Autostart          bool              `json:"autostart"`
	CloseToTray        bool              `json:"closeToTray"`
	AutoCheckUpdate    bool              `json:"autoCheckUpdate"`
	CheckUpdateOnStart bool              `json:"checkUpdateOnStart"`
	StorePath          string            `json:"storePath"`
	Mirrors            map[string]string `json:"mirrors"`
	Proxy              string            `json:"proxy"`
	IndexCacheMinutes  int               `json:"indexCacheMinutes"`
	Version            string            `json:"version"`
	RemoteState        map[string]string `json:"remoteState"`
	TotalSize          string            `json:"totalSize"`
	InstalledCount     int               `json:"installedCount"`
	ManagedEnv         []string          `json:"managedEnv"`
}

type sysDetectCacheItem struct {
	ver    string
	source string
	path   string
	ts     time.Time
}

var (
	sysDetectCache   = map[string]sysDetectCacheItem{}
	sysDetectCacheMu sync.Mutex
	verReg           = regexp.MustCompile(`(?i)(?:go|version\s+"?|Python\s+)?v?(\d+\.\d+(?:\.\d+)?(?:_\d+)?)`)
)

func (a *App) detectActiveSystemVersion(ad core.Adapter) (string, string, string) {
	sysDetectCacheMu.Lock()
	if it, ok := sysDetectCache[ad.ID()]; ok && time.Since(it.ts) < 60*time.Second {
		sysDetectCacheMu.Unlock()
		return it.ver, it.source, it.path
	}
	sysDetectCacheMu.Unlock()

	exeName := core.PathExeName(ad)
	if exeName == "" {
		return "", "", ""
	}
	resolved := core.WhichResolved(exeName)
	if resolved == "" {
		return "", "", ""
	}

	source := "系统"
	if root := core.NvmRoot(); root != "" && strings.HasPrefix(strings.ToLower(resolved), strings.ToLower(root)) {
		source = "nvm"
	} else if strings.Contains(strings.ToLower(resolved), "nvm") {
		source = "nvm"
	}

	ver := queryVersionFromExe(ad, resolved)
	if ver == "" {
		ver = "已检测"
	}

	sysDetectCacheMu.Lock()
	sysDetectCache[ad.ID()] = sysDetectCacheItem{ver: ver, source: source, path: resolved, ts: time.Now()}
	sysDetectCacheMu.Unlock()

	return ver, source, resolved
}

func queryVersionFromExe(ad core.Adapter, resolved string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	var args []string
	switch ad.ID() {
	case "go":
		args = []string{"version"}
	case "java":
		args = []string{"-version"}
	case "nvm":
		args = []string{"version"}
	case "maven", "gradle", "bun", "php", "node", "npm", "pnpm", "yarn":
		args = []string{"-v"}
	case "kubectl":
		args = []string{"version", "--client"}
	default:
		args = []string{"--version"}
	}

	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	m := verReg.FindStringSubmatch(string(out))
	if len(m) >= 2 {
		return m[1]
	}
	// 拿不到规范版本号就返回空，由调用方标记"已检测"，
	// 不要把 "Error: JAVA_HOME not found" 之类的报错当成版本号显示。
	return ""
}

// —— 查询接口 ——

func (a *App) Overview() []ToolView {
	out := make([]ToolView, len(a.ads))
	// 系统版本探测要起子进程，串行跑 20 多个工具会让首屏明显等待；
	// 并发上限 4 路，既缩短总耗时又不至于把机器打满。
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, ad := range a.ads {
		wg.Add(1)
		go func(i int, ad core.Adapter) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = a.toolView(ad)
		}(i, ad)
	}
	wg.Wait()
	return out
}

func (a *App) toolView(ad core.Adapter) ToolView {
	kind := "runtime"
	if _, ok := ad.(core.JSRunner); ok {
		kind = "js"
	}
	installs := core.ListInstallsFast(ad, a.s)
	var installedNames []string
	for _, iv := range installs {
		installedNames = append(installedNames, iv.Version)
	}

	cur, _, _ := core.CurrentInstall(ad, a.s)
	tv := ToolView{
		ID:        ad.ID(),
		Title:     ad.Title(),
		Kind:      kind,
		Group:     ad.Group(),
		Remote:    remoteCacheState(a.s, ad.ID()),
		Installed: installedNames,
	}
	if cur != nil {
		tv.Current = &InstallView{Version: cur.Version, Source: cur.Source, Size: core.HumanSize(cur.Size), Active: true, Editable: cur.Editable}
		tv.Lamp = "ok"
	} else {
		// 未在 EnvSwitch 内部激活，实时检测系统当前生效的真实版本
		if sysVer, sysSrc, _ := a.detectActiveSystemVersion(ad); sysVer != "" {
			tv.Current = &InstallView{Version: sysVer, Source: sysSrc, Size: "-", Active: true, Editable: false}
			tv.Lamp = "ok"
		} else if len(installs) > 0 {
			tv.Lamp = "idle" // 本地有安装但没激活
		} else {
			tv.Lamp = "off"
		}
	}
	return tv
}

// RefreshEnv 清空系统版本探测缓存并重新总览（「检索环境」按钮 = 强制真机复检）。
func (a *App) RefreshEnv() []ToolView {
	sysDetectCacheMu.Lock()
	sysDetectCache = map[string]sysDetectCacheItem{}
	sysDetectCacheMu.Unlock()
	return a.Overview()
}

func (a *App) Local(tool string) ([]RowView, error) {
	ad := tools.ByID(tool)
	if ad == nil {
		return nil, core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	cur, _, _ := core.CurrentInstall(ad, a.s)
	installs := core.ListInstalls(ad, a.s)
	out := make([]RowView, 0, len(installs)+1)
	for _, iv := range installs {
		active := cur != nil && core.NormVersion(cur.Version) == core.NormVersion(iv.Version)
		// RootDir 是"这个版本装在哪"：store 版本取 store\<tool>\<ver>，
		// nvm 收编的目录本身就是版本根。BinDir 只是进 PATH 的那一级（java/go 是 bin 子目录）。
		root := iv.BinDir
		if iv.Source == "store" {
			if cand := settings.VersionDir(a.s, ad.ID(), iv.Version); isDir(cand) {
				root = cand
			}
		}
		out = append(out, RowView{
			Version: iv.Version, Source: iv.Source, Size: core.HumanSize(iv.Size),
			RootDir: root, BinDir: iv.BinDir, Active: active,
		})
	}
	// 系统里实际生效的版本追加为只读行 —— 但只在版本库里没有它的时候。
	// 同一份安装往往有两个入口（nvm 的 C:\nvm4w\nodejs 是指回 E:\nvm\v16.20.2 的
	// 符号链接），照单全收会让界面上同一个版本出现两行。
	if sysVer, sysSrc, binDir := a.detectActiveSystemVersion(ad); sysVer != "" {
		if i := core.IndexOfVersion(installs, sysVer); i >= 0 {
			// 已有这一行：没走 EnvSwitch 激活时，生效的就是它，把状态补上去
			if cur == nil {
				out[i].Active = true
			}
		} else {
			dir := filepath.Dir(binDir)
			out = append(out, RowView{
				Version: sysVer,
				Source:  sysSrc,
				Size:    "-",
				// BinDir 的契约是「junction 应指向的目录」（见 core.Adapter），
				// 探测给出的是 exe 全路径，这里取所在目录，避免被当作切换目标。
				RootDir: dir,
				BinDir:  dir,
				Active:  cur == nil,
			})
		}
	}
	return out, nil
}

// Location 该工具"装在哪、怎么进环境"的只读快照：版本库根、各版本目录规则、
// 激活链接、PATH 注册与相关环境变量。前端详情页据此展示位置与环境配置。
func (a *App) Location(tool string) (core.ToolLocation, error) {
	ad := tools.ByID(tool)
	if ad == nil {
		return core.ToolLocation{}, core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	return core.Locate(ad, a.s), nil
}

func (a *App) Remote(tool string) ([]RemoteRow, error) {
	ad := tools.ByID(tool)
	if ad == nil {
		return nil, core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	vers, stale, err := core.RemoteCached(ctx, ad, a.s)
	if err != nil {
		return nil, err
	}
	_ = stale
	installs := core.ListInstalls(ad, a.s)
	have := map[string]bool{}
	for _, iv := range installs {
		have[iv.Version] = true
	}
	out := make([]RemoteRow, 0, len(vers))
	for _, rv := range vers {
		out = append(out, RemoteRow{
			Version: rv.Version, Label: rv.Label, Date: rv.Date,
			Size: core.HumanSize(rv.Size), Installed: have[rv.Version],
		})
	}
	return out, nil
}

func (a *App) Doctor() []CheckView {
	cs := core.Doctor(a.ads, a.s)
	out := make([]CheckView, 0, len(cs))
	for _, c := range cs {
		out = append(out, CheckView(c))
	}
	return out
}

func (a *App) Settings() SettingsView {
	sv := SettingsView{
		Autostart:          a.s.Autostart,
		CloseToTray:        a.s.CloseToTray,
		AutoCheckUpdate:    a.s.AutoCheckUpdate,
		CheckUpdateOnStart: a.s.CheckUpdateOnStart,
		StorePath:          settings.StoreDir(a.s),
		Mirrors:            a.s.Mirrors,
		Proxy:              a.s.Proxy,
		IndexCacheMinutes:  a.s.IndexCacheMinutes,
		Version:            BuildVersion(),
		RemoteState:        map[string]string{},
		TotalSize:          core.HumanSize(core.DirSize(settings.StoreDir(a.s))),
		ManagedEnv:         core.ManagedEnvSnapshot(a.s),
	}
	// 迁移确认要告诉用户"会搬多少个版本"：只数 store 自有版本。
	// nvm 收编目录不在版本库里，也不会被迁移，算进去会虚高。
	for _, ad := range a.ads {
		sv.RemoteState[ad.ID()] = remoteCacheState(a.s, ad.ID())
		for _, iv := range core.ListInstallsFast(ad, a.s) {
			if iv.Source == "store" {
				sv.InstalledCount++
			}
		}
	}
	return sv
}

// —— 动作接口 ——

func (a *App) Use(tool, version string) error {
	if a.storeMoving.Load() {
		return core.W(core.ErrUsage, "版本库正在迁移，完成后再切换")
	}
	if !a.opsMu.TryRLock() {
		return core.W(core.ErrUsage, "另一个版本库操作正在进行，请稍候再切换")
	}
	defer a.opsMu.RUnlock()
	ad := tools.ByID(tool)
	if ad == nil {
		return core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	if _, _, err := core.UseByName(a.s, ad, version, true); err != nil {
		return err
	}
	// 托管变量跟的是版本根，切换后必须重写；失败要让用户看见，不能静默
	if _, err := core.SyncManagedEnv(a.s, a.ads); err != nil {
		return fmt.Errorf("已切到 %s，但托管环境变量同步失败（新开的终端可能仍指向旧版本）：%v", version, err)
	}
	sysDetectCacheMu.Lock()
	delete(sysDetectCache, tool)
	sysDetectCacheMu.Unlock()
	a.emitStatus()
	return nil
}

// Install 异步启动安装，进度走 event:install。同工具同时只允许一个任务。
func (a *App) Install(tool, version string) error {
	if a.storeMoving.Load() {
		return core.W(core.ErrUsage, "版本库正在迁移，完成后再安装")
	}
	if !a.opsMu.TryRLock() {
		return core.W(core.ErrUsage, "另一个版本库操作正在进行，请稍候再安装")
	}
	// 安装是异步的：锁要跨 goroutine 持有，直到整条流水线结束。
	// 用 locked 标记，提前返回时由 defer 释放，正常启动后交给 goroutine。
	locked := true
	defer func() {
		if locked {
			a.opsMu.RUnlock()
		}
	}()

	ad := tools.ByID(tool)
	if ad == nil {
		return core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	a.mu.Lock()
	if _, running := a.busy[tool]; running {
		a.mu.Unlock()
		return core.W(core.ErrUsage, "%s 已有任务进行中", tool)
	}
	cctx, cancel := context.WithCancel(a.ctx)
	a.busy[tool] = cancel
	a.mu.Unlock()

	go func() {
		defer a.opsMu.RUnlock()
		defer func() {
			a.mu.Lock()
			delete(a.busy, tool)
			a.mu.Unlock()
			cancel()
		}()
		a.runInstall(cctx, ad, version)
	}()
	locked = false
	return nil
}

func (a *App) runInstall(ctx context.Context, ad core.Adapter, version string) {
	tool := ad.ID()
	ev := func(p ProgressEvent) {
		p.Tool = tool
		if a.ctx != nil {
			wrt.EventsEmit(a.ctx, "install", p)
		}
	}
	target, _, err := core.ResolveRemote(ctx, a.s, ad, version)
	if err != nil {
		ev(ProgressEvent{Stage: "error", Version: version, Message: err.Error()})
		return
	}
	want := target.Version
	ev(ProgressEvent{Stage: "download", Version: want, Message: "开始下载"})
	prog := func(done, total int64, speed float64) {
		pct := 0.0
		if total > 0 {
			pct = float64(done) / float64(total) * 100
		}
		ev(ProgressEvent{Stage: "download", Version: want, Pct: pct,
			Speed: core.HumanSize(int64(speed)) + "/s", Message: core.HumanSize(done)})
	}
	if err := core.InstallPipeline(ctx, a.s, ad, *target, prog); err != nil {
		stage := "error"
		if ctx.Err() != nil {
			stage = "cancelled"
		}
		ev(ProgressEvent{Stage: stage, Version: want, Message: err.Error()})
		return
	}
	// 当前无激活版本 → 自动切上
	if cur, _, _ := core.CurrentInstall(ad, a.s); cur == nil {
		if list := core.ListInstalls(ad, a.s); len(list) > 0 {
			for i := range list {
				if list[i].Version == want {
					_ = core.Switch(a.s, ad, &list[i])
					_, _ = core.EnsurePATH(a.s)
					break
				}
			}
		}
	}
	ev(ProgressEvent{Stage: "done", Version: want, Pct: 100, Message: "安装完成，冒烟通过"})
	a.emitStatus()
}

func (a *App) CancelInstall(tool string) {
	a.mu.Lock()
	if c, ok := a.busy[tool]; ok {
		c()
	}
	a.mu.Unlock()
}

func (a *App) Remove(tool, version string, cascade bool) error {
	if a.storeMoving.Load() {
		return core.W(core.ErrUsage, "版本库正在迁移，完成后再卸载")
	}
	if !a.opsMu.TryRLock() {
		return core.W(core.ErrUsage, "另一个版本库操作正在进行，请稍候再卸载")
	}
	defer a.opsMu.RUnlock()
	ad := tools.ByID(tool)
	if ad == nil {
		return core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	err := core.RemoveVersion(a.s, ad, version, cascade)
	// 只有目录真的删掉了才同步托管变量；否则会用一个次要错误盖住
	// "版本不存在/正在使用"这类真正需要用户处理的错误。
	if err == nil || strings.Contains(err.Error(), "已自动切到") {
		if _, e := core.SyncManagedEnv(a.s, a.ads); e != nil {
			return fmt.Errorf("版本已处理，但托管环境变量恢复失败：%v", e)
		}
	}
	a.emitStatus()
	if err != nil && !strings.Contains(err.Error(), "已自动切到") {
		return err
	}
	// 走到这里说明目录已真删（“已自动切到”是成功路径上的提示）。
	// 必须清掉系统版本探测缓存，否则 60 秒内本机列表仍会把刚删掉的版本
	// 当成“系统环境”行重新显示出来（nvm 收编版本尤其容易命中）。
	sysDetectCacheMu.Lock()
	delete(sysDetectCache, tool)
	sysDetectCacheMu.Unlock()
	return nil
}

func (a *App) SetAutostart(on bool) error {
	if !a.opsMu.TryLock() {
		return core.W(core.ErrUsage, "版本库操作进行中，请稍候再改设置")
	}
	defer a.opsMu.Unlock()
	if on {
		exe := selfGuiPath()
		if exe == "" {
			return core.W(core.ErrNotFound, "未找到 EnvSwitch.exe，无法开启自启")
		}
		if err := sys.SetAutostart("EnvSwitch", `"`+exe+`" --tray`); err != nil {
			return err
		}
	} else if err := sys.RemoveAutostart("EnvSwitch"); err != nil {
		return err
	}
	a.s.Autostart = on
	return a.s.Save()
}

func (a *App) SetField(key, value string) error {
	// 设置落盘会 marshal 整个 Settings（含 EnvBackup map）。与托管/迁移的
	// 修改并发会触发 concurrent map read/write，所以这里用写锁串行化。
	if !a.opsMu.TryLock() {
		return core.W(core.ErrUsage, "版本库操作进行中，请稍候再改设置")
	}
	defer a.opsMu.Unlock()
	switch {
	case strings.HasPrefix(key, "mirror."):
		// copy-on-write：Settings() 会把 Mirrors 引用交给 Wails 异步序列化，
		// 原地写 map 会与序列化并发触发 concurrent map read/write。
		next := make(map[string]string, len(a.s.Mirrors)+1)
		for k, v := range a.s.Mirrors {
			next[k] = v
		}
		next[strings.TrimPrefix(key, "mirror.")] = value
		a.s.Mirrors = next
	case key == "proxy":
		a.s.Proxy = value
	case key == "closeToTray":
		a.s.CloseToTray = value == "true"
	case key == "autoCheckUpdate":
		a.s.AutoCheckUpdate = value == "true"
		a.s.CheckUpdateOnStart = a.s.AutoCheckUpdate
		if a.s.AutoCheckUpdate {
			a.startUpdateScheduler()
		} else {
			a.stopUpdateScheduler()
		}
	case key == "checkUpdateOnStart":
		a.s.AutoCheckUpdate = value == "true"
		a.s.CheckUpdateOnStart = a.s.AutoCheckUpdate
		if a.s.AutoCheckUpdate {
			a.startUpdateScheduler()
		} else {
			a.stopUpdateScheduler()
		}
	case key == "indexCacheMinutes":
		var n int
		fmt.Sscanf(value, "%d", &n)
		if n >= 1 && n <= 1440 {
			a.s.IndexCacheMinutes = n
		}
	default:
		return core.W(core.ErrUsage, "未知配置键 %q", key)
	}
	return a.s.Save()
}

// RefreshIndex 清某工具缓存并强制重拉，返回新版本数。
func (a *App) RefreshIndex(tool string) (int, error) {
	ad := tools.ByID(tool)
	if ad == nil {
		return 0, core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	n, err := core.ForceRefresh(ad, a.s)
	if err != nil {
		return 0, err
	}
	a.emitStatus()
	return n, nil
}

func (a *App) OpenStoreDir() {
	dir := settings.StoreDir(a.s)
	// 全新环境里版本库目录可能还没建，先补上再打开
	_ = os.MkdirAll(dir, 0o755)
	_ = a.OpenDir(dir)
}

// OpenDir 在资源管理器打开一个受管目录。只允许 EnvSwitch 自己的
// home / 版本库 / 程序目录范围，避免前端把任意路径丢进来当文件浏览器。
func (a *App) OpenDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return core.W(core.ErrUsage, "目录为空")
	}
	if strings.ContainsAny(path, "<>") {
		return core.W(core.ErrUsage, "这是目录模板，不是真实路径：%s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return core.W(core.ErrUsage, "路径无法解析：%v", err)
	}
	abs = filepath.Clean(abs)
	if !withinManaged(abs, a.s) {
		return core.W(core.ErrUsage, "只允许打开 EnvSwitch 受管目录：%s", abs)
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return core.W(core.ErrNotFound, "目录不存在：%s", abs)
	}
	if a.ctx != nil {
		u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(abs)}
		wrt.BrowserOpenURL(a.ctx, u.String())
	}
	return nil
}

// —— 版本库迁移与环境变量托管 ——

type StoreMoveEvent struct {
	Stage   string  `json:"stage"` // scan / move / copy / verify / links / cleanup / done / error
	Pct     float64 `json:"pct"`
	Message string  `json:"message"`
}

// ChangeStore 整体迁移版本库到新目录（不是逐版本自定义：散落的目录无法扫描、
// 无法统计、卸载时还可能删到用户自己的文件）。
func (a *App) ChangeStore(path string) error {
	if !a.storeMoving.CompareAndSwap(false, true) {
		return core.W(core.ErrUsage, "版本库正在迁移中，请稍候")
	}
	defer a.storeMoving.Store(false)

	a.mu.Lock()
	busy := len(a.busy) > 0
	a.mu.Unlock()
	if busy {
		return core.W(core.ErrUsage, "有安装任务正在进行，完成后再迁移版本库")
	}
	if !a.opsMu.TryLock() {
		return core.W(core.ErrUsage, "有切换/卸载/环境变量操作正在进行，完成后再迁移版本库")
	}
	defer a.opsMu.Unlock()

	emit := func(stage string, pct float64, msg string) {
		if a.ctx != nil {
			wrt.EventsEmit(a.ctx, "store-migrate", StoreMoveEvent{Stage: stage, Pct: pct, Message: msg})
		}
	}
	if err := core.MoveStore(context.Background(), a.s, a.ads, path, emit); err != nil {
		emit("error", 0, err.Error())
		// 可能只差最后一步（如旧目录被占用没删掉）：配置已经指向新根，
		// 这种"部分成功"也必须刷新界面，否则用户看到的是迁移前的状态。
		if storeRootChanged(a.s, path) {
			a.clearSysDetectCache()
			a.emitStatus()
		}
		return err
	}
	a.clearSysDetectCache()
	a.emitStatus()
	// 托管变量写的是"版本根绝对路径"，迁移后必须重写到新根
	if _, err := core.SyncManagedEnv(a.s, a.ads); err != nil {
		return fmt.Errorf("版本库已迁移，但托管环境变量同步失败：%v", err)
	}
	return nil
}

func (a *App) clearSysDetectCache() {
	sysDetectCacheMu.Lock()
	sysDetectCache = map[string]sysDetectCacheItem{}
	sysDetectCacheMu.Unlock()
}

// storeRootChanged 判断迁移是否已经把配置切到目标根：用于区分"完全失败"与
// "文件已就位、收尾失败"。
func storeRootChanged(s *settings.Settings, want string) bool {
	abs, err := filepath.Abs(want)
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(settings.StoreDir(s)), filepath.Clean(abs))
}

// PickDirectory 打开系统目录选择器；用户取消时返回空串与 nil。
func (a *App) PickDirectory(title, defaultDir string) (string, error) {
	if a.ctx == nil {
		return "", core.W(core.ErrUsage, "窗口尚未就绪")
	}
	if strings.TrimSpace(title) == "" {
		title = "选择目录"
	}
	// Wails 对不存在的 DefaultDirectory 会直接报错；全新机器上 home\store
	// 可能还没创建，导致"更改位置"点了没反应。这里退化为不指定默认目录。
	if defaultDir != "" {
		if fi, err := os.Stat(defaultDir); err != nil || !fi.IsDir() {
			defaultDir = ""
		}
	}
	dir, err := wrt.OpenDirectoryDialog(a.ctx, wrt.OpenDialogOptions{
		Title:            title,
		DefaultDirectory: defaultDir,
	})
	if err != nil {
		return "", err
	}
	return dir, nil
}

// SetEnvManaged 开启/关闭某个用户环境变量的托管。
func (a *App) SetEnvManaged(key string, on bool) error {
	if a.storeMoving.Load() {
		return core.W(core.ErrUsage, "版本库正在迁移，完成后再修改环境变量")
	}
	if !a.opsMu.TryRLock() {
		return core.W(core.ErrUsage, "另一个版本库操作正在进行，请稍候")
	}
	defer a.opsMu.RUnlock()
	if err := core.SetManagedEnv(a.s, a.ads, key, on); err != nil {
		return err
	}
	a.emitStatus()
	return nil
}

func withinManaged(p string, s *settings.Settings) bool {
	for _, root := range []string{settings.Home(), settings.StoreDir(s), settings.ExeDir()} {
		r := filepath.Clean(root)
		if strings.EqualFold(p, r) {
			return true
		}
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(r)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// TrayVersions 托盘菜单数据：只列可切换的版本（store / nvm 收编），不算目录大小。
// 系统检测出来的版本不在版本库里，点它无法切换，列出来只会造成"点了没反应"。
func (a *App) TrayVersions(tool string) []RowView {
	ad := tools.ByID(tool)
	if ad == nil {
		return nil
	}
	cur, _, _ := core.CurrentInstall(ad, a.s)
	installs := core.ListInstallsFast(ad, a.s)
	out := make([]RowView, 0, len(installs))
	for _, iv := range installs {
		out = append(out, RowView{
			Version: iv.Version, Source: iv.Source, Size: "-",
			BinDir: iv.BinDir, Active: cur != nil && cur.Version == iv.Version,
		})
	}
	return out
}

func (a *App) emitStatus() {
	if a.ctx != nil {
		wrt.EventsEmit(a.ctx, "status", a.Overview())
	}
}

// —— 生命周期与托盘入口辅助 ——

type UpdateInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	Size      string `json:"size"`
	Error     string `json:"error"`
}

var lastUpdateCheck UpdateInfo

// CheckUpdateNow 拉清单比对版本；结果同时以事件推给前端横幅。
func (a *App) CheckUpdateNow() UpdateInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	m, asset, err := core.CheckUpdate(ctx, a.s.Proxy, BuildVersion())
	var info UpdateInfo
	if err != nil {
		info = UpdateInfo{Error: err.Error()}
	} else if m == nil {
		info = UpdateInfo{Available: false}
	} else {
		info = UpdateInfo{Available: true, Version: m.Version, Notes: m.Notes, Size: core.HumanSize(asset.Size)}
	}
	a.updateMu.Lock()
	lastUpdateCheck = info
	a.updateMu.Unlock()
	if a.ctx != nil {
		wrt.EventsEmit(a.ctx, "update", info)
	}
	return info
}

// PendingUpdate 前端启动时取最近一次检查结果（异步检查完成后走事件）。
func (a *App) PendingUpdate() UpdateInfo {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return lastUpdateCheck
}

// ApplyUpdate 下载 → 校验解包 → 自替换 → 带 --updated 重启 → 退出本进程。
// 全程以 tool=self 的 install 事件推「整条流程」的进度（不是单纯下载量）：
// download 0~85 → unpack 90 → apply 96 → restart 98 → done 100，任一步失败推 error。
func (a *App) ApplyUpdate() error {
	if !a.selfApplying.CompareAndSwap(false, true) {
		return fmt.Errorf("EnvSwitch 正在更新中，请稍候")
	}
	restartOK := false
	defer func() {
		if !restartOK {
			a.selfApplying.Store(false)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ev := func(p ProgressEvent) {
		p.Tool = "self"
		if a.ctx != nil {
			wrt.EventsEmit(a.ctx, "install", p)
		}
	}
	fail := func(version string, err error) error {
		ev(ProgressEvent{Stage: "error", Version: version, Message: err.Error()})
		return err
	}

	cur := BuildVersion()
	m, asset, err := core.CheckUpdate(ctx, a.s.Proxy, cur)
	if err != nil {
		return fail(cur, err)
	}
	if m == nil {
		return fail(cur, core.W(core.ErrNotFound, "当前已是最新版本"))
	}
	su := core.NewSelfUpdate(a.s)
	su.OnStage = func(stage, msg string) {
		pct := 90.0
		if stage == "apply" {
			pct = 96
		}
		ev(ProgressEvent{Stage: stage, Version: m.Version, Pct: pct, Message: msg})
	}
	ev(ProgressEvent{Stage: "download", Version: m.Version, Message: "开始下载更新包"})
	files, err := su.Download(ctx, asset, func(done, total int64, speed float64) {
		pct := 0.0
		if total > 0 {
			pct = float64(done) / float64(total) * 100
		}
		msg := "正在下载更新包"
		if total > 0 {
			msg = fmt.Sprintf("正在下载更新包 %s / %s", core.HumanSize(done), core.HumanSize(total))
		}
		ev(ProgressEvent{
			Stage:   "download",
			Version: m.Version,
			Pct:     pct * 0.85, // 下载只占整条流程的 85%，剩下的留给解包与替换
			Speed:   core.HumanSize(int64(speed)) + "/s",
			Message: msg,
		})
	})
	if err != nil {
		return fail(m.Version, err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(m.Version, err)
	}
	if err := su.Apply(files, exe); err != nil {
		return fail(m.Version, err)
	}
	ev(ProgressEvent{Stage: "restart", Version: m.Version, Pct: 98, Message: "替换完成，正在重启…"})
	cmd := exec.Command(exe, "--updated")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
	if err := cmd.Start(); err != nil {
		return fail(m.Version, fmt.Errorf("新版本已就位但启动失败，请手动打开 EnvSwitch: %w", err))
	}
	ev(ProgressEvent{Stage: "done", Version: m.Version, Pct: 100, Message: "更新完成，正在重启 EnvSwitch…"})
	// 重启已发出：不再释放旗标，本进程马上退出，重复点击没有意义。
	restartOK = true
	// 必须标记真退：Quit() 同样走 OnBeforeClose，若不置旗标，closeToTray=true
	// 时旧进程只隐藏不退，新进程又拿不到单实例锁，热更新会静默失败甚至回滚。
	a.DisableTrayKeep()
	go func() { time.Sleep(300 * time.Millisecond); wrt.Quit(a.ctx) }()
	return nil
}

func (a *App) Ctx() context.Context { return a.ctx }

// CloseToTray 读取「关闭时最小化到托盘」设置；退出流程中 quitting
// 一次性旗标覆盖它，保证真退且不污染持久设置。
func (a *App) CloseToTray() bool { return a.s.CloseToTray && !a.quitting.Load() }

// CloseWindow 供标题栏关闭按钮调用。wails 前端 runtime 没有 WindowClose
// （window.js 只暴露 Hide/Show/Minimise/Maximise），JS 直调是 undefined，
// 静默报错、点了没反应。这里从 Go 侧走 Quit()：与托盘「退出」、热更新重启
// 同一条路——OnBeforeClose 里 closeToTray=true 则隐藏到托盘，false 则真退。
// 不用 wrt.WindowClose：它直接 DestroyWindow，绕过 OnBeforeClose，
// 会把「关闭到托盘」变成无托盘可回的真退。
func (a *App) CloseWindow() {
	if a.ctx != nil {
		wrt.Quit(a.ctx)
	}
}

func (a *App) Autostart() bool { return a.s.Autostart }

// DisableTrayKeep 用于托盘"退出"与热更新重启：仅在本次进程把关闭标记为
// 真退（内存旗标），不改动 closeToTray 设置——旧实现把 false 写盘，导致
// 托盘退出一次后，下次启动点 × 就永久变成直接关程序。
func (a *App) DisableTrayKeep() { a.quitting.Store(true) }

// NotifyUpdateAvailable 预留给 M4（更新可用才弹系统通知）。
func (a *App) NotifyUpdateAvailable() {}

func (a *App) JumpToTool(tool string) {
	if a.ctx != nil {
		wrt.EventsEmit(a.ctx, "navigate", tool)
	}
}

// —— helpers ——

// remoteCacheState：fresh=有缓存 / pending=无缓存未拉取。
func remoteCacheState(s *settings.Settings, tool string) string {
	if _, err := os.Stat(filepath.Join(settings.CacheDir(s), tool+".json")); err == nil {
		return "fresh"
	}
	return "pending"
}

func selfGuiPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	name := strings.ToLower(filepath.Base(exe))
	if name == "envswitch.exe" {
		return exe
	}
	sib := filepath.Join(filepath.Dir(exe), "EnvSwitch.exe")
	if _, err := os.Stat(sib); err == nil {
		return sib
	}
	return ""
}

// BuildVersion 由 core 统一持有（CLI/GUI 共享，见 -ldflags 注入）。
func BuildVersion() string { return core.BuildVersion() }

// —— 网络代理接口 (参考 AgentHub 设计) ——

func (a *App) NetProxyList() netproxy.NetProxyConfig {
	return netproxy.GetManager().GetConfig()
}

func (a *App) NetProxySave(draft netproxy.NetProxyItem) (netproxy.NetProxyItem, error) {
	return netproxy.GetManager().SaveItem(draft)
}

func (a *App) NetProxyRemove(id string) error {
	return netproxy.GetManager().RemoveItem(id)
}

func (a *App) NetProxyToggle(id string, enabled bool) (netproxy.NetProxyItem, error) {
	return netproxy.GetManager().ToggleItem(id, enabled)
}

func (a *App) NetProxySaveSettings(req netproxy.SaveSettingsReq) (netproxy.NetProxyConfig, error) {
	return netproxy.GetManager().SaveSettings(req)
}

func (a *App) NetProxyTest(draft netproxy.NetProxyItem) (netproxy.NetProxyTest, error) {
	return netproxy.GetManager().TestProxy(draft)
}

func (a *App) NetProxyClashSync() (netproxy.ClashSyncResult, error) {
	return netproxy.GetManager().SyncClash()
}
