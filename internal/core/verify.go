package core

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ParseSHASUMS 从官方 SHASUMS256.txt 文本中取指定文件的哈希。
// 行格式 "<hash>  <name>" 或 "<hash> *<name>"。
func ParseSHASUMS(text, filename string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) < 66 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if strings.EqualFold(name, filename) {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// fetchCompanionHash 拉官方伴生哈希文件（<url>.sha256 等，格式 "<hex> [name]"）。
// 返回 (hash, 是否 sha512 长度, 是否找到)。
func fetchCompanionHash(ctx context.Context, url string) (string, bool, bool) {
	txt, err := GetText(ctx, url)
	if err != nil {
		return "", false, false
	}
	f := strings.Fields(txt)
	if len(f) == 0 {
		return "", false, false
	}
	h := strings.ToLower(f[0])
	switch len(h) {
	case 64:
		return h, false, true
	case 128:
		return h, true, true
	}
	return "", false, false
}

// FileSHA512 计算文件 sha512（hex 小写）。
func FileSHA512(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyIntegrity 校验文件满足 npm 风格 integrity（当前只支持 sha512）。
func VerifyIntegrity(path, integrity string) error {
	rest, ok := strings.CutPrefix(integrity, "sha512-")
	if !ok {
		return nil // 未知算法不拦（例如将来 sha384）
	}
	want, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return W(ErrVerify, "integrity 格式异常")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := h.Sum(nil)
	if len(got) != len(want) {
		return W(ErrVerify, "integrity 长度不符")
	}
	for i := range got {
		if got[i] != want[i] {
			return W(ErrVerify, "%s sha512 与 registry 不符，已拒绝",
				filepath.Base(path))
		}
	}
	return nil
}
