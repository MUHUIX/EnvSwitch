package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
)

// java —— Eclipse Temurin（Adoptium）。索引一次拿全部 release 名，
// 下载 URL 按官方命名模板拼装，校验走 release 伴生 .sha256.txt。
type Java struct{}

func (Java) ID() string           { return "java" }
func (Java) Title() string        { return "JDK (Temurin)" }
func (Java) BinSub() string       { return "bin" }
func (Java) Smoke() (string, []string) { return "java.exe", []string{"-version"} }

// Adoptium release_type=ga：默认分页会被 jdk-28 EA beta 占满，必须服务器端过滤
const temurinNames = "https://api.adoptium.net/v3/info/release_names?os=windows&arch=x64&image_type=jdk&release_type=ga&sort_order=DESC&page_size=50"

var temurinName = regexp.MustCompile(`^jdk-(\d+(?:\.\d+)*)(?:\+(\d+))?(?:-ea-beta)?$`)

func (Java) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	var resp struct {
		Releases []string `json:"releases"`
	}
	if err := core.GetJSON(context.Background(), temurinNames, &resp); err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	seen := map[string]bool{}
	for _, rn := range resp.Releases {
		if strings.Contains(rn, "beta") {
			continue
		}
		m := temurinName.FindStringSubmatch(rn)
		if m == nil {
			continue
		}
		ver, build := m[1], m[2]
		if ver == "" || seen[ver] {
			continue
		}
		seen[ver] = true
		major := ver
		if i := strings.Index(ver, "."); i > 0 {
			major = ver[:i]
		}
		archive := fmt.Sprintf("OpenJDK%sU-jdk_x64_windows_hotspot_%s_%s.zip", major, ver, build)
		tag := rn
		if build != "" {
			tag = fmt.Sprintf("jdk-%s+%s", ver, build)
		}
		url := fmt.Sprintf("https://github.com/adoptium/temurin%s-binaries/releases/download/%s/%s",
			major, strings.ReplaceAll(tag, "+", "%2B"), archive)
		out = append(out, core.RemoteVersion{
			Version: ver, Size: -1, URL: url, Archive: archive,
		})
	}
	return out, nil
}

// gradle —— services.gradle.org 官方 JSON，downloadUrl 直给。
type Gradle struct{}

func (Gradle) ID() string      { return "gradle" }
func (Gradle) Title() string   { return "Gradle" }
func (Gradle) BinSub() string  { return "bin" }
func (Gradle) Smoke() (string, []string) { return "", nil } // 依赖 JAVA_HOME，安装期只做文件校验
func (Gradle) ProbeFiles() []string { return []string{"bin/gradle.bat", "bin/gradle", "lib"} }

func (Gradle) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	var rels []struct {
		Version     string `json:"version"`
		DownloadURL string `json:"downloadUrl"`
		Snapshot    bool   `json:"snapshot"`
		RcFor       any    `json:"rcFor"`
		MilestoneFor any   `json:"milestoneFor"`
	}
	if err := core.GetJSON(context.Background(), "https://services.gradle.org/versions/all", &rels); err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	for _, r := range rels {
		if r.Snapshot || (isNonEmpty(r.RcFor)) || isNonEmpty(r.MilestoneFor) {
			continue
		}
		out = append(out, core.RemoteVersion{
			Version: r.Version, Size: -1,
			URL: r.DownloadURL, Archive: "gradle-" + r.Version + "-bin.zip",
		})
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out, nil
}

func isNonEmpty(v any) bool {
	s, ok := v.(string)
	return ok && s != ""
}

// maven —— Apache dist（默认阿里云镜像目录，官方 archive 作 Alts）。
type Maven struct{}

func (Maven) ID() string     { return "maven" }
func (Maven) Title() string  { return "Maven" }
func (Maven) BinSub() string { return "bin" }
func (Maven) Smoke() (string, []string) { return "", nil }
func (Maven) ProbeFiles() []string {
	return []string{"bin/mvn.cmd", "bin/mvn", "conf/settings.xml", "lib"}
}

// 目录页 href 有相对（3.9.11/）与全路径两种形态，统一抓末尾版本号目录
var mavenVer = regexp.MustCompile(`href="[^"]*?(\d+\.\d+(?:\.\d+)?)/"`)

// Apache dist 主站与云镜像都只保留每个 minor 的最新补丁版；全量版本在 archive
const (
	mavenAliyunBase = "https://mirrors.aliyun.com/apache/maven/maven-3/"
	mavenApacheBase = "https://archive.apache.org/dist/maven/maven-3/"
)

func (Maven) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	base, alt := mavenApacheBase, mavenAliyunBase
	if s.Mirrors["maven"] == "aliyun" {
		base, alt = mavenAliyunBase, ""
	}
	page, err := core.GetText(context.Background(), base)
	if err != nil {
		page, err = core.GetText(context.Background(), alt)
		if err != nil {
			return nil, err
		}
		base, alt = alt, ""
	}
	var out []core.RemoteVersion
	seen := map[string]bool{}
	for _, m := range mavenVer.FindAllStringSubmatch(page, -1) {
		ver := m[1]
		if !strings.HasPrefix(ver, "3.") || seen[ver] { // 目录页 href 会重复出现
			continue
		}
		seen[ver] = true
		archive := fmt.Sprintf("apache-maven-%s-bin.zip", ver)
		dir := base + ver + "/binaries/" + archive
		var alts []string
		if alt != "" {
			alts = []string{alt + ver + "/binaries/" + archive}
		}
		out = append(out, core.RemoteVersion{
			Version: ver, Size: -1, URL: dir, Alts: alts, Archive: archive,
		})
	}
	sort.Slice(out, func(i, j int) bool { return core.CompareVersions(out[i].Version, out[j].Version) > 0 })
	return out, nil
}

