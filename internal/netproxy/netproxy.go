// Package netproxy 负责管理出站网络代理池、策略选路以及连通性探测。
// 参考 AgentHub 设计，数据落盘在 %LOCALAPPDATA%\EnvSwitch\netproxy.json。
package netproxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MUHUIX/envswitch/internal/settings"
	"golang.org/x/net/proxy"
)

type NetProxyMode string

const (
	ModeOff    NetProxyMode = "off"
	ModeFixed  NetProxyMode = "fixed"
	ModeRotate NetProxyMode = "rotate"
)

const (
	FileName          = "netproxy.json"
	DefaultTestURL    = "https://api.ipify.org?format=json"
	DefaultTimeoutMs  = 15000
	ClashAppID        = "io.github.clash-verge-rev.clash-verge-rev"
)

type NetProxyTest struct {
	Success    bool   `json:"success"`
	Status     int    `json:"status"`
	IP         string `json:"ip"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error"`
	At         int64  `json:"at"`
}

type NetProxyItem struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Protocol    string        `json:"protocol"` // "http" | "socks5"
	Host        string        `json:"host"`
	Port        int           `json:"port"`
	Username    string        `json:"username"`
	Password    string        `json:"password,omitempty"`
	HasPassword bool          `json:"hasPassword"`
	Enabled     bool          `json:"enabled"`
	Source      string        `json:"source"` // "manual" | "clash"
	ClashUID    string        `json:"clashUid,omitempty"`
	LastTest    *NetProxyTest `json:"lastTest,omitempty"`
	CreatedAt   int64         `json:"createdAt"`
	UpdatedAt   int64         `json:"updatedAt"`
}

type NetProxySettings struct {
	TestURL   string `json:"testUrl"`
	TimeoutMs int    `json:"timeoutMs"`
}

type NetProxyConfig struct {
	Version  int              `json:"version"`
	Mode     NetProxyMode     `json:"mode"`
	FixedID  string           `json:"fixedId"`
	Settings NetProxySettings `json:"settings"`
	Items    []NetProxyItem   `json:"items"`
}

type SaveSettingsReq struct {
	Mode      string `json:"mode"`
	FixedID   string `json:"fixedId"`
	TestURL   string `json:"testUrl"`
	TimeoutMs int    `json:"timeoutMs"`
}

type ClashSyncResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Removed int      `json:"removed"`
	Items   []NetProxyItem `json:"items"`
}

var (
	mgrMu       sync.RWMutex
	globalMgr   *Manager
	rotateIndex atomic.Uint64
)

type Manager struct {
	mu   sync.Mutex
	path string
	cfg  NetProxyConfig
}

func GetManager() *Manager {
	mgrMu.Lock()
	defer mgrMu.Unlock()
	if globalMgr == nil {
		p := filepath.Join(settings.Home(), FileName)
		m := &Manager{path: p}
		m.load()
		globalMgr = m
	}
	return globalMgr
}

func emptyConfig() NetProxyConfig {
	return NetProxyConfig{
		Version: 1,
		Mode:    ModeOff,
		FixedID: "",
		Settings: NetProxySettings{
			TestURL:   DefaultTestURL,
			TimeoutMs: DefaultTimeoutMs,
		},
		Items: []NetProxyItem{},
	}
}

func (m *Manager) load() {
	m.cfg = emptyConfig()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var loaded NetProxyConfig
	if err := json.Unmarshal(data, &loaded); err == nil {
		if loaded.Mode != ModeFixed && loaded.Mode != ModeRotate {
			loaded.Mode = ModeOff
		}
		if loaded.Settings.TimeoutMs <= 0 {
			loaded.Settings.TimeoutMs = DefaultTimeoutMs
		}
		if loaded.Settings.TestURL == "" {
			loaded.Settings.TestURL = DefaultTestURL
		}
		for i := range loaded.Items {
			loaded.Items[i].HasPassword = loaded.Items[i].Password != ""
		}
		m.cfg = loaded
	}
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *Manager) GetConfig() NetProxyConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := m.cfg
	cp.Items = make([]NetProxyItem, len(m.cfg.Items))
	for i, item := range m.cfg.Items {
		itemCopy := item
		itemCopy.HasPassword = item.Password != ""
		itemCopy.Password = "" // 不向前端暴露明文
		cp.Items[i] = itemCopy
	}
	return cp
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "np_" + hex.EncodeToString(b)
}

