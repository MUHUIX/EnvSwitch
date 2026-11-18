// Package sys 原生 Win32 托盘：Shell_NotifyIcon + 消息循环 + 动态弹出菜单。
// 不用 wails 自带 SystemTray：其菜单启动后不可重建，而"新装版本立刻出现在
// 托盘"是核心体验。菜单项与动作全部由回调注入，本层零业务。
package sys

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	user32              = syscall.NewLazyDLL("user32.dll")
	shell32             = syscall.NewLazyDLL("shell32.dll")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procExtractIconExW  = shell32.NewProc("ExtractIconExW")
	procRegisterClassW  = user32.NewProc("RegisterClassExW")
	procCreateWndW      = user32.NewProc("CreateWindowExW")
	procDefWndProc      = user32.NewProc("DefWindowProcW")
	procGetMessageW     = user32.NewProc("GetMessageW")
	procTranslateMsg    = user32.NewProc("TranslateMessage")
	procDispatchMsg     = user32.NewProc("DispatchMessageW")
	procPostQuitMsg     = user32.NewProc("PostQuitMessage")
	procGetCursorPos    = user32.NewProc("GetCursorPos")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	procCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	procDestroyMenu     = user32.NewProc("DestroyMenu")
	procAppendMenuW     = user32.NewProc("AppendMenuW")
	procCheckMenuItem   = user32.NewProc("CheckMenuItem")
	procLoadImageW      = user32.NewProc("LoadImageW")
	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
)

const (
	wmTrayIcon    = 0x8001
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B
	wmDestroy     = 0x0002

	nimAdd    = 0
	nimDelete = 2
	nifMsg    = 1
	nifIcon   = 2
	nifTip    = 4

	mfString    = 0x0000
	mfSeparator = 0x0001
	mfDisabled  = 0x0002
	mfChecked   = 0x0008
	mfPopup     = 0x0010
	mfByCommand = 0x0000

	tpmReturnCmd   = 0x0100
	tpmRightButton = 0x0002

	imageIcon  = 1
	lrShared   = 0x8000
	idiAppIcon = 32512
)

// MenuItem 托盘菜单项。ID 由调用方保证唯一，回调按 ID 分发；Sub 非空即弹出二级菜单。
type MenuItem struct {
	ID       uint16
	Label    string
	Checked  bool
	Disabled bool
	Sep      bool
	Sub      []MenuItem
}

// Tray 托盘句柄（隐藏窗口 + 其线程上的消息循环）。
type Tray struct {
	nid    trayData
	onMenu func() []MenuItem
	onPick func(id uint16)
	onLeft func()
	ready  chan struct{}
}

type trayData struct {
	Size            uint32
	Hwnd            uintptr
	UID             uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
}

