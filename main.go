// main.go —— EnvSwitch GUI 入口。窗口与托盘是两个独立线程环：
// wails 管渲染与绑定，托盘原生实现管动态快捷菜单。--tray 静默启动（开机自启）。
// 启动顺序固定：单实例锁 → 崩溃回滚检查 → heartbeat 写入 → 托盘 → wails 窗口。
// 锁在最前：只有独占本进程之后，才允许动 exe / .old / heartbeat。
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wrt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/MUHUIX/envswitch/internal/app"
	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200} // DETACHED | NEW_PROCESS_GROUP
}

//go:embed all:frontend/dist
var assets embed.FS

// 托盘菜单 ID：固定动作 1–9；工具区从 100 起，每工具 100 个号段（组头 + 版本行）。
// 早期固定动作占了 200 段，导致第二个工具（python）的版本项与「开机自启」等撞号。
const (
	idOpenMain    uint16 = 1
	idToggleAuto  uint16 = 2
	idCheckUpdate uint16 = 3
	idQuit        uint16 = 4
	toolBase      uint16 = 100
	maxPerTool           = 40
)

// 单实例互斥量名与重启侧的等锁上限。15s 足够覆盖旧进程收尾（消息泵退出 + 托盘线程
// 拆除 + 内核释放句柄通常在 2s 内）；再久就是旧进程真卡死了，继续等不如留现场报错。
const (
	instanceName = "EnvSwitch.SingleInstance"
	relaunchWait = 15 * time.Second
)

func main() {
	startTrayOnly := flag.Bool("tray", false, "静默启动到托盘（开机自启用）")
	updated := flag.Bool("updated", false, "内部参数：热更新后的首次启动")
	waitInstance := flag.Bool("wait-instance", false, "内部参数：等上一个实例退场后再起（重启与回滚拉起用）")
	flag.Parse()

	exe, _ := os.Executable()
	su := core.NewSelfUpdate(mustSettings())

	// 先独占，再谈更新：回滚与替换都要动正在运行的 exe，另一个实例还活着时动手，
	// 改的是别人的映像文件。热更新/回滚拉起的进程一律走等锁——派生它的上一进程
	// 此刻多半还没退干净，抢一次锁就自杀等于把新版本原地判死刑。
	if *updated || *waitInstance {
		if err := sys.AcquireSingleInstanceWait(instanceName, relaunchWait); err != nil {
			noteRelaunchFailure(exe, err)
			os.Exit(0)
		}
	} else if err := sys.AcquireSingleInstance(instanceName); err != nil {
		os.Exit(0) // 已有实例
	}

	handleUpdateLifecycle(su, exe, *updated)

	a := app.New()
	tray := sys.StartTray("EnvSwitch · 版本切换台",
		func() []sys.MenuItem { return buildTrayMenu(a) },
		func(id uint16) { handleTrayPick(a, id) },
		func() { showMainWindow(a) })
	tray.WaitReady()

	// wails.Run 必须跑在主 goroutine：Windows 消息泵与 WebView2 COM 回调依赖 OS 线程
	// 亲和性（main 已被 user32 包 init 的 LockOSThread 钉死）。放普通 goroutine 会被
	// 调度器在启动中途迁移到其他线程，COM 回调投进被废弃的线程队列，DomReady 永不触发、
	// 窗口永久隐藏——约 1/6 概率间歇复现。
	runWindow(a, *startTrayOnly)
	// 事件循环结束（用户退出/致命错误）即终结进程，托盘线程随之退出
	os.Exit(0)
}

func mustSettings() *settings.Settings {
	s, err := settings.Load()
	if err != nil {
		return &settings.Settings{Version: 1, IndexCacheMinutes: 10, Mirrors: map[string]string{}}
	}
	return s
}

// handleUpdateLifecycle 三态（调用方已持有单实例锁）：
// 1 --updated：写 heartbeat，5s 后确认存活则清 .old（更新成功定稿）；
// 2 启动即发现 .old 且无新鲜 heartbeat：上次更新崩溃，换回旧版并重启；
// 3 存在 .old 但 heartbeat 显示上次已确认：残留清理。
func handleUpdateLifecycle(su *core.SelfUpdate, exe string, updatedFlag bool) {
	if exe == "" {
		return
	}
	if updatedFlag {
		// heartbeat 只能在拿到锁之后写（调用点已保证）。进门就写的旧实现，让
		// 「起了进程、抢锁失败自杀」也留下一枚成功凭据：十分钟内看着像更新成功、
		// 不回滚，十分钟后凭据一过期又被判成崩溃、把已经就位的新版本退回去——
		// 两个方向都判反，用户只看到「程序关了没装上」。
		_ = su.WriteHeartbeat()
		go func() {
			time.Sleep(5 * time.Second)
			su.MarkGood()
			os.Remove(exe + ".old")
			os.Remove(exe + ".broken") // 更早的崩溃残骸，顺手自愈
		}()
		return
	}
	if su.RollbackPending(exe) {
		if err := su.DoRollback(exe); err == nil {
			// 必须带 --wait-instance：本进程要到 os.Exit 才放锁，孩子直接抢会被
			// 弹死，回滚后照样起不来。
			c := exec.Command(exe, "--wait-instance")
			c.SysProcAttr = detachedAttr()
			_ = c.Start()
			os.Exit(0)
		}
		// 回滚失败：保留现场，正常带旧版继续跑
	}
	// 无 .old = 上一轮早已定稿或已回滚，历史 heartbeat 是陈旧文件
	if _, err := os.Stat(exe + ".old"); err != nil {
		su.MarkGood()
	}
}

