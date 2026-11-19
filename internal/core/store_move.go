package core

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// 版本库根目录迁移：整体换盘，而不是"每个版本散落各处"。
// 只有整棵树一起搬，scanStore / RemoveVersion / 占用统计才继续成立。
//
// 步骤：校验目标 → 统计 → 同盘 rename 或跨盘复制 → 校验 → 落盘新路径 →
// 重建 current junction → 清理旧目录。任一步失败都不改配置，跨盘复制失败会
// 删除本次写入的目标树（用户选的是空目录，删它不会碰到既有数据）。

type MoveProgress func(stage string, pct float64, message string)

type storeStats struct {
	Files int64
	Bytes int64
}

func sameVolume(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}

func isUnderPath(p, root string) bool {
	p = filepath.Clean(p)
	root = filepath.Clean(root)
	if strings.EqualFold(p, root) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(p), strings.ToLower(root)+string(filepath.Separator))
}

// ValidateStoreRoot 校验并规范化用户选的新版本库根。
// 拒绝：空值、UNC 网络路径、盘根、EnvSwitch 的 current/bin 内部、非空目录、不可写。
func ValidateStoreRoot(s *settings.Settings, root string) (string, error) {
	raw := strings.TrimSpace(root)
	if raw == "" {
		return "", W(ErrUsage, "版本库路径不能为空")
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return "", W(ErrUsage, "不支持网络路径；版本库必须在本机磁盘上，否则盘符变化或断网会失联")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", W(ErrUsage, "路径无法解析：%v", err)
	}
	clean := filepath.Clean(abs)
	if filepath.VolumeName(clean) == "" {
		return "", W(ErrUsage, "请输入带盘符的绝对路径")
	}
	if len(strings.TrimSuffix(clean, string(filepath.Separator))) <= 2 {
		return "", W(ErrUsage, "请选择磁盘下的子目录，不要直接使用盘根")
	}
	if strings.EqualFold(clean, filepath.Clean(settings.StoreDir(s))) {
		return "", W(ErrUsage, "新路径与当前版本库相同")
	}
	home := filepath.Clean(settings.Home())
	// 允许迁回默认位置（home\store），但 home 下的其它目录一律拒绝：
	// 那些目录和 current / bin / downloads 混在一起，迁移会互相踩。
	if isUnderPath(clean, home) && !strings.EqualFold(clean, filepath.Join(home, "store")) {
		return "", W(ErrUsage, "不要把版本库放进 EnvSwitch 的数据目录；请选择其他磁盘或独立目录")
	}
	if fi, err := os.Stat(clean); err == nil {
		if !fi.IsDir() {
			return "", W(ErrUsage, "目标路径已存在且不是目录：%s", clean)
		}
		ents, err := os.ReadDir(clean)
		if err != nil {
			return "", W(ErrPerm, "无法读取目标目录：%v", err)
		}
		if len(ents) > 0 {
			return "", W(ErrUsage, "目标目录不是空的，请换一个空目录（若是上次迁移中断留下的残留，请手动清空后重试）")
		}
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return "", W(ErrPerm, "无法创建目标目录：%v", err)
	}
	probe := filepath.Join(clean, ".envs-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return "", W(ErrPerm, "目标目录不可写（可能需要管理员权限）：%v", err)
	}
	_ = os.Remove(probe)
	return clean, nil
}

// MoveStore 把版本库整体迁到 newRoot；progress 用于前端进度条。
func MoveStore(ctx context.Context, s *settings.Settings, ads []Adapter, newRoot string, progress MoveProgress) error {
	if progress == nil {
		progress = func(string, float64, string) {}
	}
	clean, err := ValidateStoreRoot(s, newRoot)
	if err != nil {
		return err
	}
	old := filepath.Clean(settings.StoreDir(s))
	progress("scan", 0, "统计版本文件…")
	stat, err := treeStats(old)
	if err != nil {
		return W(ErrPerm, "统计旧版本库失败：%v", err)
	}

	// 空库：没有文件要搬，直接换路径
	if stat.Files == 0 {
		if err := os.MkdirAll(clean, 0o755); err != nil {
			return W(ErrPerm, "创建新版本库失败：%v", err)
		}
		progress("done", 100, "版本库路径已更新")
		return applyStorePath(s, clean)
	}

	if sameVolume(old, clean) {
		progress("move", 20, "同盘移动目录…")
		if err := moveSameVolume(old, clean); err != nil {
			return err
		}
		progress("move", 80, "目录已移动")
	} else {
		progress("copy", 5, fmt.Sprintf("跨盘复制 %d 个文件（%s）…", stat.Files, HumanSize(stat.Bytes)))
		var last int64
		err := copyTree(ctx, old, clean, func(done int64) {
			if done-last < 4<<20 && done != stat.Bytes {
				return
			}
			last = done
			pct := 5 + 70*float64(done)/float64(max(stat.Bytes, 1))
			progress("copy", pct, "复制中… "+HumanSize(done)+" / "+HumanSize(stat.Bytes))
		})
		if err != nil {
			_ = os.RemoveAll(clean)
			if ctx.Err() != nil {
				return W(ErrUsage, "已取消，旧版本库未改动")
			}
			return W(ErrPerm, "复制失败，已回滚：%v", err)
		}
		progress("verify", 80, "校验复制结果…")
		got, e := treeStats(clean)
		if e != nil || got.Files != stat.Files || got.Bytes != stat.Bytes {
			_ = os.RemoveAll(clean)
			return W(ErrVerify, "校验不一致（源 %d 文件 / %s，目标 %d 文件 / %s），已回滚",
				stat.Files, HumanSize(stat.Bytes), got.Files, HumanSize(got.Bytes))
		}
	}

	if err := applyStorePath(s, clean); err != nil {
		// 配置没落盘就不能留下"文件已搬走、程序还认旧路径"的半截状态
		if sameVolume(old, clean) {
			_ = os.Rename(clean, old)
		} else {
			_ = os.RemoveAll(clean)
		}
		return W(ErrPerm, "保存版本库配置失败，已回滚：%v", err)
	}
	progress("links", 90, "重建激活链接…")
	if err := rebuildLinks(s, ads, old, clean); err != nil {
		return fmt.Errorf("版本库已迁到 %s，但激活链接重建失败（重新切换一次即可修复）：%v", clean, err)
	}

	if !sameVolume(old, clean) {
		progress("cleanup", 96, "清理旧版本库…")
		if err := os.RemoveAll(old); err != nil {
			return fmt.Errorf("版本库已迁到 %s，但旧目录清理失败（可手动删除）：%v", clean, err)
		}
	}
	progress("done", 100, "版本库已迁移到 "+clean)
	return nil
}

