<script setup>
import { ref, computed, inject, onMounted } from 'vue'
import { api, openExternal } from '../bridge'
import Icon from '../components/Icon.vue'

const toast = inject('toast')
const isDark = inject('isDark', ref(true))
const st = ref(null)
const checkingUpdate = ref(false)
const updateInfo = ref(null)
const updateMsg = ref('')
const applyingUpdate = ref(false)

// 构建版本号统一带 v 前缀（-ldflags 注入的是 tag 名），展示层不再重复拼 v；
// dev 等非数字版本保持原样，避免出现 "vdev"。
function tagVer(v) {
  if (!v) return ''
  const s = String(v)
  return /^\d/.test(s) ? `v${s}` : s
}
const verText = computed(() => tagVer(st.value?.version || '1.0.0'))

onMounted(async () => {
  try {
    st.value = await api.settings()
    const p = await api.pendingUpdate()
    if (p?.available) {
      updateInfo.value = p
      updateMsg.value = `发现新版本 ${tagVer(p.version)}`
    }
  } catch (err) {
    // 降级保护
  }
})

async function checkUpdate() {
  checkingUpdate.value = true
  updateMsg.value = '正在检查更新…'
  try {
    const res = await api.checkUpdateNow()
    updateInfo.value = res
    if (res?.available) {
      updateMsg.value = `发现新版本 ${tagVer(res.version)}`
    } else if (res?.error) {
      updateMsg.value = `检查失败：${res.error}`
    } else {
      updateMsg.value = '当前已是最新版本'
    }
  } catch (e) {
    updateMsg.value = `检查失败：${e}`
  } finally {
    checkingUpdate.value = false
  }
}

async function doApplyUpdate() {
  applyingUpdate.value = true
  try {
    await api.applyUpdate()
  } catch (e) {
    applyingUpdate.value = false
    toast(String(e), 'err')
  }
}

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

// 反馈跳转：用系统默认浏览器打开 GitHub 的 issue 表单——应用内打开拿不到 GitHub
// 登录态，也没法提交表单；模板已在仓库 .github/ISSUE_TEMPLATE 下预设好，填完直接提交。
const REPO = 'https://github.com/MUHUIX/EnvSwitch'

