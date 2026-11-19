package sys

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

var (
	modk32          = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = modk32.NewProc("CreateMutexW")
	// procCloseHandle 复用 junction.go 里的同一 kernel32 声明，避免同包重复 lazy proc。
)

// ErrAlreadyRunning 表示另一实例已持有互斥量。
var ErrAlreadyRunning = errors.New("EnvSwitch 已在运行")

// tryAcquire 单次尝试占住命名互斥量。
//
// 成功时**故意不关句柄**：内核对象活到最后一个句柄关闭为止，本进程全程握着它
// 才等于占住这把锁（句柄表随进程退出由系统回收，无需显式释放）。失败时必须关——
// 等锁的轮询若每 200ms 攒一个句柄，十五秒就是七十个。
func tryAcquire(name string) error {
	p, err := syscall.UTF16PtrFromString("Local\\" + name)
	if err != nil {
		return err
	}
	h, _, e1 := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(p)))
	if e1 == syscall.ERROR_ALREADY_EXISTS {
		if h != 0 {
			procCloseHandle.Call(h)
		}
		return ErrAlreadyRunning
	}
	if h == 0 {
		if e1 != nil {
			return e1
		}
		return errors.New("CreateMutexW 返回空句柄")
	}
	return nil
}

// AcquireSingleInstance 创建命名互斥量；拿不到立刻返回 ErrAlreadyRunning。
func AcquireSingleInstance(name string) error { return tryAcquire(name) }

// AcquireSingleInstanceWait 轮询重试（200ms 一次）直到拿到互斥量或超时。
//
// 热更新/回滚重启专用：上一进程从 Quit() 到真正消失有几百毫秒到数秒的窗口——
// 消息泵退出、托盘线程收尾、内核释放句柄都在这段里——期间 Local\ 互斥量仍被它
// 握着。单次尝试必然撞 ERROR_ALREADY_EXISTS，新进程就地静默自杀，表现出来就是
// 「软件关了，新版本没起来」。等它退场才是对的；超时返回错误，交调用方留现场。
func AcquireSingleInstanceWait(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := tryAcquire(name)
		// 成功、非「已占用」的真错误、或已到时限：都把手上的结果交出去。
		if !errors.Is(err, ErrAlreadyRunning) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
