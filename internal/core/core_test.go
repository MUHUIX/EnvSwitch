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

func TestShimScriptStable(t *testing.T) {
	s := shimScript("npm", "bin/npm-cli.js")
	for _, want := range []string{`@ECHO off`, `%ES%\current\npm\bin/npm-cli.js`, `"%NODE%"`, `EXIT /B 78`, `GOTO :envs_missing`} {
		if !strings.Contains(s, want) {
			t.Fatalf("shim 缺关键片段 %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, ") ELSE (") || strings.Contains(s, "1>&2") {
		t.Fatal("shim 禁止 IF 括号块与括号内重定向（cmd 解析陷阱）")
	}
	if s != shimScript("npm", "bin/npm-cli.js") {
		t.Fatal("shim 生成应幂等")
	}
}

func TestUnpackZipSlip(t *testing.T) {
	// 危险路径检测在 unzip 内，tar 同理；此处验证 flatten 行为
	t.Skip("zip fixture 需生成器，集成阶段覆盖")
}