function submitFeedback() {
  openExternal(`${REPO}/issues/new?template=bug_report.yml`)
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
      </span>
    </header>

    <div class="scroll">
      <div class="settings-wrap">
        <!-- 通用设置 -->
        <section class="cfg-group">
          <div class="group-header">
            <h4 class="group-title">通用设置</h4>
          </div>

          <div class="cfg-panel">
            <div class="cfg-row clickable" @click="toggleAuto">
              <div class="cfg-info">
                <span class="cfg-label">开机自启</span>
                <span class="cfg-desc">写入当前用户注册表 HKCU Run 键，全程免管理员权限</span>
              </div>
              <div class="cfg-ctrl" @click.stop>
                <button
                  type="button"
                  class="toggle"
                  :class="{ on: st.autostart }"
                  :aria-checked="st.autostart"
                  role="switch"
                  @click="toggleAuto"
                >
                  <span class="toggle-ball"></span>
                </button>
              </div>
            </div>

            <div class="cfg-row clickable" @click="toggleField('closeToTray')">
              <div class="cfg-info">
                <span class="cfg-label">关闭主窗口时最小化到系统托盘</span>
                <span class="cfg-desc">后台极轻常驻（&lt;30MB），托盘右键即可免开窗秒切版本</span>
              </div>
              <div class="cfg-ctrl" @click.stop>
                <button
                  type="button"
                  class="toggle"
                  :class="{ on: st.closeToTray }"
                  :aria-checked="st.closeToTray"
                  role="switch"
                  @click="toggleField('closeToTray')"
                >
                  <span class="toggle-ball"></span>
                </button>
              </div>
            </div>

            <div class="cfg-row clickable" @click="toggleField('autoCheckUpdate')">
              <div class="cfg-info">
                <span class="cfg-label">自动检查更新</span>
                <span class="cfg-desc">开启后启动时及每小时自动检查更新（仅提醒通知，由您决定是否安装）</span>
              </div>
              <div class="cfg-ctrl" @click.stop>
                <button
                  type="button"
                  class="toggle"
                  :class="{ on: st.autoCheckUpdate }"
                  :aria-checked="st.autoCheckUpdate"
                  role="switch"
                  @click="toggleField('autoCheckUpdate')"
                >
                  <span class="toggle-ball"></span>
                </button>
              </div>
            </div>
          </div>
        </section>

        <!-- 软件更新 -->
        <section class="cfg-group">
          <div class="group-header">
            <h4 class="group-title">软件更新</h4>
          </div>

          <div class="cfg-panel">
            <div class="cfg-row">
              <div class="cfg-info">
                <div class="ver-state-line">
                  <span class="cfg-label">EnvSwitch 客户端</span>
                  <span class="size-pill num">{{ verText }}</span>
                </div>
                <span class="cfg-desc" v-if="updateMsg">{{ updateMsg }}</span>
                <span class="cfg-desc" v-else>自动定时检测新版本，安装由您自主决定</span>
              </div>
              <div class="cfg-ctrl">
                <button
                  class="btn sm"
                  :class="{ ghost: !updateInfo?.available, primary: updateInfo?.available }"
                  :disabled="checkingUpdate"
                  @click="checkUpdate"
                >
                  <Icon name="refresh" :size="13" />
                  <span>{{ checkingUpdate ? '正在检查…' : '检查更新' }}</span>
                </button>
              </div>
            </div>

            <!-- 检测到新版本时的详细确认区 -->
            <div class="update-card-box" v-if="updateInfo?.available">
              <div class="update-card-left">
                <div class="new-ver-title">新版本 {{ tagVer(updateInfo.version) }} 已准备就绪</div>
                <div class="new-ver-notes" v-if="updateInfo.notes">{{ updateInfo.notes }}</div>
                <div class="new-ver-meta">
                  <span>安装包大小：{{ updateInfo.size }}</span>
                  <span class="dot">·</span>
                  <span>原地秒级热替换，崩溃自愈回滚</span>
                </div>
              </div>
              <button
                class="btn primary"
                :disabled="applyingUpdate"
                @click="doApplyUpdate"
              >
                <Icon name="down" :size="14" />
                <span>{{ applyingUpdate ? '正在更新…' : '立即更新' }}</span>
              </button>
            </div>
          </div>
        </section>

        <!-- 网络与索引缓存 -->
        <section class="cfg-group">
          <div class="group-header">
            <h4 class="group-title">网络与代理加速</h4>
          </div>

          <div class="cfg-panel">
            <div class="cfg-row">
              <div class="cfg-info">
                <span class="cfg-label">GitHub 下载代理加速前缀</span>
                <span class="cfg-desc">加速 GitHub Releases 资产下载，例如 https://ghproxy.net/，留空走直连</span>
              </div>
              <div class="cfg-ctrl">
                <input
                  class="cfg-input proxy-input"
                  v-model="st.proxy"
                  placeholder="https://ghproxy.net/"
                  @change="setKv('proxy', st.proxy)"
                />
              </div>
            </div>

            <div class="cfg-row">
              <div class="cfg-info">
                <span class="cfg-label">远程版本索引缓存时长</span>
                <span class="cfg-desc">减少重复网络请求，加速版本列表秒级呈现（单位：分钟）</span>
              </div>
              <div class="cfg-ctrl inline-ctrl">
                <input
                  type="number"
                  min="1"
                  max="1440"
                  class="cfg-input num-input num"
                  v-model.number="st.indexCacheMinutes"
                  @change="setKv('indexCacheMinutes', st.indexCacheMinutes)"
                />
                <span class="unit-text">分钟</span>
              </div>
            </div>
          </div>
        </section>

        <!-- 常用工具镜像源（紧凑网格布局） -->
        <section class="cfg-group">
          <div class="group-header">
            <h4 class="group-title">多语言与工具链下载镜像源</h4>
          </div>

          <div class="mirrors-grid">
            <div
              v-for="(opts, name) in mirrorOpts"
              :key="name"
              class="mirror-card"
            >
              <div class="mirror-head">
                <Icon :name="name" :size="16" />
                <span class="mirror-name">{{ name }}</span>
              </div>
              <div class="mirror-body">
                <select
                  class="cfg-select mirror-select"
                  :value="st.mirrors[name] || opts[0]"
                  @change="setKv('mirror.' + name, $event.target.value)"
                >
                  <option v-for="o in opts" :key="o" :value="o">{{ o }}</option>
                </select>
              </div>
            </div>
          </div>
        </section>

        <!-- 本地存储与版本库 -->
        <section class="cfg-group">
          <div class="group-header">
            <h4 class="group-title">本地存储与版本库</h4>
          </div>

          <div class="cfg-panel">
            <div class="cfg-row">
              <div class="cfg-info">
                <span class="cfg-label">版本库实际存储目录</span>
                <span class="cfg-desc">各个工具版本文件的物理安装路径</span>
              </div>
              <div class="cfg-ctrl path-row">
                <input class="cfg-input wide-input num" :value="st.storePath" readonly />
                <button class="btn sm ghost open-dir-btn" @click="api.openStoreDir()" title="在文件资源管理器中打开">
                  <Icon name="folder" :size="13" />
                  <span>打开目录</span>
                </button>
              </div>
            </div>

            <div class="cfg-row">
              <div class="cfg-info">
                <span class="cfg-label">已安装工具版本占用</span>
                <span class="cfg-desc">本机存储的所有开发环境版本文件总大小</span>
              </div>
              <div class="cfg-ctrl">
                <span class="size-pill num">{{ st.totalSize || '0 B' }}</span>
              </div>
            </div>
          </div>
        </section>

        <!-- 反馈与需求 -->
        <section class="cfg-group">
          <div class="group-header">
            <h4 class="group-title">反馈与需求</h4>
          </div>

          <div class="cfg-panel">
            <div class="cfg-row">
              <div class="cfg-info">
                <span class="cfg-label">问题反馈</span>
                <span class="cfg-desc">功能异常、报错、版本切换不生效、界面错位等，均可在 GitHub 提交问题单</span>
              </div>
              <div class="cfg-ctrl">
                <button class="btn sm ghost" @click="submitFeedback">
                  <Icon name="github" :size="13" />
                  <span>提交反馈</span>
                </button>
              </div>
            </div>
          </div>
        </section>

        <!-- 关于软件与作者 -->
        <section class="cfg-group">
          <div class="group-header">
            <h4 class="group-title">关于软件</h4>
          </div>

          <div class="cfg-panel about-panel">
            <div class="about-card">
              <div class="about-logo-box" :class="{ 'is-light': !isDark }">
                <img
                  class="about-logo dark-logo"
                  :class="{ active: isDark }"
                  src="/logo-dark.png"
                  alt="EnvSwitch"
                />
                <img
                  class="about-logo light-logo"
                  :class="{ active: !isDark }"
                  src="/logo-light.png"
                  alt="EnvSwitch"
                />
              </div>

              <div class="about-content">
                <div class="about-head">
                  <span class="about-name">EnvSwitch</span>
                  <span class="about-badge ver-num">{{ verText }}</span>
                  <span class="about-badge author-badge">作者：沐晖</span>
                </div>
                <p class="about-intro">
                  全栈开发者多语言与工具链版本极速切换台 · Junction 毫秒重指 · 免命令行 · 零管理员权限依赖
                </p>
                <div class="about-meta">
                  <span>授权：MIT + 附加使用条款</span>
                  <span class="dot">·</span>
                  <a
                    class="repo-link"
                    href="javascript:void(0)"
                    @click="openExternal('https://github.com/MUHUIX/EnvSwitch')"
                  >
                    GitHub 源码仓库
                  </a>
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-wrap {
  display: flex;
  flex-direction: column;
  gap: 22px;
  max-width: 920px;
  padding-bottom: 24px;
}

.cfg-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.group-header {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding-left: 2px;
}

.group-title {
  font-size: 13.5px;
  font-weight: 700;
  color: var(--ink-1);
  letter-spacing: -0.01em;
}

.cfg-panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  overflow: hidden;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04);
}

