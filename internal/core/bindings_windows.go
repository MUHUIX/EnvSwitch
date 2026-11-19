//go:build windows

package core

// 目录绑定的 Win32 侧支撑（方案 §4.2 归一、§6.1 cmd AutoRun 安装）。
// 全部只读 API + HKCU 写；不需要管理员，也不需要开发者模式。

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const cmdProcessorKey = `Software\Microsoft\Command Processor`

func init() {
	winLongPath = getLongPathName
	winSubstRoot = dosDeviceRoot
	winFinalPath = finalPathByHandle
	winAutoRunGet = readCmdAutoRun
	winAutoRunSet = writeCmdAutoRun
}

// getLongPathName 展 8.3 短名（治 §2-L11）。失败（目录不存在、权限）返回空串，
// 调用方保留原值 —— 归一不因此致命（§4.2「失败保留原值，不致命」）。
func getLongPathName(p string) string {
	if p == "" {
		return ""
	}
	ptr, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	n, err := windows.GetLongPathName(ptr, nil, 0)
	if err != nil || n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	n2, err := windows.GetLongPathName(ptr, &buf[0], n)
	if err != nil || n2 == 0 || n2 > n {
		return ""
	}
	return windows.UTF16ToString(buf[:n2])
}

// dosDeviceRoot 用 QueryDosDeviceW 解 subst / dos 盘：X: → 真实目录。
// 普通盘符返回空（或自身），调用方自行判断是否替换。
func dosDeviceRoot(drive string) string {
	if len(drive) != 2 || drive[1] != ':' {
		return ""
	}
	name, err := windows.UTF16PtrFromString(drive[:1])
	if err != nil {
		return ""
	}
	buf := make([]uint16, 32*1024)
	n, err := windows.QueryDosDevice(name, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	target := windows.UTF16ToString(buf[:n])
	// subst 盘的返回形如 `\??\E:\work\linked`；普通盘是 `\Device\HarddiskVolumeN`，后者无法反解 → 忽略
	target = strings.TrimPrefix(target, `\??\`)
	target = strings.TrimPrefix(target, `\DosDevices\`)
	if len(target) >= 2 && target[1] == ':' {
		return target
	}
	return ""
}

// finalPathByHandle 取目录经重解析点解析后的最终路径（GetFinalPathNameByHandleW）。
// 用于把 junction / 符号链接的「链接路径」与「目标路径」双键登记（治 §2-L11）。
func finalPathByHandle(p string) string {
	if p == "" {
		return ""
	}
	ptr, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	h, err := windows.CreateFile(ptr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil || h == windows.InvalidHandle {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, 32*1024)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || n == 0 || n > uint32(len(buf)) {
		return ""
	}
	out := windows.UTF16ToString(buf[:n])
	// \\?\C:\x 与 \??\C:\x 两种前缀都要剥（§4.2）
	for _, pre := range extPathPrefixes {
		if len(out) >= len(pre) && strings.EqualFold(out[:len(pre)], pre) {
			out = out[len(pre):]
			break
		}
	}
	// Volume{GUID} 形态无法映射回盘符时直接放弃（§14-7 待实测）
	if strings.HasPrefix(strings.ToLower(out), volumeGUIDPrefix) {
		return ""
	}
	return out
}

const volumeGUIDPrefix = `volume{`

// readCmdAutoRun 读 HKCU\...\Command Processor\AutoRun。
// 第二返回值 = 该键存在且有值。
func readCmdAutoRun() (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, cmdProcessorKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("AutoRun")
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, strings.TrimSpace(v) != "", nil
}

// writeCmdAutoRun 写/删 AutoRun。remove=true 时删除该值（§6.1「卸载即删」）。
func writeCmdAutoRun(value string, remove bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, cmdProcessorKey, registry.SET_VALUE)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		k, _, err = registry.CreateKey(registry.CURRENT_USER, cmdProcessorKey, registry.SET_VALUE)
		if err != nil {
			return err
		}
	}
	defer k.Close()
	if remove {
		if err := k.DeleteValue("AutoRun"); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	return k.SetStringValue("AutoRun", value)
}
