<script setup>
import { ref, inject, onMounted } from 'vue'
import { api, openExternal } from '../bridge'
import Icon from '../components/Icon.vue'

const toast = inject('toast')
const st = ref(null)

onMounted(async () => {
  try {
    st.value = await api.settings()
  } catch (err) {
    // 降级保护
  }
})

async function setKv(k, v) {
  try {
    await api.setField(k, v)
    toast('设置已保存')
  } catch (e) {
    toast(String(e), 'err')
  }
}

async function toggleAuto() {
  const on = !st.value.autostart
  try {
    await api.setAutostart(on)
    st.value.autostart = on
    toast(on ? '开机自启已开启' : '开机自启已关闭')
  } catch (e) {
    toast(String(e), 'err')
  }
}

async function toggleField(name) {
  st.value[name] = !st.value[name]
  await setKv(name, st.value[name])
}

const mirrorOpts = {
  node: ['npmmirror', 'official'],
  npm: ['npmmirror', 'official'],
  pnpm: ['npmmirror', 'official'],
  yarn: ['npmmirror', 'official'],
  python: ['auto', 'github', 'nju'],
  go: ['google.cn', 'official', 'aliyun'],
  rust: ['static.rust-lang.org', 'rsproxy.cn'],
  uv: ['astral-sh', 'ghproxy'],
  php: ['windows.php.net'],
  java: ['adoptium'],
  maven: ['archive', 'aliyun'],
  gradle: ['gradle.org'],
  dotnet: ['azureedge'],
  deno: ['github'],
  bun: ['github'],
  kubectl: ['dl.k8s.io'],
  terraform: ['hashicorp']
}
</script>

<template>
  <div class="main" v-if="st">
    <header class="topbar">
      <span class="crumb">
        <b>系统设置</b>
        <span class="crumb-sub">/ 首选项与网络镜像配置</span>
      </span>
      <div class="top-actions">
        <span class="save-tip">修改配置实时生效</span>
      </div>
    </header>

    <div class="scroll">
      <div class="settings-wrap">
        <!-- 通用设置 -->
        <div class="cfg-group">
          <h4 class="group-title">通用设置</h4>
          <div class="cfg-panel">
            <div class="cfg-item">
              <div class="cfg-info">
                <span class="cfg-label">开机自启</span>
                <span class="cfg-desc">写入当前用户注册表 HKCU Run 键，全程免管理员权限</span>
              </div>
              <div class="cfg-ctrl">
                <span class="toggle" :class="{ on: st.autostart }" @click="toggleAuto">
                  <i class="toggle-ball"></i>
                </span>
              </div>
            </div>

            <div class="cfg-item">
              <div class="cfg-info">
                <span class="cfg-label">关闭主窗口时最小化到系统托盘</span>
                <span class="cfg-desc">后台常驻极度轻量（&lt;30MB），托盘右键直接免开窗切换版本</span>
              </div>
              <div class="cfg-ctrl">
                <span class="toggle" :class="{ on: st.closeToTray }" @click="toggleField('closeToTray')">
                  <i class="toggle-ball"></i>
                </span>
              </div>
            </div>

            <div class="cfg-item">
              <div class="cfg-info">
                <span class="cfg-label">启动时检查更新</span>
                <span class="cfg-desc">发现新版自动下载校验，支持原地秒级热替换，崩溃自动回滚</span>
              </div>
              <div class="cfg-ctrl">
                <span class="toggle" :class="{ on: st.checkUpdateOnStart }" @click="toggleField('checkUpdateOnStart')">
                  <i class="toggle-ball"></i>
                </span>
              </div>
            </div>
          </div>
        </div>

        <!-- 镜像源与网络代理 -->
        <div class="cfg-group">
          <h4 class="group-title">镜像源与网络代理</h4>
          <div class="cfg-panel">
            <div class="cfg-item" v-for="(opts, name) in mirrorOpts" :key="name">
              <div class="cfg-info">
                <span class="cfg-label">{{ name }} 下载源</span>
              </div>
              <div class="cfg-ctrl">
                <select
                  class="cfg-select"
                  :value="st.mirrors[name] || opts[0]"
                  @change="setKv('mirror.' + name, $event.target.value)"
                >
                  <option v-for="o in opts" :key="o" :value="o">{{ o }}</option>
                </select>
              </div>
            </div>

            <div class="cfg-item">
              <div class="cfg-info">
                <span class="cfg-label">GitHub 下载代理加速前缀</span>
                <span class="cfg-desc">加速 GitHub 资产下载，例如 https://ghproxy.net/，可留空</span>
              </div>
              <div class="cfg-ctrl">
                <input
                  class="cfg-input"
                  v-model="st.proxy"
                  placeholder="https://ghproxy.net/"
                  @change="setKv('proxy', st.proxy)"
                />
              </div>
            </div>

            <div class="cfg-item">
              <div class="cfg-info">
                <span class="cfg-label">远程版本索引缓存时长（分钟）</span>
                <span class="cfg-desc">减少重复网络请求，加速版本列表呈现</span>
              </div>
              <div class="cfg-ctrl">
                <input
                  type="number"
                  min="1"
                  max="1440"
                  class="cfg-input narrow"
                  v-model="st.indexCacheMinutes"
                  @change="setKv('indexCacheMinutes', st.indexCacheMinutes)"
                />
              </div>
            </div>
          </div>
        </div>

        <!-- 存储管理 -->
        <div class="cfg-group">
          <h4 class="group-title">本地存储与版本库</h4>
          <div class="cfg-panel">
            <div class="cfg-item">
              <div class="cfg-info">
                <span class="cfg-label">版本库实际存储目录</span>
                <span class="cfg-desc">独立隔离存储，切换只修改 Junction 链接，换盘不重复下载</span>
              </div>
              <div class="cfg-ctrl path-row">
                <input class="cfg-input wide num" :value="st.storePath" readonly />
                <button class="btn sm ghost" @click="api.openStoreDir()">
                  <Icon name="folder" :size="13" />
                  <span>打开目录</span>
                </button>
              </div>
            </div>

            <div class="cfg-item">
              <div class="cfg-info">
                <span class="cfg-label">已安装工具版本占用</span>
              </div>
              <div class="cfg-ctrl">
                <span class="size-val num">{{ st.totalSize || '0 B' }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- 关于软件与作者 -->
        <div class="cfg-group">
          <h4 class="group-title">关于 EnvSwitch</h4>
          <div class="cfg-panel">
            <div class="cfg-item about-box">
              <img class="about-logo" src="/logo.png" alt="EnvSwitch" />
              <div class="about-details">
                <div class="about-title">
                  <strong>EnvSwitch</strong>
                  <span class="about-ver">v{{ st.version || '1.0.0' }}</span>
                </div>
                <div class="about-sub">全栈开发者环境版本快速切换台 · 免命令行 · 零管理员依赖</div>
                <div class="about-meta">
                  <span>作者：<strong>沐晖</strong></span>
                  <span class="meta-dot">·</span>
                  <span>开源协议：MIT License</span>
                  <span class="meta-dot">·</span>
                  <a class="repo-link" href="javascript:void(0)" @click="openExternal('https://github.com/HUIdada1/EnvSwitch')">
                    GitHub 仓库
                  </a>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.crumb-sub {
  color: var(--ink-3);
  font-size: 11px;
}

.save-tip {
  font-size: 11px;
  color: var(--ink-3);
}

.settings-wrap {
  display: flex;
  flex-direction: column;
  gap: 20px;
  max-width: 900px;
}

.group-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--ink-1);
  margin-bottom: 8px;
  letter-spacing: 0.01em;
}

