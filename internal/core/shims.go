package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/MUHUIX/envswitch/internal/settings"
)

// EnsureShims 为已激活的 JS 工具生成/更新 bin\shims\*.cmd。
// 内容幂等：无变化不重写。命令名按字典序生成，保证可重现。
func EnsureShims(s *settings.Settings, ads []Adapter) error {
	dir := settings.ShimsDir(s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, ad := range ads {
		jr, ok := ad.(JSRunner)
		if !ok {
			continue
		}
		names := make([]string, 0, len(jr.Shims()))
		for n := range jr.Shims() {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			entry := jr.Shims()[name]
			content := shimScript(ad.ID(), entry)
			path := filepath.Join(dir, name+".cmd")
			if old, err := os.ReadFile(path); err == nil && string(old) == content {
				continue // 幂等：内容相同不重写（模板升级会自动重写）
			}
			if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
				return fmt.Errorf("写 shim %s: %w", name, err)
			}
		}
	}
	return nil
}

// PathExeName 返回该工具在 PATH 上应出现的可执行文件名（用于 which/status 解析）。
func PathExeName(ad Adapter) string {
	if jr, ok := ad.(JSRunner); ok {
		s := jr.Shims()
		name := ad.ID()
		if _, has := s[name]; !has {
			var ks []string
			for k := range s {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			if len(ks) > 0 {
				name = ks[0]
			}
		}
		return name + ".cmd"
	}
	exe, _ := ad.Smoke()
	if exe == "" {
		// Maven / Gradle 的冒烟交给用户环境（依赖 JAVA_HOME），但仍要能在 PATH 上被识别。
		switch ad.ID() {
		case "maven":
			return "mvn.cmd"
		case "gradle":
			return "gradle.bat"
		}
		return ""
	}
	return filepath.Base(exe)
}

// shimScript 稳定路径模板：永远指向 current\<tool>\<入口js>；
// shims 在 home\bin\shims，上跳两级得 home；激活 node 缺失回退系统 node。
// 实测定型三条铁律：① IF (...) ELSE 括号块内 & 解析炸 → 用 GOTO；
// ② IF EXIST 不规范化 .. → 先 FOR %%~fI 展平；③ bat 中文按 GBK 解码乱码 → 提示语用英文。
func shimScript(tool, entry string) string {
	target := `%ES%\current\` + tool + `\` + filepath.ToSlash(entry)
	return "@ECHO off\r\n" +
		"SETLOCAL\r\n" +
		"SET \"D=%~dp0..\\..\"\r\n" +
		"FOR %%I IN (\"%D%\") DO SET \"ES=%%~fI\"\r\n" +
		"IF NOT EXIST \"" + target + "\" GOTO :envs_missing\r\n" +
		"SET \"NODE=%ES%\\current\\node\\node.exe\"\r\n" +
		"IF NOT EXIST \"%NODE%\" SET \"NODE=node\"\r\n" +
		"\"%NODE%\" \"" + target + "\" %*\r\n" +
		"EXIT /B %ERRORLEVEL%\r\n" +
		":envs_missing\r\n" +
		"ECHO EnvSwitch: " + tool + " is not activated. Run: envs use " + tool + " ^<ver^>\r\n" +
		"EXIT /B 78\r\n"
}
