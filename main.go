// main.go —— EnvSwitch GUI 入口。窗口与托盘是两个独立线程环：
// wails 管渲染与绑定，托盘原生实现管动态快捷菜单。--tray 静默启动（开机自启）。
// 启动顺序固定：崩溃回滚检查 → heartbeat 写入 → 托盘 → wails 窗口。
package main

import (
	"context"
	"embed"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wrt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/HUIdada1/envswitch/internal/app"
	"github.com/HUIdada1/envswitch/internal/core"
	"github.com/HUIdada1/envswitch/internal/settings"
	"github.com/HUIdada1/envswitch/internal/sys"
)

func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200} // DETACHED | NEW_PROCESS_GROUP
}

//go:embed all:frontend/dist
var assets embed.FS

// 托盘菜单 ID：(工具序号+1)*100+行号；200+ 为固定动作。
const (
	idOpenMain    uint16 = 200
	idToggleAuto  uint16 = 201
	idCheckUpdate uint16 = 202
	idQuit        uint16 = 210
	maxPerTool           = 40
)

func main() {
	startTrayOnly := flag.Bool("tray", false, "静默启动到托盘（开机自启用）")
	updated := flag.Bool("updated", false, "内部参数：热更新后的首次启动")
	flag.Parse()

	exe, _ := os.Executable()
	su := core.NewSelfUpdate(mustSettings())
	handleUpdateLifecycle(su, exe, *updated)

	if err := sys.AcquireSingleInstance("EnvSwitch.SingleInstance"); err != nil {
		os.Exit(0) // 已有实例
	}

	a := app.New()
	tray := sys.StartTray("EnvSwitch · 版本切换台",
		func() []sys.MenuItem { return buildTrayMenu(a) },
		func(id uint16) { handleTrayPick(a, id) },
		func() { showMainWindow(a) })
	tray.WaitReady()

	go runWindow(a, *startTrayOnly)
	select {}
}

func mustSettings() *settings.Settings {
	s, err := settings.Load()
	if err != nil {
		return &settings.Settings{Version: 1, IndexCacheMinutes: 10, Mirrors: map[string]string{}}
	}
	return s
}

// handleUpdateLifecycle 三态：
// 1 --updated：写 heartbeat，5s 后确认存活则清 .old（更新成功定稿）；
// 2 启动即发现 .old 且无新鲜 heartbeat：上次更新崩溃，换回旧版并重启；
// 3 存在 .old 但 heartbeat 显示上次已确认：残留清理。
func handleUpdateLifecycle(su *core.SelfUpdate, exe string, updatedFlag bool) {
	if exe == "" {
		return
	}
	if updatedFlag {
		_ = su.WriteHeartbeat()
		go func() {
			time.Sleep(5 * time.Second)
			su.MarkGood()
			os.Remove(exe + ".old")
		}()
		return
	}
	if su.RollbackPending(exe) {
		if err := su.DoRollback(exe); err == nil {
			c := exec.Command(exe)
			c.SysProcAttr = detachedAttr()
			_ = c.Start()
			os.Exit(0)
		}
		// 回滚失败：保留现场，正常带旧版继续跑
	}
	// 无 .old 时清掉历史 heartbeat（陈旧文件）
	if _, err := os.Stat(exe + ".old"); err != nil {
		os.Remove(filepath.Join(settings.Home(), "update", "heartbeat"))
	}
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
			mainWindowOpen = !hidden
		},
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			if a.CloseToTray() {
				wrt.WindowHide(ctx)
				mainWindowOpen = false
				return true
			}
			return false
		},
		OnDomReady: func(ctx context.Context) {
			if !hidden {
				wrt.WindowShow(ctx)
				mainWindowOpen = true
			}
		},
		Bind: []interface{}{a},
	})
	if err != nil {
		// windowsgui 构建无控制台，落盘供诊断
		_ = os.WriteFile("startup-error.log", []byte(err.Error()), 0o644)
	}
}

var mainWindowOpen bool

func showMainWindow(a *app.App) {
	ctx := a.Ctx()
	if ctx == nil {
		return
	}
	if !mainWindowOpen {
		wrt.WindowShow(ctx)
		mainWindowOpen = true
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
		var sub []sys.MenuItem
		for ri, row := range a.TrayVersions(tv.ID) {
			if ri >= maxPerTool {
				break
			}
			label := row.Version
			if row.Source != "store" {
				label += "  (" + row.Source + ")"
			}
			sub = append(sub, sys.MenuItem{ID: uint16((ti+1)*100 + ri + 1), Label: label, Checked: row.Active})
		}
		if len(sub) == 0 {
			sub = []sys.MenuItem{{ID: uint16((ti + 1) * 100), Label: "未安装 · 打开主界面安装"}}
		} else {
			sub = append(sub, sys.MenuItem{Sep: true},
				sys.MenuItem{ID: uint16((ti + 1) * 100), Label: "管理 " + tv.ID + "…"})
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
	case idToggleAuto:
		_ = a.SetAutostart(!a.Autostart())
	case idCheckUpdate:
		a.CheckUpdateNow()
	case idQuit:
		a.DisableTrayKeep()
		wrt.Quit(a.Ctx())
	default:
		toolIdx := int(id/100) - 1
		tools := a.Overview()
		if toolIdx < 0 || toolIdx >= len(tools) {
			return
		}
		if id%100 == 0 { // 管理入口：打开主界面并定位工具
			showMainWindow(a)
			a.JumpToTool(tools[toolIdx].ID)
			return
		}
		rows := a.TrayVersions(tools[toolIdx].ID)
		i := int(id%100) - 1
		if i < len(rows) && !rows[i].Active {
			_ = a.Use(tools[toolIdx].ID, rows[i].Version)
		}
	}
}

