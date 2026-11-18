package sys

import "golang.org/x/sys/windows/registry"

// HasWebView2 检测 Evergreen Fixed/Canary Runtime（GUI 依赖）。
func HasWebView2() bool {
	keys := []struct{ root registry.Key; path string }{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`},
	}
	for _, k := range keys {
		h, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err == nil {
			h.Close()
			return true
		}
	}
	return false
}