func (m *Manager) SaveItem(draft NetProxyItem) (NetProxyItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := strings.TrimSpace(draft.Name)
	if name == "" {
		return NetProxyItem{}, errors.New("请填写代理名称")
	}
	host := strings.TrimSpace(draft.Host)
	if host == "" {
		return NetProxyItem{}, errors.New("请填写代理主机地址")
	}
	if draft.Port < 1 || draft.Port > 65535 {
		return NetProxyItem{}, errors.New("代理端口必须是 1-65535 的整数")
	}
	proto := strings.ToLower(strings.TrimSpace(draft.Protocol))
	if proto != "socks5" {
		proto = "http"
	}

	now := time.Now().UnixMilli()
	var target NetProxyItem

	if draft.ID == "" {
		// 新增
		target = NetProxyItem{
			ID:          randomID(),
			Name:        name,
			Protocol:    proto,
			Host:        host,
			Port:        draft.Port,
			Username:    strings.TrimSpace(draft.Username),
			Password:    draft.Password,
			HasPassword: draft.Password != "",
			Enabled:     draft.Enabled,
			Source:      "manual",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		m.cfg.Items = append(m.cfg.Items, target)
	} else {
		// 编辑
		idx := -1
		for i, it := range m.cfg.Items {
			if it.ID == draft.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return NetProxyItem{}, fmt.Errorf("找不到出口条目 %s", draft.ID)
		}
		existing := m.cfg.Items[idx]
		existing.Name = name
		existing.Protocol = proto
		existing.Host = host
		existing.Port = draft.Port
		existing.Username = strings.TrimSpace(draft.Username)
		if draft.Password != "" {
			existing.Password = draft.Password
			existing.HasPassword = true
		}
		existing.Enabled = draft.Enabled
		existing.UpdatedAt = now
		m.cfg.Items[idx] = existing
		target = existing
	}

	if err := m.saveLocked(); err != nil {
		return NetProxyItem{}, err
	}
	target.Password = ""
	return target, nil
}

func (m *Manager) RemoveItem(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, it := range m.cfg.Items {
		if it.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}

	m.cfg.Items = append(m.cfg.Items[:idx], m.cfg.Items[idx+1:]...)
	if m.cfg.FixedID == id {
		m.cfg.FixedID = ""
		if m.cfg.Mode == ModeFixed {
			m.cfg.Mode = ModeOff
		}
	}

	return m.saveLocked()
}

func (m *Manager) ToggleItem(id string, enabled bool) (NetProxyItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, it := range m.cfg.Items {
		if it.ID == id {
			m.cfg.Items[i].Enabled = enabled
			m.cfg.Items[i].UpdatedAt = time.Now().UnixMilli()
			if err := m.saveLocked(); err != nil {
				return NetProxyItem{}, err
			}
			res := m.cfg.Items[i]
			res.Password = ""
			return res, nil
		}
	}
	return NetProxyItem{}, fmt.Errorf("找不到出口条目 %s", id)
}

func (m *Manager) SaveSettings(req SaveSettingsReq) (NetProxyConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mode := NetProxyMode(req.Mode)
	if mode != ModeFixed && mode != ModeRotate {
		mode = ModeOff
	}
	if mode == ModeFixed && req.FixedID == "" {
		return m.cfg, errors.New("选择「固定出口」时必须指定一条出口")
	}

	m.cfg.Mode = mode
	m.cfg.FixedID = req.FixedID
	if req.TestURL != "" {
		m.cfg.Settings.TestURL = strings.TrimSpace(req.TestURL)
	}
	if req.TimeoutMs >= 1000 && req.TimeoutMs <= 60000 {
		m.cfg.Settings.TimeoutMs = req.TimeoutMs
	}

	if err := m.saveLocked(); err != nil {
		return m.cfg, err
	}
	return m.cfg, nil
}

// BuildClient 构造指定条目的测试用 HTTP 客户端
func BuildClient(item NetProxyItem, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
	}

	if item.Host != "" && item.Port > 0 {
		addr := net.JoinHostPort(item.Host, strconv.Itoa(item.Port))
		if strings.ToLower(item.Protocol) == "socks5" {
			var auth *proxy.Auth
			if item.Username != "" || item.Password != "" {
				auth = &proxy.Auth{
					User:     item.Username,
					Password: item.Password,
				}
			}
			dialer, err := proxy.SOCKS5("tcp", addr, auth, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("初始化 socks5 dialer 失败: %w", err)
			}
			transport.DialContext = func(ctx context.Context, network, daddr string) (net.Conn, error) {
				return dialer.Dial(network, daddr)
			}
		} else {
			// HTTP/HTTPS 代理
			var u *url.URL
			if item.Username != "" || item.Password != "" {
				u = &url.URL{
					Scheme: "http",
					User:   url.UserPassword(item.Username, item.Password),
					Host:   addr,
				}
			} else {
				u = &url.URL{
					Scheme: "http",
					Host:   addr,
				}
			}
			transport.Proxy = http.ProxyURL(u)
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}

// TestProxy 测试连通性并探查出口 IP
func (m *Manager) TestProxy(draft NetProxyItem) (NetProxyTest, error) {
	// 若是已有条目且 draft.Password 为空，尝试取已存密码
	if draft.ID != "" && draft.Password == "" {
		m.mu.Lock()
		for _, it := range m.cfg.Items {
			if it.ID == draft.ID && it.Password != "" {
				draft.Password = it.Password
				break
			}
		}
		m.mu.Unlock()
	}

	testURL := m.cfg.Settings.TestURL
	if testURL == "" {
		testURL = DefaultTestURL
	}
	timeoutMs := m.cfg.Settings.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	client, err := BuildClient(draft, timeout)
	if err != nil {
		return NetProxyTest{
			Success: false,
			Error:   err.Error(),
			At:      time.Now().UnixMilli(),
		}, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, testURL, nil)
	if err != nil {
		return NetProxyTest{
			Success: false,
			Error:   err.Error(),
			At:      time.Now().UnixMilli(),
		}, nil
	}
	req.Header.Set("User-Agent", "EnvSwitch-NetProxy/1.0")

	resp, err := client.Do(req)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		res := NetProxyTest{
			Success:    false,
			DurationMs: duration,
			Error:      err.Error(),
			At:         time.Now().UnixMilli(),
		}
		m.recordTestResult(draft.ID, res)
		return res, nil
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	exitIP := extractIP(string(bodyBytes))

	// 如果 testURL 没查到 IP，再快速向 ipify 做一次轻量探查（最多 3 秒）
	if exitIP == "" && resp.StatusCode >= 200 && resp.StatusCode < 400 {
		ipCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if ipReq, err := http.NewRequestWithContext(ipCtx, http.MethodGet, "https://api.ipify.org?format=json", nil); err == nil {
			if ipResp, err := client.Do(ipReq); err == nil {
				defer ipResp.Body.Close()
				b, _ := io.ReadAll(io.LimitReader(ipResp.Body, 4096))
				exitIP = extractIP(string(b))
			}
		}
	}

	res := NetProxyTest{
		Success:    true,
		Status:     resp.StatusCode,
		IP:         exitIP,
		DurationMs: duration,
		At:         time.Now().UnixMilli(),
	}
	m.recordTestResult(draft.ID, res)
	return res, nil
}

func (m *Manager) recordTestResult(id string, test NetProxyTest) {
	if id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, it := range m.cfg.Items {
		if it.ID == id {
			m.cfg.Items[i].LastTest = &test
			_ = m.saveLocked()
			break
		}
	}
}

func extractIP(s string) string {
	var parsed struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal([]byte(s), &parsed); err == nil && parsed.IP != "" {
		return parsed.IP
	}
	re := regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	return re.FindString(s)
}

// SyncClash 扫描本地 Clash Verge 配置并导入监听端口
func (m *Manager) SyncClash() (ClashSyncResult, error) {
	dirs := []string{}
	if appData := os.Getenv("APPDATA"); appData != "" {
		dirs = append(dirs, filepath.Join(appData, ClashAppID))
	}
	if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
		dirs = append(dirs, filepath.Join(userProfile, "AppData", "Roaming", ClashAppID))
		dirs = append(dirs, filepath.Join(userProfile, ".config", ClashAppID))
	}

	var foundVerge string
	for _, d := range dirs {
		p := filepath.Join(d, "verge.yaml")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			foundVerge = p
			break
		}
	}

	if foundVerge == "" {
		return ClashSyncResult{
			OK:      false,
			Message: "未在当前系统检测到 Clash Verge 配置文件（已检查 %APPDATA% 常见路径）",
		}, nil
	}

	content, err := os.ReadFile(foundVerge)
	if err != nil {
		return ClashSyncResult{OK: false, Message: fmt.Sprintf("读取 verge.yaml 失败: %v", err)}, nil
	}

	text := string(content)
	mixedPort := 0
	reMixed := regexp.MustCompile(`(?m)^\s*verge_mixed_port:\s*(\d+)`)
	if match := reMixed.FindStringSubmatch(text); len(match) > 1 {
		mixedPort, _ = strconv.Atoi(match[1])
	}
	if mixedPort == 0 {
		// 常见默认端口
		mixedPort = 7897
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	added, updated := 0, 0
	existingClash := -1
	for i, it := range m.cfg.Items {
		if it.Source == "clash" && it.ClashUID == "__mixed__" {
			existingClash = i
			break
		}
	}

	now := time.Now().UnixMilli()
	if existingClash >= 0 {
		m.cfg.Items[existingClash].Host = "127.0.0.1"
		m.cfg.Items[existingClash].Port = mixedPort
		m.cfg.Items[existingClash].Protocol = "http"
		m.cfg.Items[existingClash].UpdatedAt = now
		updated++
	} else {
		newItem := NetProxyItem{
			ID:          randomID(),
			Name:        "Clash 混合代理 (Verge)",
			Protocol:    "http",
			Host:        "127.0.0.1",
			Port:        mixedPort,
			Enabled:     true,
			Source:      "clash",
			ClashUID:    "__mixed__",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		m.cfg.Items = append(m.cfg.Items, newItem)
		added++
	}

	_ = m.saveLocked()

	return ClashSyncResult{
		OK:      true,
		Message: fmt.Sprintf("成功同步 Clash Verge 代理配置（混合端口: %d）", mixedPort),
		Added:   added,
		Updated: updated,
		Items:   m.cfg.Items,
	}, nil
}

// GetActiveProxy 返回当前激活的代理地址（若无则返回 nil）
func GetActiveProxy() (*url.URL, *NetProxyItem) {
	mgr := GetManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if mgr.cfg.Mode == ModeOff {
		return nil, nil
	}

	enabled := make([]NetProxyItem, 0, len(mgr.cfg.Items))
	for _, it := range mgr.cfg.Items {
		if it.Enabled {
			enabled = append(enabled, it)
		}
	}
	if len(enabled) == 0 {
		return nil, nil
	}

	var chosen *NetProxyItem
	if mgr.cfg.Mode == ModeFixed {
		for i := range enabled {
			if enabled[i].ID == mgr.cfg.FixedID {
				chosen = &enabled[i]
				break
			}
		}
		if chosen == nil {
			// 固定项未匹配，兜底取第一个启用项
			chosen = &enabled[0]
		}
	} else if mgr.cfg.Mode == ModeRotate {
		idx := rotateIndex.Add(1) - 1
		chosen = &enabled[idx%uint64(len(enabled))]
	}

	if chosen == nil {
		return nil, nil
	}

	scheme := "http"
	if strings.ToLower(chosen.Protocol) == "socks5" {
		scheme = "socks5"
	}
	addr := net.JoinHostPort(chosen.Host, strconv.Itoa(chosen.Port))
	u := &url.URL{
		Scheme: scheme,
		Host:   addr,
	}
	if chosen.Username != "" || chosen.Password != "" {
		u.User = url.UserPassword(chosen.Username, chosen.Password)
	}
	return u, chosen
}
