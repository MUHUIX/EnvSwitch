package sys

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// 用户级环境变量（HKCU\Environment）读写：只碰单个值，不重写整块环境。
// 与 PATH 一样用 REG_EXPAND_SZ，值里允许保留 %LOCALAPPDATA% 这类占位符。

// ReadUserEnv 读取用户级环境变量原文（不展开 %VAR%）。
func ReadUserEnv(name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("打开 HKCU\\Environment 失败: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("读取用户变量 %s 失败: %w", name, err)
	}
	return v, true, nil
}

// WriteUserEnv 写入用户级环境变量（REG_EXPAND_SZ）。
func WriteUserEnv(name, value string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开 HKCU\\Environment 失败（可能被组策略限制）: %w", err)
	}
	defer k.Close()
	if err := k.SetExpandStringValue(name, value); err != nil {
		return fmt.Errorf("写入用户变量 %s 失败: %w", name, err)
	}
	return nil
}

// DeleteUserEnv 删除用户级环境变量；不存在视为成功。
func DeleteUserEnv(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开 HKCU\\Environment 失败: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("删除用户变量 %s 失败: %w", name, err)
	}
	return nil
}
