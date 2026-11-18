// Package sys 封装 Windows 系统调用：junction 重解析点、用户 PATH 注册表、
// 开机自启 Run 键、单实例互斥量、环境变量广播。全部作用于 HKCU 与用户目录，
// 不需要管理员权限，也不需要开发者模式（junction ≠ symlink）。
package sys

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modkernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateFileWJ    = modkernel32.NewProc("CreateFileW")
	procDeviceIoControl = modkernel32.NewProc("DeviceIoControl")
	procCloseHandle     = modkernel32.NewProc("CloseHandle")
)

const (
	genericWriteJK         = 0x40000000
	fileShareReadWriteDelete = 7
	openExisting             = 3
	fileFlagOpenReparsePoint = 0x00200000
	fileFlagBackupsSemantics = 0x02000000

	// winioctl.h 实值：SET=CTL(41)=0x900A4，GET=CTL(42)=0x900A8，DELETE=CTL(43)=0x900AC
	fsctlGetReparsePoint    = 0x000900A8
	fsctlSetReparsePoint    = 0x000900A4
	fsctlDeleteReparsePoint = 0x000900AC

	ioReparseTagMountPoint = 0xA0000003
)

type mountPointHeader struct {
	ReparseTag           uint32
	ReparseDataLength    uint16
	Reserved             uint16
	SubstituteNameOffset uint16
	SubstituteNameLength uint16
	PrintNameOffset      uint16
	PrintNameLength      uint16
}

// utf16BytesNoNUL 已废弃：直接使用 syscall.StringToUTF16（含尾 NUL，junction 计算时显式处理）。

