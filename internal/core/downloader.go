package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Minute}

// Progress 下载进度回调。
type Progress func(done, total int64, speedBps float64)

// FetchURL 下载 url 到 dst 路径（自动 .part 断点续传、Range、取消）。
// expectSHA 非空时流式校验 sha256。
func FetchURL(ctx context.Context, url, dst string, expectSHA string, prog Progress) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	part := dst + ".part"
	offset := int64(0)
	if fi, err := os.Stat(part); err == nil {
		offset = fi.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return W(ErrNet, "构造请求: %v", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return W(ErrNet, "下载 %s: %v", filepath.Base(url), err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent: // 续传
	case http.StatusOK:
		offset = 0 // 服务器不支持 Range，重头下载
	case http.StatusRequestedRangeNotSatisfiable:
		offset = 0
		os.Remove(part)
	default:
		return W(ErrNet, "HTTP %d (%s)", resp.StatusCode, url)
	}

	total := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent && total >= 0 {
		total += offset
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}

	buf := make([]byte, 256*1024)
	done := offset
	start := time.Now()
	lastTick := start
	var writeErr error
	for {
		if ctx.Err() != nil {
			writeErr = ctx.Err()
			break
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				writeErr = werr
				break
			}
			done += int64(n)
			if prog != nil && time.Since(lastTick) > 200*time.Millisecond {
				elapsed := time.Since(start).Seconds()
				var speed float64
				if elapsed > 0 {
					speed = float64(done-offset) / elapsed
				}
				prog(done, total, speed)
				lastTick = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			writeErr = W(ErrNet, "读取流中断: %v", rerr)
			break
		}
	}
	// 显式且唯一关闭文件句柄，避免 Windows 下后续操作触发 ERROR_SHARING_VIOLATION
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if prog != nil {
		prog(done, total, 0)
	}

	// Range 续传时上面只哈希了新段；统一以整文件复核为准
	if expectSHA != "" {
		got, err := FileSHA256(part)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, expectSHA) {
			os.Remove(part)
			return W(ErrVerify, "%s 哈希不符（期望 %.12s… 实际 %.12s…），已删除待重下",
				filepath.Base(part), expectSHA, got)
		}
	}
	return os.Rename(part, dst)
}

// FileSHA256 计算文件 sha256（hex 小写）。
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GetJSON GET 并解析 JSON（限 32MB）。
func GetJSON(ctx context.Context, url string, out any) error {
	return getJSONAccept(ctx, url, "application/json", out)
}

func getJSONAccept(ctx context.Context, url, accept string, out any) error {
	body, err := getWithRetry(ctx, url, accept)
	if err != nil {
		return err
	}
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF}) // 带 BOM 的发布文件很常见
	if err := json.Unmarshal(body, out); err != nil {
		return W(ErrNet, "解析 %s: %v", url, err)
	}
	return nil
}

// GetText 拉取小体积文本（SHASUMS 等）。
func GetText(ctx context.Context, url string) (string, error) {
	b, err := getWithRetry(ctx, url, "text/plain")
	return string(b), err
}

// GetBytesAccept 拉取响应体（限 32MB），自定义 Accept。
func GetBytesAccept(ctx context.Context, url, accept string) ([]byte, error) {
	return getWithRetry(ctx, url, accept)
}

// getWithRetry 索引类请求对瞬时故障重试两次（GitHub/raw 与 k8s CDN 在国内
// 常见 TLS reset 与超时，一次重试即可覆盖大部分抖动）。
func getWithRetry(ctx context.Context, url, accept string) ([]byte, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 800 * time.Millisecond):
			}
		}
		body, err := getOnce(ctx, url, accept)
		if err == nil {
			return body, nil
		}
		last = err
	}
	return nil, last
}

func getOnce(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "EnvSwitch")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, W(ErrNet, "%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, W(ErrNet, "HTTP %d (%s)", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, W(ErrNet, "%v", err)
	}
	return body, nil
}

// WithProxy 给 GitHub 下载 URL 套代理前缀。
func WithProxy(proxy, url string) string {
	if proxy == "" || !strings.HasPrefix(url, "https://github.com/") && !strings.HasPrefix(url, "https://objects.githubusercontent.com/") {
		return url
	}
	if !strings.HasSuffix(proxy, "/") {
		proxy += "/"
	}
	return proxy + url
}

// pathDir 取 URL 的目录部分（不用 filepath，避免反斜杠污染）。
func pathDir(u string) string {
	if i := strings.LastIndexByte(u, '/'); i > 0 {
		return u[:i]
	}
	return u
}

// humanSize 1.2 GB / 482 MB / 96 KB。
func HumanSize(n int64) string {
	if n < 0 {
		return "—"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
