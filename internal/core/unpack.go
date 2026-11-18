package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Unpack 解 zip / tar.gz 到 dst，并把唯一顶层目录展平（node-vX-win-x64/、go/、python/、package/）。
// 已做 zip-slip 防护（禁止绝对路径与 .. 逃逸）。
func Unpack(archive, dst string) error {
	tmp := dst + ".extract"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	lower := strings.ToLower(archive)
	var err error
	switch {
	case strings.HasSuffix(lower, ".zip"):
		err = unzip(archive, tmp)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		err = untarGz(archive, tmp)
	default:
		return fmt.Errorf("不支持的归档格式: %s", filepath.Base(archive))
	}
	if err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := flatten(tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		os.RemoveAll(dst)
	}
	return os.Rename(tmp, dst)
}

func unzip(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("打开 zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := filepath.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("zip 内含危险路径: %s", f.Name)
		}
		p := filepath.Join(dst, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		cerr := w.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
	}
	return nil
}

func untarGz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		name := filepath.Clean(strings.ReplaceAll(h.Name, "\\", "/"))
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("tar 内含危险路径: %s", h.Name)
		}
		p := filepath.Join(dst, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777|0o200)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return err
			}
			w.Close()
		case tar.TypeSymlink, tar.TypeLink:
			// standalone python 构建基本无链接；出现则跳过并记录（不影响主链路）
			continue
		}
	}
}

// flatten：tmp 下若只有唯一目录（无散文件），把其子项上移一层。
func flatten(tmp string) error {
	ents, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	if len(ents) != 1 || !ents[0].IsDir() {
		return nil
	}
	inner := filepath.Join(tmp, ents[0].Name())
	children, err := os.ReadDir(inner)
	if err != nil {
		return err
	}
	for _, c := range children {
		if err := os.Rename(filepath.Join(inner, c.Name()), filepath.Join(tmp, c.Name())); err != nil {
			return fmt.Errorf("展平目录: %w", err)
		}
	}
	return os.Remove(inner)
}

// DirSize 递归统计目录字节数。
func DirSize(path string) int64 {
	var total int64
	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // 读不到的子项按 0 计
		}
		if fi, e := d.Info(); e == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}
