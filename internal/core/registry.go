package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/MUHUIX/envswitch/internal/settings"
)

type cacheFile struct {
	FetchedAt int64           `json:"fetchedAt"`
	Versions  []RemoteVersion `json:"versions"`
}

// RemoteCached 远程列表带 TTL 缓存；网络失败回退缓存并标 stale（离线可用）。
func RemoteCached(ctx context.Context, ad Adapter, s *settings.Settings) (vers []RemoteVersion, stale bool, err error) {
	path := filepath.Join(settings.CacheDir(s), ad.ID()+".json")
	load := func() ([]RemoteVersion, int64, bool) {
		data, e := os.ReadFile(path)
		if e != nil {
			return nil, 0, false
		}
		var cf cacheFile
		if json.Unmarshal(data, &cf) != nil || len(cf.Versions) == 0 {
			return nil, 0, false
		}
		return cf.Versions, cf.FetchedAt, true
	}

	ttl := time.Duration(s.IndexCacheMinutes) * time.Minute
	if vers, at, ok := load(); ok && time.Since(time.UnixMilli(at)) < ttl {
		return vers, false, nil
	}
	vers, ferr := ad.FetchRemote(s)
	if ferr == nil {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if data, e := json.Marshal(cacheFile{FetchedAt: time.Now().UnixMilli(), Versions: vers}); e == nil {
			tmp := path + ".tmp"
			if os.WriteFile(tmp, data, 0o644) == nil {
				os.Rename(tmp, path)
			}
		}
		return vers, false, nil
	}
	if vers, _, ok := load(); ok {
		return vers, true, nil
	}
	return nil, true, ferr
}

// InvalidateCache 删除某工具的索引缓存文件。
func InvalidateCache(ad Adapter, s *settings.Settings) {
	os.Remove(filepath.Join(settings.CacheDir(s), ad.ID()+".json"))
}

// ForceRefresh 立即重拉远程索引并回写缓存，返回版本数。
func ForceRefresh(ad Adapter, s *settings.Settings) (int, error) {
	InvalidateCache(ad, s)
	vers, err := ad.FetchRemote(s)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(settings.CacheDir(s), ad.ID()+".json")
	if data, e := json.Marshal(cacheFile{FetchedAt: time.Now().UnixMilli(), Versions: vers}); e == nil {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if os.WriteFile(path+".tmp", data, 0o644) == nil {
			os.Rename(path+".tmp", path)
		}
	}
	return len(vers), nil
}