// applyStorePath 落盘新路径；等于默认位置时清空 StorePath，保持配置干净。
// Save 失败必须把内存值退回去：否则文件已回滚、运行中的应用却仍指向新根，
// 界面会瞬间变成"一个版本都没有"。
func applyStorePath(s *settings.Settings, root string) error {
	prev := s.StorePath
	def := filepath.Join(settings.Home(), "store")
	if strings.EqualFold(filepath.Clean(root), filepath.Clean(def)) {
		s.StorePath = ""
	} else {
		s.StorePath = root
	}
	if err := s.Save(); err != nil {
		s.StorePath = prev
		return err
	}
	return nil
}

func moveSameVolume(old, dst string) error {
	if fi, err := os.Stat(dst); err == nil {
		if !fi.IsDir() {
			return W(ErrUsage, "目标路径已存在且不是目录：%s", dst)
		}
		ents, err := os.ReadDir(dst)
		if err != nil {
			return W(ErrPerm, "无法读取目标目录：%v", err)
		}
		if len(ents) > 0 {
			return W(ErrUsage, "目标目录不是空的，请换一个空目录")
		}
		if err := os.Remove(dst); err != nil {
			return W(ErrPerm, "清理空目标目录失败：%v", err)
		}
	}
	if err := os.Rename(old, dst); err != nil {
		return W(ErrPerm, "移动版本库失败（可能跨盘或被占用）：%v", err)
	}
	return nil
}

// treeStats 统计文件数与总字节；根不存在视为空库。
func treeStats(root string) (storeStats, error) {
	var st storeStats
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return st, nil
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		st.Files++
		st.Bytes += fi.Size()
		return nil
	})
	return st, err
}

// copyTree 逐文件复制并回调累计字节。符号链接一律拒绝：
// 跟随链接可能把版本库外的大目录复制进来，跳过又会造成残缺安装。
func copyTree(ctx context.Context, src, dst string, onBytes func(int64)) error {
	var done int64
	buf := make([]byte, 1<<20)
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("版本库包含符号链接，无法安全迁移：%s", p)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		for {
			n, rerr := in.Read(buf)
			if n > 0 {
				if _, werr := out.Write(buf[:n]); werr != nil {
					in.Close()
					out.Close()
					return werr
				}
				done += int64(n)
				if onBytes != nil {
					onBytes(done)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				in.Close()
				out.Close()
				return rerr
			}
		}
		in.Close()
		if err := out.Close(); err != nil {
			return err
		}
		return nil
	})
}

// rebuildLinks 把 current\<tool> 从旧根映射到新根；目标不存在就摘掉链接，
// 避免留下指向空目录的 junction（那比"未激活"更难排查）。
//
// 注意不能把 ReadJunction 的第二返回值当"链接是否存在"：迁移后旧目标已经
// 不存在，那个返回值必然是 false，但链接本身还在、还需要摘掉重建。
func rebuildLinks(s *settings.Settings, ads []Adapter, oldRoot, newRoot string) error {
	var firstErr error
	for _, ad := range ads {
		link := settings.CurrentLink(s, ad.ID())
		target, _ := junctionTarget(link)
		if target == "" {
			continue
		}
		rel, err := filepath.Rel(oldRoot, target)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue // 外部/nvm 收编的链接不随版本库迁移
		}
		next := filepath.Join(newRoot, rel)
		if err := sys.RemoveJunction(link); err != nil {
			if _, statErr := os.Lstat(link); statErr == nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("摘除 %s 失败：%w", filepath.Base(link), err)
				}
				continue
			}
		}
		if !dirExists(next) {
			continue
		}
		if err := sys.CreateJunction(link, next); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("重建 %s 失败：%w", filepath.Base(link), err)
		}
	}
	return firstErr
}
