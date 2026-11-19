package core

import (
	"strings"
	"testing"
)

func TestParseSHASUMS(t *testing.T) {
	text := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  node-v22.22.0-win-x64.zip\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb *node-v22.22.0-headers.tar.gz\n"
	if got, ok := ParseSHASUMS(text, "node-v22.22.0-win-x64.zip"); !ok || !strings.HasPrefix(got, "aaaa") {
		t.Fatalf("双空格格式失败: %q %v", got, ok)
	}
	if got, ok := ParseSHASUMS(text, "node-v22.22.0-headers.tar.gz"); !ok || !strings.HasPrefix(got, "bbbb") {
		t.Fatalf("星号格式失败: %q %v", got, ok)
	}
	if _, ok := ParseSHASUMS(text, "missing.zip"); ok {
		t.Fatal("不应匹配缺失文件")
	}
}

func TestCompareAndMatch(t *testing.T) {
	if CompareVersions("22.22.0", "22.9.1") <= 0 {
		t.Fatal("22.22.0 应大于 22.9.1")
	}
	if CompareVersions("v3.13.0+build", "3.13.0") != 0 {
		t.Fatal("构建号应忽略")
	}
	vers := []string{"25.2.0", "22.22.0", "22.21.1", "20.19.0", "16.20.2"}
	if got, err := MatchVersion(vers, "22"); err != nil || got != "22.22.0" {
		t.Fatalf("major 别名应取最新 22.x: %q %v", got, err)
	}
	if got, err := MatchVersion(vers, "latest"); err != nil || got != "25.2.0" {
		t.Fatalf("latest: %q %v", got, err)
	}
	if got, err := MatchVersion(vers, "22.21"); err != nil || got != "22.21.1" {
		t.Fatalf("minor 前缀: %q %v", got, err)
	}
	if _, err := MatchVersion(vers, "99"); !strings.Contains(err.Error(), "没有匹配") {
		t.Fatal("未匹配应报错")
	}
}

func TestNormAndIndexOfVersion(t *testing.T) {
	if NormVersion(" v16.20.2 ") != "16.20.2" || NormVersion("16.20.2") != "16.20.2" {
		t.Fatalf("规范化失败: %q", NormVersion(" v16.20.2 "))
	}
	// 收编列表里已有 16.20.2，PATH 检测到的同版本必须命中它（下标 1），不能再追加一行
	installs := []Install{
		{Version: "22.22.0", Source: "nvm"},
		{Version: "16.20.2", Source: "nvm", BinDir: `E:\nvm\v16.20.2`},
	}
	if i := IndexOfVersion(installs, "v16.20.2"); i != 1 {
		t.Fatalf("v 前缀应命中同一条: got %d", i)
	}
	if i := IndexOfVersion(installs, "16.20.2"); i != 1 {
		t.Fatalf("精确应命中: got %d", i)
	}
	if i := IndexOfVersion(installs, "20.19.0"); i != -1 {
		t.Fatalf("未安装应返回 -1: got %d", i)
	}
	if i := IndexOfVersion(installs, ""); i != -1 || IndexOfVersion(installs, "20") != -1 {
		t.Fatal("空串与 major 别名不算同一条（否则 20.x 会被误合并）")
	}
}

func TestShimScriptStable(t *testing.T) {
	s := shimScript("npm", "bin/npm-cli.js", "npm", shimExePath())
	// 原断言里的 `EXIT /B 78` 已按 §9-1 废除，换成新模板的三个必要片段：
	// 延迟展开在位、envs.exe 绝对路径注入位、无扩展名形态存在且为 LF。
	// 其余六项（@ECHO off / target 路径 / "%NODE%" / GOTO :envs_missing / 禁括号块 / 幂等）一项不减。
	for _, want := range []string{
		`@ECHO off`, `%ES%\current\npm\bin/npm-cli.js`, `"%NODE%"`, `GOTO :envs_missing`,
		`SETLOCAL ENABLEDELAYEDEXPANSION`, `SET "EXE=` + shimExePath() + `"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("shim 缺关键片段 %q:\n%s", want, s)
		}
	}
	sh := posixShimScript("npm", "bin/npm-cli.js", "npm", shimExePath())
	if !strings.HasPrefix(sh, "#!/usr/bin/env sh\n") || strings.Contains(sh, "\r") {
		t.Fatal("无扩展名 shim 必须是 #!/usr/bin/env sh 开头且纯 LF 行尾（§6.2-4）")
	}
	if strings.Contains(s, ") ELSE (") || strings.Contains(s, "1>&2") {
		t.Fatal("shim 禁止 IF 括号块与括号内重定向（cmd 解析陷阱）")
	}
	if s != shimScript("npm", "bin/npm-cli.js", "npm", shimExePath()) {
		t.Fatal("shim 生成应幂等")
	}
}

func TestUnpackZipSlip(t *testing.T) {
	// 危险路径检测在 unzip 内，tar 同理；此处验证 flatten 行为
	t.Skip("zip fixture 需生成器，集成阶段覆盖")
}