// CreateJunction 在 linkPath 创建指向 targetPath 的目录 junction。
// linkPath 必须不存在或已是空目录/junction。
func CreateJunction(linkPath, targetPath string) error {
	linkPath = strings.TrimRight(linkPath, `\/`)
	targetPath = strings.TrimRight(targetPath, `\/`)
	if targetPath == "" {
		return errors.New("junction 目标为空")
	}
	if err := os.MkdirAll(linkPath, 0o755); err != nil {
		return fmt.Errorf("创建 junction 目录失败: %w", err)
	}

	sub := `\??\` + targetPath // reparse 用设备命名空间前缀（不是 Win32 的 \\?\）
	printName := targetPath

	subW := syscall.StringToUTF16(sub)             // 含尾 NUL
	printW := syscall.StringToUTF16(printName)     // 含尾 NUL

	hdr := mountPointHeader{
		ReparseTag:           ioReparseTagMountPoint,
		SubstituteNameOffset: 0,
		SubstituteNameLength: uint16((len(subW) - 1) * 2),
		PrintNameOffset:      uint16((len(subW)) * 2),
		PrintNameLength:      uint16((len(printW) - 1) * 2),
	}
	bufSize := int(unsafe.Sizeof(hdr)) + (len(subW)+len(printW))*2
	buf := make([]byte, bufSize)
	// 头
	*(*mountPointHeader)(unsafe.Pointer(&buf[0])) = hdr
	hdrBytes := int(unsafe.Sizeof(hdr))
	// PathBuffer
	copy(buf[hdrBytes:], unsafe.Slice((*byte)(unsafe.Pointer(&subW[0])), len(subW)*2))
	copy(buf[hdrBytes+len(subW)*2:], unsafe.Slice((*byte)(unsafe.Pointer(&printW[0])), len(printW)*2))
	// ReparseDataLength = 从 Reserved 之后到结尾的字节数 = total - 8
	*(*uint16)(unsafe.Pointer(&buf[4])) = uint16(bufSize - 8)

	h, err := openReparseHandle(linkPath, genericWriteJK)
	if err != nil {
		return err
	}
	defer procCloseHandle.Call(h)

	var ret uint32
	r1, _, e1 := procDeviceIoControl.Call(
		h, fsctlSetReparsePoint,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		0, 0, uintptr(unsafe.Pointer(&ret)), 0)
	if r1 == 0 {
		return fmt.Errorf("FSCTL_SET_REPARSE_POINT 失败 (%v)：%s", e1, winErrHint(e1))
	}
	return nil
}

// RemoveJunction 删除 junction（只摘链接，不碰目标内容）；普通空目录也能删。
func RemoveJunction(linkPath string) error {
	linkPath = strings.TrimRight(linkPath, `\/`)
	h, err := openReparseHandle(linkPath, genericWriteJK)
	if err != nil {
		// 不是 reparse point：尝试直接删目录
		if e := os.Remove(linkPath); e == nil {
			return nil
		}
		return err
	}
	defer procCloseHandle.Call(h)

	del := make([]byte, 24) // REPARSE_GUID_DATA_BUFFER：tag4+len2+rsv2+guid16
	*(*uint32)(unsafe.Pointer(&del[0])) = ioReparseTagMountPoint
	var ret uint32
	r1, _, e1 := procDeviceIoControl.Call(
		h, fsctlDeleteReparsePoint,
		uintptr(unsafe.Pointer(&del[0])), uintptr(len(del)),
		0, 0, uintptr(unsafe.Pointer(&ret)), 0)
	if r1 == 0 {
		return fmt.Errorf("FSCTL_DELETE_REPARSE_POINT 失败: %v", e1)
	}
	// 重解析点已摘除，现在 linkPath 是空目录
	return os.Remove(linkPath)
}

// ReadJunction 读取 link 的重解析数据，返回其指向的真实目录。
// 第一返回值 = 目标（即使目标已失效也会返回读到的路径）；
// 第二返回值 = 是否为有效 mount-point junction 且目标存在。
func ReadJunction(link string) (string, bool) {
	link = strings.TrimRight(link, `\/`)
	h, err := openReparseHandle(link, 0)
	if err != nil {
		return "", false
	}
	defer procCloseHandle.Call(h)
	out := make([]byte, 8192)
	var ret uint32
	r1, _, _ := procDeviceIoControl.Call(
		h, fsctlGetReparsePoint,
		0, 0,
		uintptr(unsafe.Pointer(&out[0])), uintptr(len(out)),
		uintptr(unsafe.Pointer(&ret)), 0)
	if r1 == 0 || int(ret) < 16 {
		return "", false
	}
	if *(*uint32)(unsafe.Pointer(&out[0])) != ioReparseTagMountPoint {
		return "", false
	}
	subOff := *(*uint16)(unsafe.Pointer(&out[8]))
	subLen := *(*uint16)(unsafe.Pointer(&out[10]))
	start := 16 + int(subOff)
	end := start + int(subLen)
	if end > int(ret) || subLen < 2 {
		return "", false
	}
	us := unsafe.Slice((*uint16)(unsafe.Pointer(&out[start])), int(subLen)/2)
	sub := strings.TrimPrefix(syscall.UTF16ToString(us), `\??\`)
	if sub == "" {
		return "", false
	}
	fi, err := os.Stat(sub)
	if err != nil || !fi.IsDir() {
		return sub, false // 链接存在但目标失效
	}
	return sub, true
}

// IsJunction 判断路径是指向 targetPath 的 junction（target 为空则只判有效存在）。
func IsJunction(linkPath, targetPath string) (bool, error) {
	sub, ok := ReadJunction(linkPath)
	if !ok {
		return false, nil
	}
	if targetPath == "" {
		return true, nil
	}
	return strings.EqualFold(strings.TrimRight(sub, `\/`), strings.TrimRight(targetPath, `\/`)), nil
}

func openReparseHandle(path string, access uint32) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, _, e1 := procCreateFileWJ.Call(
		uintptr(unsafe.Pointer(p)), uintptr(access),
		uintptr(fileShareReadWriteDelete), 0,
		uintptr(openExisting),
		uintptr(fileFlagOpenReparsePoint|fileFlagBackupsSemantics), 0)
	if h == ^uintptr(0) { // INVALID_HANDLE_VALUE
		return 0, fmt.Errorf("打开 %s 失败: %v", path, e1)
	}
	return h, nil
}

// winErrHint 给出常见错误的人话解释。
func winErrHint(err error) string {
	if errno, ok := err.(syscall.Errno); ok {
		switch errno {
		case syscall.Errno(1): // ERROR_INVALID_FUNCTION
			return "目标卷不支持重解析点"
		case syscall.Errno(5): // ERROR_ACCESS_DENIED
			return "目录可能被占用或权限不足"
		}
	}
	return ""
}
