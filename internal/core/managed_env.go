package core

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// 环境变量托管：把 JAVA_HOME / GOROOT 这类"安装根"变量指向当前版本根。
// 注意不是指向 current\<tool>：java/go/maven 的链接落在 <ver>\bin，而这些变量
// 要的是 <ver> 本身。因此切换/迁移后要按新的链接目标重写一次。
//
// 边界：只碰用户级（HKCU\Environment）、只碰白名单里的变量、写前备份原值。

// 注册表读写留成变量：单测用内存实现替换，绝不在真实环境里读写用户变量。
var (
	readUserEnv   = sys.ReadUserEnv
	writeUserEnv  = sys.WriteUserEnv
	deleteUserEnv = sys.DeleteUserEnv
	broadcastEnv  = sys.BroadcastEnvChange
)

// 托管列表与备份在 settings 里，读写都走这把锁：
// 切换（SyncManagedEnv）与开关（SetManagedEnv）可能来自不同页面。
var managedEnvMu sync.Mutex

type ManagedEnvSpec struct {
	Key  string
	Tool string
	Note string
}

var managedEnvSpecs = []ManagedEnvSpec{
	{Key: "JAVA_HOME", Tool: "java", Note: "JDK 根目录（Maven/Gradle 依赖）"},
	{Key: "GOROOT", Tool: "go", Note: "Go 安装根"},
	{Key: "DOTNET_ROOT", Tool: "dotnet", Note: ".NET 运行时根"},
	{Key: "MAVEN_HOME", Tool: "maven", Note: "Maven 根目录"},
	{Key: "M2_HOME", Tool: "maven", Note: "Maven 2 兼容变量"},
	{Key: "GRADLE_HOME", Tool: "gradle", Note: "Gradle 根目录"},
}

func ManagedEnvSpecByKey(key string) (ManagedEnvSpec, bool) {
	for _, s := range managedEnvSpecs {
		if strings.EqualFold(s.Key, key) {
			return s, true
		}
	}
	return ManagedEnvSpec{}, false
}

func IsManagedEnv(s *settings.Settings, key string) bool {
	managedEnvMu.Lock()
	defer managedEnvMu.Unlock()
	return isManaged(s, key)
}

// ManagedEnvSnapshot 返回托管列表副本。设置页读列表时不能直接取切片：
// 开关可能正在 append，切片头读写会撕裂。
func ManagedEnvSnapshot(s *settings.Settings) []string {
	managedEnvMu.Lock()
	defer managedEnvMu.Unlock()
	return append([]string{}, s.ManagedEnv...)
}