// noteRelaunchFailure 等锁超时：本构建是 -H=windowsgui，没有控制台，不落盘就只剩
// 「双击没反应」一种现场。写进更新目录，和 package.zip 放一起好找。
func noteRelaunchFailure(exe string, err error) {
	dir := filepath.Join(settings.Home(), "update")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return
	}
	f, openErr := os.OpenFile(filepath.Join(dir, "relaunch-error.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if openErr != nil {
		return
	}
	fmt.Fprintf(f, "%s 新版本已就位但没拿到单实例锁（上一进程未退出）: %v\nexe=%s\n",
		time.Now().Format(time.RFC3339), err, exe)
	_ = f.Close()
}

func runWindow(a *app.App, hidden bool) {
	err := wails.Run(&options.App{
		Title:            "EnvSwitch",
		Width:            1240,
		Height:           800,
		MinWidth:         1024,
		MinHeight:        680,
		Frameless:        true, // 标题栏由前端自绘（logo/版本/作者/GitHub/窗口三键）
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 13, G: 15, B: 18, A: 1},
		StartHidden:      hidden,
		OnStartup: func(ctx context.Context) {
			a.Startup(ctx)
			mainWindowOpen.Store(!hidden)
		},
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			if a.CloseToTray() {
				wrt.WindowHide(ctx)
				mainWindowOpen.Store(false)
				return true
			}
			return false
		},
		OnDomReady: func(ctx context.Context) {
			if !hidden {
				wrt.WindowShow(ctx)
				mainWindowOpen.Store(true)
			}
		},
		Bind: []interface{}{a},
	})
	if err != nil {
		// windowsgui 构建无控制台，落盘供诊断
		_ = os.WriteFile("startup-error.log", []byte(err.Error()), 0o644)
	}
}

var mainWindowOpen atomic.Bool

func showMainWindow(a *app.App) {
	ctx := a.Ctx()
	if ctx == nil {
		return
	}
	if !mainWindowOpen.Load() {
		wrt.WindowShow(ctx)
		mainWindowOpen.Store(true)
	}
	wrt.WindowUnminimise(ctx)
}

// buildTrayMenu 每个工具一项带二级版本列表（一级 14 行 + 动作区，动态重建）。
func buildTrayMenu(a *app.App) []sys.MenuItem {
	var items []sys.MenuItem
	for ti, tv := range a.Overview() {
		cur := "未激活"
		if tv.Current != nil {
			cur = tv.Current.Version
		}
		base := toolBase + uint16(ti)*100
		var sub []sys.MenuItem
		for ri, row := range a.TrayVersions(tv.ID) {
			if ri >= maxPerTool {
				break
			}
			label := row.Version
			if row.Source != "store" {
				label += "  (" + row.Source + ")"
			}
			sub = append(sub, sys.MenuItem{ID: base + uint16(ri) + 1, Label: label, Checked: row.Active})
		}
		if len(sub) == 0 {
			sub = []sys.MenuItem{{ID: base, Label: "未纳入版本库 · 打开主界面"}}
		} else {
			sub = append(sub, sys.MenuItem{Sep: true},
				sys.MenuItem{ID: base, Label: "管理 " + tv.ID + "…"})
		}
		items = append(items, sys.MenuItem{Label: tv.Title + "  ·  " + cur, Sub: sub})
	}
	items = append(items,
		sys.MenuItem{Sep: true},
		sys.MenuItem{ID: idOpenMain, Label: "打开主界面"},
		sys.MenuItem{ID: idToggleAuto, Label: "开机自启", Checked: a.Autostart()},
		sys.MenuItem{ID: idCheckUpdate, Label: "检查更新"},
		sys.MenuItem{Sep: true},
		sys.MenuItem{ID: idQuit, Label: "退出 EnvSwitch"},
	)
	return items
}

func handleTrayPick(a *app.App, id uint16) {
	switch id {
	case idOpenMain:
		showMainWindow(a)
		return
	case idToggleAuto:
		_ = a.SetAutostart(!a.Autostart())
		return
	case idCheckUpdate:
		a.CheckUpdateNow()
		return
	case idQuit:
		a.DisableTrayKeep()
		wrt.Quit(a.Ctx())
		return
	}
	if id < toolBase {
		return
	}
	n := int(id - toolBase)
	toolIdx := n / 100
	row := n % 100
	tools := a.Overview()
	if toolIdx < 0 || toolIdx >= len(tools) {
		return
	}
	toolID := tools[toolIdx].ID
	if row == 0 { // 管理入口：打开主界面并定位工具
		showMainWindow(a)
		a.JumpToTool(toolID)
		return
	}
	rows := a.TrayVersions(toolID)
	i := row - 1
	if i >= 0 && i < len(rows) && !rows[i].Active {
		_ = a.Use(toolID, rows[i].Version)
	}
}

