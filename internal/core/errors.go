// Package core 是 EnvSwitch 的编排层：错误码、远程索引、本地仓库、
// 下载校验解包、切换与热更新。GUI 与 CLI 都只调这一层，保证行为一致。
// 构建期注入版本：-ldflags "-X github.com/MUHUIX/envswitch/internal/core.Version=v1.0.0"
package core

import (
	"errors"
	"fmt"
	"runtime/debug"
)

var Version = "dev"

// BuildVersion 优先取链接注入值，dev 构建回落到模块版本信息。
func BuildVersion() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// 错误 → 退出码（README/方案 §8 约定）
var (
	ErrUsage     = errors.New("参数错误")
	ErrNet       = errors.New("网络错误")
	ErrVerify    = errors.New("校验失败")
	ErrPerm      = errors.New("权限不足")
	ErrAmbiguous = errors.New("版本歧义")
	ErrNotFound  = errors.New("未找到")
)

// ExitCode 把错误映射为进程退出码（cobra 参数解析错误由 main 单独按 2 处理）。
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		return 2
	case errors.Is(err, ErrNet):
		return 3
	case errors.Is(err, ErrVerify):
		return 4
	case errors.Is(err, ErrPerm):
		return 5
	case errors.Is(err, ErrAmbiguous):
		return 6
	default:
		return 1
	}
}

// W 包装错误并保留语义标签：core.W(core.ErrNet, "获取索引: %v", e)
func W(kind error, format string, args ...any) error {
	return &wrapped{kind: kind, msg: fmt.Sprintf(format, args...)}
}

type wrapped struct {
	kind error
	msg  string
}

func (e *wrapped) Error() string { return e.msg }
func (e *wrapped) Unwrap() error { return e.kind }