type wndClassEx struct {
	size          uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// StartTray 后台 goroutine 起托盘线程；返回的 Tray 就绪后可用 WaitReady 同步。
// 窗口必须与其消息循环同线程，故整个生命周期锁死一个 OS 线程。
func StartTray(tip string, onMenu func() []MenuItem, onPick func(uint16), onLeft func()) *Tray {
	t := &Tray{onMenu: onMenu, onPick: onPick, onLeft: onLeft, ready: make(chan struct{})}
	go t.run(tip)
	return t
}

func (t *Tray) WaitReady() { <-t.ready }

func (t *Tray) run(tip string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const className = "EnvSwitchTrayWnd"
	lpClassName, _ := syscall.UTF16PtrFromString(className)
	trayByHwnd = map[uintptr]*Tray{}
	hinst := uintptr(0) // 传 0 即可（hInst=NULL 注册进程类）
	wc := wndClassEx{
		size:          uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(trayWndProc),
		hInstance:     hinst,
		lpszClassName: lpClassName,
	}
	procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := procCreateWndW.Call(0, uintptr(unsafe.Pointer(lpClassName)), 0,
		0, 0, 0, 0, 0, 0, 0, 0)
	trayByHwnd[hwnd] = t

	// 图标：优先从当前 exe 提取第 0 个图标（即包含的 Logo 托盘小图标）
	var icon uintptr
	if exePath, err := os.Executable(); err == nil {
		exePathW, _ := syscall.UTF16PtrFromString(exePath)
		var hSmall uintptr
		r, _, _ := procExtractIconExW.Call(
			uintptr(unsafe.Pointer(exePathW)),
			0, // 索引 0（主图标）
			0, // 不需要大图标
			uintptr(unsafe.Pointer(&hSmall)),
			1,
		)
		if r > 0 && hSmall != 0 {
			icon = hSmall
		}
	}
	if icon == 0 {
		hInst, _, _ := procGetModuleHandleW.Call(0)
		for _, resId := range []uintptr{3, 1, 7} {
			icon, _, _ = procLoadImageW.Call(hInst, resId, imageIcon, 16, 16, lrShared)
			if icon != 0 {
				break
			}
		}
	}
	if icon == 0 {
		icon, _, _ = procLoadImageW.Call(0, idiAppIcon, imageIcon, 0, 0, lrShared)
	}
	tipU, _ := syscall.UTF16FromString(tip)
	t.nid = trayData{
		Size:            uint32(unsafe.Sizeof(trayData{})),
		Hwnd:            hwnd,
		Flags:           nifMsg | nifIcon | nifTip,
		CallbackMessage: wmTrayIcon,
		Icon:            icon,
	}
	copy(t.nid.Tip[:], tipU)
	procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid)))
	close(t.ready)

	var msg struct {
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  uintptr
		time    uint32
		pt      struct{ X, Y int32 }
	}
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			return
		}
		procTranslateMsg.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMsg.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

var trayByHwnd map[uintptr]*Tray

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	t := trayByHwnd[hwnd]
	switch {
	case msg == wmDestroy:
		if t != nil {
			procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
			delete(trayByHwnd, hwnd)
		}
		procPostQuitMsg.Call(0)
		return 0
	case msg == wmTrayIcon && t != nil:
		switch lparam {
		case wmLButtonUp:
			if t.onLeft != nil {
				go t.onLeft()
			}
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		}
		return 0
	}
	r, _, _ := procDefWndProc.Call(hwnd, msg, wparam, lparam)
	return r
}

// showMenu 每次右键全量重建菜单（动态：装/卸/切后立刻反映真实状态）。
// TPM_RETURNCMD 同步返回选择项 id；Sub 项递归建弹出子菜单。
func (t *Tray) showMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	appendItems(hMenu, t.onMenu())
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForeground.Call(t.nid.Hwnd) // 否则点击菜单外区域不消失（经典 bug）
	ret, _, _ := procTrackPopupMenu.Call(hMenu, tpmRightButton|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y), 0, t.nid.Hwnd, 0)
	procDestroyMenu.Call(hMenu)
	if ret != 0 && t.onPick != nil {
		go t.onPick(uint16(ret))
	}
}

func appendItems(hMenu uintptr, items []MenuItem) {
	for _, it := range items {
		if it.Sep {
			procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
			continue
		}
		label, _ := syscall.UTF16PtrFromString(it.Label)
		if len(it.Sub) > 0 {
			sub, _, _ := procCreatePopupMenu.Call()
			appendItems(sub, it.Sub)
			procAppendMenuW.Call(hMenu, mfPopup|mfString, sub, uintptr(unsafe.Pointer(label)))
			continue
		}
		flags := uintptr(mfString)
		if it.Disabled {
			flags |= mfDisabled
		}
		procAppendMenuW.Call(hMenu, flags, uintptr(it.ID), uintptr(unsafe.Pointer(label)))
		if it.Checked {
			procCheckMenuItem.Call(hMenu, uintptr(it.ID), mfByCommand|mfChecked)
		}
	}
}

// Quit 移除托盘图标（消息循环随线程退出）。
func (t *Tray) Quit() {
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
	procPostQuitMsg.Call(0)
}