.cfg-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--hairline);
  transition: background-color 0.15s var(--ease);
  gap: 16px;
}

.cfg-row:last-child {
  border-bottom: none;
}

.cfg-row.clickable {
  cursor: pointer;
}

.cfg-row.clickable:hover {
  background: var(--surface-soft);
}

.cfg-info {
  display: flex;
  flex-direction: column;
  gap: 3px;
  flex: 1;
}

.cfg-label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.cfg-desc {
  font-size: 11px;
  color: var(--ink-3);
  line-height: 1.4;
}

.cfg-ctrl {
  flex: none;
  display: flex;
  align-items: center;
}

/* ================== 新版开关按钮（Toggle Switch） ================== */
.toggle {
  width: 44px;
  height: 24px;
  border-radius: 999px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline-strong);
  position: relative;
  cursor: pointer;
  outline: none;
  padding: 0;
  display: block;
  transition: background-color 0.22s var(--ease), border-color 0.22s var(--ease), box-shadow 0.22s var(--ease);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.12);
}

.toggle:hover {
  border-color: var(--ink-3);
}

.toggle:focus-visible {
  box-shadow: 0 0 0 2px var(--accent-line), inset 0 1px 2px rgba(0, 0, 0, 0.12);
}

.toggle.on {
  background: var(--accent);
  border-color: var(--accent);
  box-shadow: 0 0 10px rgba(63, 191, 168, 0.35), inset 0 1px 1px rgba(255, 255, 255, 0.2);
}

.toggle-ball {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: #ffffff;
  position: absolute;
  top: 2px;
  left: 2px;
  box-shadow: 0 2px 5px rgba(0, 0, 0, 0.25), 0 0 1px rgba(0, 0, 0, 0.15);
  transition: transform 0.24s cubic-bezier(0.34, 1.56, 0.64, 1), width 0.18s ease;
  pointer-events: none;
}

.toggle:active .toggle-ball {
  width: 21px;
}

