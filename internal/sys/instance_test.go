package sys

import (
	"errors"
	"testing"
	"time"
)

// 单实例锁的两种语义：普通启动抢一次就走，热更新重启必须等上一进程退场。
// 同一进程内第二次 CreateMutexW 同样返回 ERROR_ALREADY_EXISTS，所以「已被占用」
// 这条路径能在单测里确定性复现，不必真起两个进程。

func TestAcquireSingleInstanceRejectsSecond(t *testing.T) {
	name := "EnvSwitch.Test." + t.Name()
	if err := AcquireSingleInstance(name); err != nil {
		t.Fatalf("首次获取失败: %v", err)
	}
	if err := AcquireSingleInstance(name); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("第二次应报已在运行，实得 %v", err)
	}
}

// 锁仍被握着时，Wait 必须等满时限再交出 ErrAlreadyRunning —— 这是新进程
// 「不起就退」与「等旧进程退场」的分界。
func TestAcquireSingleInstanceWaitTimesOutWhileHeld(t *testing.T) {
	name := "EnvSwitch.Test." + t.Name()
	if err := AcquireSingleInstance(name); err != nil {
		t.Fatalf("首次获取失败: %v", err)
	}
	start := time.Now()
	err := AcquireSingleInstanceWait(name, 500*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("锁被占时应超时返回 ErrAlreadyRunning，实得 %v", err)
	}
	if elapsed < 400*time.Millisecond {
		t.Fatalf("没有等满时限就返回了: %v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("超时之后仍在重试: %v", elapsed)
	}
}

// 锁空闲时 Wait 应立刻成功，不让正常重启白等一个轮询周期。
func TestAcquireSingleInstanceWaitSucceedsWhenFree(t *testing.T) {
	name := "EnvSwitch.Test." + t.Name()
	start := time.Now()
	if err := AcquireSingleInstanceWait(name, 5*time.Second); err != nil {
		t.Fatalf("锁空闲时应立即拿到: %v", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("锁空闲却等了 %v", d)
	}
}
