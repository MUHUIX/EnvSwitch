package sys

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	modk32          = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = modk32.NewProc("CreateMutexW")
)

// ErrAlreadyRunning 表示另一实例已持有互斥量。
var ErrAlreadyRunning = errors.New("EnvSwitch 已在运行")

// AcquireSingleInstance 创建命名互斥量；句柄进程退出由系统回收，无需显式释放。
func AcquireSingleInstance(name string) error {
	p, err := syscall.UTF16PtrFromString("Local\\" + name)
	if err != nil {
		return err
	}
	_, _, e1 := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(p)))
	if e1 == syscall.ERROR_ALREADY_EXISTS {
		return ErrAlreadyRunning
	}
	return nil
}