.toggle.on .toggle-ball {
  transform: translateX(20px);
}

.toggle.on:active .toggle-ball {
  transform: translateX(17px);
}

/* ================== 输入与选择控件 ================== */
.cfg-input {
  height: 30px;
  padding: 0 10px;
  border-radius: var(--r-ctl);
  border: 1px solid var(--hairline);
  background: var(--surface-soft);
  color: var(--ink-1);
  font-size: 12px;
  outline: none;
  transition: all 0.15s var(--ease);
}

.cfg-input:hover {
  border-color: var(--hairline-strong);
}

.cfg-input:focus {
  border-color: var(--accent);
  background: var(--surface);
  box-shadow: 0 0 0 2px var(--accent-dim);
}

.proxy-input {
  width: 280px;
}

.num-input {
  width: 80px;
  text-align: center;
}

.inline-ctrl {
  display: flex;
  align-items: center;
  gap: 8px;
}

.unit-text {
  font-size: 12px;
  color: var(--ink-3);
}

.cfg-select {
  height: 28px;
  padding: 0 10px;
  border-radius: var(--r-ctl);
  border: 1px solid var(--hairline);
  background: var(--surface-soft);
  color: var(--ink-1);
  font-size: 11.5px;
  outline: none;
  cursor: pointer;
  transition: all 0.15s var(--ease);
}

.cfg-select:hover {
  border-color: var(--hairline-strong);
  background: var(--surface-raised);
}

.cfg-select:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px var(--accent-dim);
}

/* ================== 镜像源网格布局 ================== */
.mirrors-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 10px;
}

.mirror-card {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 8px 12px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  transition: all 0.15s var(--ease);
}

.mirror-card:hover {
  background: var(--surface-raised);
  border-color: var(--hairline-strong);
}

.mirror-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.mirror-name {
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-1);
}

.mirror-body {
  flex: none;
}

.mirror-select {
  width: 140px;
  height: 26px;
  font-size: 11px;
}

/* ================== 本地存储与大小 ================== */
.path-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.wide-input {
  width: 320px;
}

.open-dir-btn {
  height: 30px;
  padding: 0 10px;
  gap: 6px;
}

.size-pill {
  font-size: 13px;
  font-weight: 700;
  color: var(--accent);
  background: var(--accent-dim);
  border: 1px solid var(--accent-line);
  padding: 3px 10px;
  border-radius: 6px;
}

/* ================== 关于面板 ================== */
.about-panel {
  padding: 16px;
}

.about-card {
  display: flex;
  align-items: center;
  gap: 16px;
}

.about-logo-box {
  position: relative;
  width: 52px;
  height: 52px;
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.2);
  flex: none;
  background: var(--surface-raised);
  transition: box-shadow 0.25s var(--ease);
}

.about-logo-box.is-light {
  box-shadow: 0 3px 10px rgba(0, 0, 0, 0.08), 0 0 0 1px rgba(0, 0, 0, 0.05);
}

.about-logo {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  border-radius: 12px;
  object-fit: cover;
  opacity: 0;
  transform: scale(0.94);
  transition: opacity 0.25s var(--ease), transform 0.25s var(--ease);
  pointer-events: none;
}

.about-logo.active {
  opacity: 1;
  transform: scale(1);
}

.about-content {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.about-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.about-name {
  font-size: 16px;
  font-weight: 700;
  color: var(--ink-1);
}

.about-badge {
  font-size: 11px;
  padding: 1px 7px;
  border-radius: 5px;
  font-weight: 600;
}

.about-badge.ver-num {
  background: var(--accent-dim);
  border: 1px solid var(--accent-line);
  color: var(--accent-ink);
  font-family: var(--font-num);
}

.ver-state-line {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 2px;
}

.update-card-box {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  margin: 0 16px 14px;
  background: var(--surface-soft);
  border: 1px solid var(--accent-line);
  border-radius: var(--r-ctl);
  gap: 16px;
}

.update-card-left {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.new-ver-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-1);
}

.new-ver-notes {
  font-size: 12px;
  color: var(--ink-2);
}

.new-ver-meta {
  font-size: 11px;
  color: var(--ink-3);
  display: flex;
  align-items: center;
  gap: 6px;
}

.about-badge.author-badge {
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  color: var(--ink-2);
}

.about-intro {
  font-size: 11.5px;
  color: var(--ink-2);
  line-height: 1.4;
  margin: 0;
}

.about-meta {
  font-size: 11px;
  color: var(--ink-3);
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 2px;
}

.about-meta .dot {
  opacity: 0.5;
}

.repo-link {
  color: var(--accent);
  text-decoration: none;
  font-weight: 500;
  transition: color 0.15s ease;
}

.repo-link:hover {
  text-decoration: underline;
}
</style>
