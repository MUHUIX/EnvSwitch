package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/HUIdada1/envswitch/internal/core"
	"github.com/HUIdada1/envswitch/internal/settings"
)

// packument —— npm registry 缩写元数据：
// {"dist-tags":{"latest":"x.y.z"},"versions":{"x.y.z":{"dist":{"tarball","integrity","unpackedSize"}}},"time":{...}}
type packument struct {
	DistTags map[string]string       `json:"dist-tags"`
	Versions map[string]packuVersion `json:"versions"`
	Time     map[string]string       `json:"time"`
}

type packuVersion struct {
	Version string `json:"version"`
	Dist    struct {
		Tarball      string `json:"tarball"`
		Integrity    string `json:"integrity"`
		Shasum       string `json:"shasum"`
		UnpackedSize int64  `json:"unpackedSize"`
	} `json:"dist"`
}

// fetchNpmVersions 拉某包的版本列表（跳过 prerelease）。registry 包的校验
// 语义 = tarball sha512 必须与 integrity 一致（FetchURL 后由 InstallPipeline 执行）。
func fetchNpmVersions(ctx context.Context, s *settings.Settings, pkg string) ([]core.RemoteVersion, error) {
	base := regBase(s, pkg)
	raw, err := core.GetBytesAccept(ctx, base+"/"+pkg, "application/vnd.npm.install-v1+json")
	if err != nil {
		return nil, err
	}
	var p packument
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, core.W(core.ErrNet, "%s packument 解析失败: %v", pkg, err)
	}
	latest := p.DistTags["latest"]
	var out []core.RemoteVersion
	for ver, v := range p.Versions {
		if strings.Contains(ver, "-") {
			continue // 跳过 rc/next/beta
		}
		rv := core.RemoteVersion{
			Version:   ver,
			URL:       v.Dist.Tarball,
			Size:      v.Dist.UnpackedSize,
			Integrity: v.Dist.Integrity,
			Archive:   fmt.Sprintf("%s-%s.tgz", pkg, ver),
		}
		// registry 双域互备（npmmirror 偶发滞后 404）
		if strings.HasPrefix(rv.URL, npmRegistryMirror) {
			rv.Alts = []string{npmRegistryOfficial + rv.URL[len(npmRegistryMirror):]}
		} else if strings.HasPrefix(rv.URL, npmRegistryOfficial) {
			rv.Alts = []string{npmRegistryMirror + rv.URL[len(npmRegistryOfficial):]}
		}
		if ver == latest {
			rv.Label = "latest"
		}
		if t := p.Time[ver]; len(t) >= 10 {
			rv.Date = t[:10]
		}
		out = append(out, rv)
	}
	sortByVersionDesc(out)
	return out, nil
}

func sortByVersionDesc(list []core.RemoteVersion) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && core.CompareVersions(list[j].Version, list[j-1].Version) > 0; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