.cfg-panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 4px 16px;
}

.cfg-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 0;
  border-bottom: 1px solid var(--hairline);
}

.cfg-item:last-child {
  border-bottom: none;
}

.cfg-info {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.cfg-label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.cfg-desc {
  font-size: 11px;
  color: var(--ink-3);
}

/* 切换开关 */
.toggle {
  width: 38px;
  height: 20px;
  border-radius: 999px;
  background: var(--surface-raised);
  border: 1px solid var(--hairline-strong);
  position: relative;
  cursor: pointer;
  transition: all 0.2s var(--ease);
}

.toggle.on {
  background: var(--accent);
  border-color: var(--accent);
}

.toggle-ball {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #FFFFFF;
  position: absolute;
  top: 2px;
  left: 2px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2);
  transition: transform 0.2s var(--ease);
}

.toggle.on .toggle-ball {
  transform: translateX(18px);
}

.cfg-select {
  height: 28px;
  padding: 0 10px;
  border-radius: var(--r-ctl);
  border: 1px solid var(--hairline);
  background: var(--surface-soft);
  color: var(--ink-1);
  font-size: 12px;
  outline: none;
}

.cfg-input {
  height: 28px;
  padding: 0 10px;
  border-radius: var(--r-ctl);
  border: 1px solid var(--hairline);
  background: var(--surface-soft);
  color: var(--ink-1);
  font-size: 12px;
  outline: none;
}

.cfg-input.narrow {
  width: 80px;
}

.cfg-input.wide {
  width: 280px;
}

.path-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.size-val {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-1);
}

/* 关于区 */
.about-box {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 16px 0 !important;
}

.about-logo {
  width: 48px;
  height: 48px;
  border-radius: 10px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
}

.about-details {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.about-title {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 16px;
  color: var(--ink-1);
}

.about-ver {
  font-size: 12px;
  font-weight: 600;
  color: var(--accent);
  font-family: var(--font-num);
}

.about-sub {
  font-size: 11.5px;
  color: var(--ink-2);
}

.about-meta {
  font-size: 11px;
  color: var(--ink-3);
  display: flex;
  align-items: center;
  gap: 6px;
}

.meta-dot {
  opacity: 0.5;
}

.repo-link {
  color: var(--accent);
  text-decoration: none;
  font-weight: 500;
}

.repo-link:hover {
  text-decoration: underline;
}
</style>