// dotnet —— 官方 releases-index（每通道最新 SDK）+ dotnetcli 模板 + 伴生 .sha512。
type Dotnet struct{}

func (Dotnet) ID() string     { return "dotnet" }
func (Dotnet) Title() string  { return ".NET SDK" }
func (Dotnet) BinSub() string { return "" }
func (Dotnet) Smoke() (string, []string) { return "dotnet.exe", []string{"--version"} }

const dotnetIndex = "https://raw.githubusercontent.com/dotnet/core/main/release-notes/releases-index.json"
const dotnetIndexJS = "https://cdn.jsdelivr.net/gh/dotnet/core@main/release-notes/releases-index.json"
const dotnetCDN = "https://dotnetcli.azureedge.net/dotnet/Sdk/"
const dotnetCDN2 = "https://builds.dotnet.microsoft.com/dotnet/Sdk/"

func (Dotnet) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	var idx struct {
		Releases []struct {
			ChannelVersion string `json:"channel-version"`
			LatestSDK      string `json:"latest-sdk"`
			LTS            bool   `json:"lts"`
		} `json:"releases-index"`
	}
	if err := core.GetJSON(context.Background(), dotnetIndex, &idx); err != nil {
		if err2 := core.GetJSON(context.Background(), dotnetIndexJS, &idx); err2 != nil {
			return nil, err // 报主源错误（信息更准）
		}
	}
	var out []core.RemoteVersion
	for _, r := range idx.Releases {
		if r.LatestSDK == "" { // 无 SDK 的纯 runtime 通道
			continue
		}
		archive := fmt.Sprintf("dotnet-sdk-%s-win-x64.zip", r.LatestSDK)
		label := ""
		if r.LTS {
			label = "LTS"
		}
		out = append(out, core.RemoteVersion{
			Version: r.LatestSDK, Label: label, Size: -1,
			URL: dotnetCDN + r.LatestSDK + "/" + archive,
			Alts:  []string{dotnetCDN2 + r.LatestSDK + "/" + archive},
			Archive: archive,
		})
	}
	return out, nil
}

// kubectl —— dl.k8s.io 单文件 exe（cdn 子域国内 DNS 不可达，302 由 Go 跟随），
// stable-{minor} 系列取近 4 个 minor。
type Kubectl struct{}

func (Kubectl) ID() string     { return "kubectl" }
func (Kubectl) Title() string  { return "kubectl" }
func (Kubectl) BinSub() string { return "" }
func (Kubectl) Smoke() (string, []string) {
	return "kubectl.exe", []string{"version", "--client"}
}

const k8sDL = "https://dl.k8s.io/release/"

func (Kubectl) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	ctx := context.Background()
	first, err := core.GetText(ctx, k8sDL+"stable.txt")
	if err != nil {
		return nil, err
	}
	major, minor, ok := parseK8s(strings.TrimSpace(first))
	if !ok {
		return nil, core.W(core.ErrNet, "k8s stable.txt 格式异常: %q", first)
	}
	var out []core.RemoteVersion
	add := func(v string) {
		archive := "kubectl.exe"
		url := fmt.Sprintf("%sv%s/bin/windows/amd64/kubectl.exe", k8sDL, v)
		out = append(out, core.RemoteVersion{
			Version: strings.TrimPrefix(v, "v"), Size: -1, URL: url, Archive: archive,
		})
	}
	add(strings.TrimSpace(first))
	for i := 0; i < 4 && minor-i >= 24; i++ {
		v, err := core.GetText(ctx, fmt.Sprintf("%sstable-%d.%d.txt", k8sDL, major, minor-i))
		if err != nil {
			continue
		}
		vs := strings.TrimSpace(v)
		if !strings.HasPrefix(vs, "v") {
			continue
		}
		add(vs)
	}
	return out, nil
}

func parseK8s(v string) (int, int, bool) {
	p := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(p) < 2 {
		return 0, 0, false
	}
	a, e1 := strconv.Atoi(p[0])
	b, e2 := strconv.Atoi(p[1])
	return a, b, e1 == nil && e2 == nil
}

// terraform —— HashiCorp 官方 releases API：URL 与 shasum 直给，最干净的一个。
type Terraform struct{}

func (Terraform) ID() string     { return "terraform" }
func (Terraform) Title() string  { return "Terraform" }
func (Terraform) BinSub() string { return "" }
func (Terraform) Smoke() (string, []string) { return "terraform.exe", []string{"version"} }

func (Terraform) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	var rels []struct {
		Version string `json:"version"`
		Builds  []struct {
			Arch   string `json:"arch"`
			OS     string `json:"os"`
			URL    string `json:"url"`
			Shasum string `json:"shasum"`
			Filename string `json:"filename"`
		} `json:"builds"`
	}
	err := core.GetJSON(context.Background(),
		"https://api.releases.hashicorp.com/v1/releases/terraform", &rels)
	if err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	for _, r := range rels {
		for _, b := range r.Builds {
			if b.OS != "windows" || b.Arch != "amd64" {
				continue
			}
			out = append(out, core.RemoteVersion{
				Version: r.Version, Size: -1,
				URL: b.URL, SHA256: b.Shasum, Archive: b.Filename,
			})
			break
		}
		if len(out) >= 80 {
			break
		}
	}
	return out, nil
}
