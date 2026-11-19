// Package app 是绑定层：把 core 的能力整形成 GUI 可直接消费的 DTO 与动作，
// 不实现任何业务规则（规则全部在 core）。
package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	wrt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/HUIdada1/envswitch/internal/core"
	"github.com/HUIdada1/envswitch/internal/netproxy"
	"github.com/HUIdada1/envswitch/internal/settings"
	"github.com/HUIdada1/envswitch/internal/sys"
	"github.com/HUIdada1/envswitch/internal/tools"
)

// App 单实例服务。binds 方法名即前端 window.go.app.App.<Method>。
type App struct {
	ctx          context.Context
	mu           sync.Mutex
	updateMu     sync.Mutex // 保护 lastUpdateCheck（后台定时检查与前端查询并发）
	s            *settings.Settings
	ads          []core.Adapter
	busy         map[string]context.CancelFunc // tool -> 进行中的安装任务（同工具串行）
	updateStopCh chan struct{}
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
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Kind      string        `json:"kind"` // runtime | js
	Group     string        `json:"group"`
	Current   *InstallView  `json:"current"`
	Lamp      string        `json:"lamp"` // ok / idle / up / off / err —— 设计语言§6 状态灯语义
	Remote    string        `json:"remoteState"` // fresh / stale / pending / error
	Installed []string      `json:"installed"`   // 本机可用版本列表（快捷切换）
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
	BinDir  string `json:"binDir"`
	Active  bool   `json:"active"`
}

type RemoteRow struct {
	Version string `json:"version"`
	Label   string `json:"label"`
	Date    string `json:"date"`
	Size    string `json:"size"`
	Installed bool  `json:"installed"`
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
		active := cur != nil && cur.Version == iv.Version
		out = append(out, RowView{
			Version: iv.Version, Source: iv.Source, Size: core.HumanSize(iv.Size),
			BinDir: iv.BinDir, Active: active,
		})
	}
	// 系统里实际生效的版本（不在版本库）追加为只读行
	if sysVer, sysSrc, binDir := a.detectActiveSystemVersion(ad); sysVer != "" {
		out = append(out, RowView{
			Version: sysVer,
			Source:  sysSrc,
			Size:    "-",
			BinDir:  binDir,
			Active:  cur == nil,
		})
	}
	return out, nil
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
	}
	for _, ad := range a.ads {
		sv.RemoteState[ad.ID()] = remoteCacheState(a.s, ad.ID())
	}
	return sv
}

// —— 动作接口 ——

func (a *App) Use(tool, version string) error {
	ad := tools.ByID(tool)
	if ad == nil {
		return core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	if _, _, err := core.UseByName(a.s, ad, version, true); err != nil {
		return err
	}
	sysDetectCacheMu.Lock()
	delete(sysDetectCache, tool)
	sysDetectCacheMu.Unlock()
	a.emitStatus()
	return nil
}

// Install 异步启动安装，进度走 event:install。同工具同时只允许一个任务。
func (a *App) Install(tool, version string) error {
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
		defer func() {
			a.mu.Lock()
			delete(a.busy, tool)
			a.mu.Unlock()
			cancel()
		}()
		a.runInstall(cctx, ad, version)
	}()
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
	ad := tools.ByID(tool)
	if ad == nil {
		return core.W(core.ErrUsage, "未知工具 %s", tool)
	}
	err := core.RemoveVersion(a.s, ad, version, cascade)
	a.emitStatus()
	if err != nil {
		// “已自动切到”是成功路径上的提示
		if strings.Contains(err.Error(), "已自动切到") {
			return nil
		}
		return err
	}
	return nil
}

func (a *App) SetAutostart(on bool) error {
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
	switch {
	case strings.HasPrefix(key, "mirror."):
		a.s.Mirrors[strings.TrimPrefix(key, "mirror.")] = value
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
	if a.ctx != nil {
		wrt.BrowserOpenURL(a.ctx, "file:///"+strings.ReplaceAll(settings.StoreDir(a.s), `\`, `/`))
	}
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

// ApplyUpdate 下载 → 校验 → 自替换 → 带 --updated 重启 → 退出本进程。
func (a *App) ApplyUpdate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	m, asset, err := core.CheckUpdate(ctx, a.s.Proxy, BuildVersion())
	if err != nil {
		return err
	}
	if m == nil {
		return core.W(core.ErrNotFound, "当前已是最新版本")
	}
	su := core.NewSelfUpdate(a.s)
	ev := func(p ProgressEvent) {
		p.Tool = "self"
		wrt.EventsEmit(a.ctx, "install", p)
	}
	files, err := su.Download(ctx, asset, func(done, total int64, speed float64) {
		pct := 0.0
		if total > 0 {
			pct = float64(done) / float64(total) * 100
		}
		ev(ProgressEvent{Stage: "download", Version: m.Version, Pct: pct, Speed: core.HumanSize(int64(speed)) + "/s"})
	})
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := su.Apply(files, exe); err != nil {
		return err
	}
	cmd := exec.Command(exe, "--updated")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { time.Sleep(300 * time.Millisecond); wrt.Quit(a.ctx) }()
	return nil
}

func (a *App) Ctx() context.Context { return a.ctx }

func (a *App) CloseToTray() bool { return a.s.CloseToTray }

func (a *App) Autostart() bool { return a.s.Autostart }

// DisableTrayKeep 用于托盘"退出"：本次关闭必须真退，忽略 closeToTray。
func (a *App) DisableTrayKeep() {
	a.s.CloseToTray = false
	_ = a.s.Save()
}

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
