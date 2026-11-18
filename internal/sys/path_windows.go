package sys

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// ReadUserPath 读取用户 PATH 原始值（保留 %VAR% 不展开）。
func ReadUserPath() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer k.Close()
	// GetStringsValue/GetStringValue 对 REG_EXPAND_SZ 返回未展开的原始串
	v, _, err := k.GetStringValue("Path")
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// WriteUserPath 以 REG_EXPAND_SZ 写回用户 PATH（保持与系统一致的语义）。
func WriteUserPath(value string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开 HKCU\\Environment 失败: %w", err)
	}
	defer k.Close()
	if err := k.SetExpandStringValue("Path", value); err != nil {
		return fmt.Errorf("写用户 PATH 失败: %w", err)
	}
	return nil
}

// splitPath 按 ; 切分并去空段（保留原始大小写与 %VAR% 形态）。
func splitPath(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// EnsureUserPath 把 owned 六条路径插到用户 PATH 最前；已存在则只调整相对顺序。
// 返回 (修改前的原始值, 是否发生变化, error)——原始值供调用方快照备份。
func EnsureUserPath(owned []string) (string, bool, error) {
	raw, err := ReadUserPath()
	if err != nil {
		return "", false, err
	}
	parts := splitPath(raw)
	set := map[string]bool{}
	for _, o := range owned {
		set[strings.ToLower(o)] = true
	}
	rest := make([]string, 0, len(parts))
	for _, p := range parts {
		if !set[strings.ToLower(p)] {
			rest = append(rest, p)
		}
	}
	next := strings.Join(append(append([]string{}, owned...), rest...), ";")
	if next == raw {
		return raw, false, nil
	}
	if err := WriteUserPath(next); err != nil {
		return raw, false, err
	}
	return raw, true, nil
}

// StripOwnedUserPath 移除用户 PATH 中所有以 home 前缀开头的 current\ 条目（卸载/恢复用）。
// 返回 (原始值, 是否变化)。
func StripOwnedUserPath(isOwned func(string) bool) (string, bool, error) {
	raw, err := ReadUserPath()
	if err != nil {
		return "", false, err
	}
	parts := splitPath(raw)
	rest := make([]string, 0, len(parts))
	changed := false
	for _, p := range parts {
		if isOwned(p) {
			changed = true
			continue
		}
		rest = append(rest, p)
	}
	if !changed {
		return raw, false, nil
	}
	if err := WriteUserPath(strings.Join(rest, ";")); err != nil {
		return raw, false, err
	}
	return raw, true, nil
}

// UserPathCanWrite 探测 HKCU\Environment 写权限（打开即关，不产生写入）。
func UserPathCanWrite() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// MachinePath 读取系统 PATH（只读，供 doctor 分析顺序）。
func MachinePath() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("Path")
	return v
}