// isManaged 无锁版本，供已持锁的写路径使用。
func isManaged(s *settings.Settings, key string) bool {
	for _, k := range s.ManagedEnv {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// SetManagedEnv 开启/关闭某个变量的托管。
// 开启前要求目标工具已经激活（current junction 存在），否则变量会指向空目录。
func SetManagedEnv(s *settings.Settings, ads []Adapter, key string, on bool) error {
	managedEnvMu.Lock()
	defer managedEnvMu.Unlock()

	spec, ok := ManagedEnvSpecByKey(key)
	if !ok {
		return W(ErrUsage, "不支持托管环境变量 %s", key)
	}
	if on {
		if isManaged(s, key) {
			return nil
		}
		root, alive := managedEnvRoot(ads, s, spec.Tool)
		if !alive {
			return W(ErrUsage, "请先给 %s 切换一个版本，再托管 %s", spec.Tool, key)
		}
		old, had, err := readUserEnv(key)
		if err != nil {
			return err
		}
		if err := writeUserEnv(key, root); err != nil {
			return err
		}
		s.EnvBackup[key] = settings.EnvBackup{Had: had, Value: old}
		s.ManagedEnv = append(s.ManagedEnv, key)
		sort.Strings(s.ManagedEnv)
		if err := s.Save(); err != nil {
			// 写盘失败就把变量退回去，别留下"改了环境但没记备份"的状态
			if had {
				_ = writeUserEnv(key, old)
			} else {
				_ = deleteUserEnv(key)
			}
			delete(s.EnvBackup, key)
			s.ManagedEnv = removeEnvKey(s.ManagedEnv, key)
			return err
		}
		broadcastEnv()
		return nil
	}

	if !isManaged(s, key) {
		return nil
	}
	// 先记下内存状态：Save 失败时要连变量一起回退，不能出现
	// "注册表已恢复原值、配置文件里还写着托管"的分叉。
	prevManaged := append([]string{}, s.ManagedEnv...)
	bak, has := s.EnvBackup[key]
	if has && bak.Had {
		if err := writeUserEnv(key, bak.Value); err != nil {
			return err
		}
	} else {
		if err := deleteUserEnv(key); err != nil {
			return err
		}
	}
	delete(s.EnvBackup, key)
	s.ManagedEnv = removeEnvKey(s.ManagedEnv, key)
	if err := s.Save(); err != nil {
		if root, ok := managedEnvRoot(ads, s, spec.Tool); ok {
			_ = writeUserEnv(key, root)
		}
		s.ManagedEnv = prevManaged
		if has {
			s.EnvBackup[key] = bak
		}
		return err
	}
	broadcastEnv()
	return nil
}

// SyncManagedEnv 把托管变量校准到实际激活链接。目标工具的最后一个版本被删掉后，
// junction 已摘除，这里会自动恢复原值并取消托管 —— 否则变量会永久指向空目录。
func SyncManagedEnv(s *settings.Settings, ads []Adapter) (dropped []string, err error) {
	managedEnvMu.Lock()
	defer managedEnvMu.Unlock()
	if len(s.ManagedEnv) == 0 {
		return nil, nil
	}
	changed := false
	rewritten := false
	for _, key := range append([]string{}, s.ManagedEnv...) {
		spec, ok := ManagedEnvSpecByKey(key)
		if !ok {
			s.ManagedEnv = removeEnvKey(s.ManagedEnv, key)
			changed = true
			continue
		}
		root, alive := managedEnvRoot(ads, s, spec.Tool)
		if !alive {
			bak, has := s.EnvBackup[key]
			if has && bak.Had {
				if e := writeUserEnv(key, bak.Value); e != nil {
					return dropped, e
				}
			} else if e := deleteUserEnv(key); e != nil {
				return dropped, e
			}
			delete(s.EnvBackup, key)
			s.ManagedEnv = removeEnvKey(s.ManagedEnv, key)
			dropped = append(dropped, key)
			changed = true
			continue
		}
		if cur, _, e := readUserEnv(key); e == nil && !strings.EqualFold(strings.TrimSpace(cur), root) {
			if e := writeUserEnv(key, root); e != nil {
				return dropped, e
			}
			rewritten = true
		}
	}
	if changed {
		if e := s.Save(); e != nil {
			return dropped, e
		}
	}
	if len(dropped) > 0 || rewritten {
		broadcastEnv()
	}
	return dropped, nil
}

// managedEnvRoot 返回托管变量应写入的"安装根"。
// 注意 java/go/maven/gradle 的 current 链接直接指向 <ver>\bin（那是进 PATH 的那级），
// 而 JAVA_HOME / GOROOT 这类变量要的是 <ver> 本身，所以 BinSub 非空时要上提一级。
func managedEnvRoot(ads []Adapter, s *settings.Settings, tool string) (string, bool) {
	var ad Adapter
	for _, a := range ads {
		if strings.EqualFold(a.ID(), tool) {
			ad = a
			break
		}
	}
	if ad == nil {
		return "", false
	}
	link := settings.CurrentLink(s, tool)
	target, alive := junctionTarget(link)
	if !alive || target == "" {
		return "", false
	}
	if sub := strings.Trim(ad.BinSub(), `\/`); sub != "" {
		suffix := string(filepath.Separator) + sub
		if strings.HasSuffix(strings.ToLower(target), strings.ToLower(suffix)) {
			return filepath.Dir(target), true
		}
	}
	return target, true
}

func removeEnvKey(list []string, key string) []string {
	out := list[:0]
	for _, k := range list {
		if !strings.EqualFold(k, key) {
			out = append(out, k)
		}
	}
	return out
}
