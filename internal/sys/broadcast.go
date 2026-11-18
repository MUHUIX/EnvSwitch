package sys

import (
	"syscall"
	"unsafe"
)

var (
	moduser32  = syscall.NewLazyDLL("user32.dll")
	procSendMT = moduser32.NewProc("SendMessageTimeoutW")
)

const (
	hwndBroadcast   = 0xffff
	wmSettingChange = 0x001A
	smtoNormal      = 0x0000
	smtoAbortIfHung = 0x0002
)

// BroadcastEnvChange 通知资源管理器环境已变（新终端从注册表取新值，已开终端不受影响）。
func BroadcastEnvChange() {
	env, _ := syscall.UTF16PtrFromString("Environment")
	var result uintptr
	procSendMT.Call(
		hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(env)),
		smtoNormal|smtoAbortIfHung, 1000, // 1 秒上限，避免被无响应窗口卡住
		uintptr(unsafe.Pointer(&result)),
	)
}
