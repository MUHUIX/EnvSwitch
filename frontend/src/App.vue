<script setup>
import { ref, computed, provide, onMounted, onUnmounted } from 'vue'
import { api, on } from './bridge'
import Icon from './components/Icon.vue'
import TitleBar from './components/TitleBar.vue'
import Overview from './views/Overview.vue'
import ToolDetail from './views/ToolDetail.vue'
import SettingsView from './views/Settings.vue'
import NetProxyView from './views/NetProxy.vue'

const view = ref({ name: 'overview', tool: null })
const tools = ref([])
const toasts = ref([])
const collapsed = ref(localStorage.getItem('envswitch-rail') === 'collapsed')
const ver = ref('')
const isDark = ref(localStorage.getItem('envswitch-theme') !== 'light')
const hasUpdate = ref(false)
let toastSeq = 0

// 应用主题
function applyTheme(dark) {
  isDark.value = dark
  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light')
  localStorage.setItem('envswitch-theme', dark ? 'dark' : 'light')
}

function toggleTheme() {
  applyTheme(!isDark.value)
}

// 四大标准分类板块
const GROUPS = [
  ['runtime', '语言运行时'],
  ['packages', '包管理与工具链'],
  ['enterprise', '企业运行时 (JVM/.NET)'],
  ['cloud', '云原生与运维']
]

// 侧栏折叠时分组头显示的代表图标（分组 id → icons.js 线性图标名）
const GROUP_ICON = {
  runtime: 'group-runtime',
  packages: 'group-packages',
  enterprise: 'group-enterprise',
  cloud: 'group-cloud'
}

// 托底：映射缺失时退回通用四宫格图标，避免出现空 svg
function groupIcon(gid) {
  return GROUP_ICON[gid] ?? 'dash'
}

// 分组展开/锁起状态持久化：默认全部展开（未记录或为false即为展开）
const groupCollapsedMap = ref({})
try {
  const saved = localStorage.getItem('envswitch-groups-collapsed')
  if (saved) {
    groupCollapsedMap.value = JSON.parse(saved)
  }
} catch (e) {}

function toggleGroup(gid) {
  groupCollapsedMap.value[gid] = !groupCollapsedMap.value[gid]
  localStorage.setItem('envswitch-groups-collapsed', JSON.stringify(groupCollapsedMap.value))
}

function isGroupCollapsed(gid) {
  return !!groupCollapsedMap.value[gid]
}

const grouped = computed(() =>
  GROUPS.map(([id, label]) => {
    const items = tools.value.filter(t => {
      if (id === 'enterprise') {
        return t.group === 'enterprise' || t.group === 'jvm' || t.group === 'dotnet'
      }
      return t.group === id
    })
    return { id, label, items }
  }).filter(g => g.items.length)
)

function goto(name, tool = null) {
  view.value = { name, tool }
}

function toggleRail() {
  collapsed.value = !collapsed.value
  localStorage.setItem('envswitch-rail', collapsed.value ? 'collapsed' : 'expanded')
}

function toast(msg, kind = '') {
  const id = ++toastSeq
  toasts.value.push({ id, msg, kind })
  setTimeout(() => {
    toasts.value = toasts.value.filter(t => t.id !== id)
  }, kind === 'err' ? 6000 : 3200)
}

provide('nav', goto)
provide('toast', toast)
provide('tools', tools)
provide('isDark', isDark)
provide('hasUpdate', hasUpdate)

let offNavigate = () => {}
let offUpdate = () => {}
let offStatus = () => {}
let offInstall = () => {}
onMounted(async () => {
  applyTheme(isDark.value)
  try {
    tools.value = await api.overview()
    const s = await api.settings()
    ver.value = s?.version || 'v1.0.0'
    const pending = await api.pendingUpdate()
    if (pending?.available) {
      hasUpdate.value = true
    }
  } catch (err) {
    // 降级保护
  }
  offNavigate = on('navigate', (tool) => goto('tool', tool))
  offUpdate = on('update', (u) => {
    hasUpdate.value = !!u?.available
  })
  offStatus = on('status', (list) => { tools.value = list })
  offInstall = on('install', (p) => {
    if (p.stage === 'error') toast(`${p.tool} ${p.version} 失败\n${p.message}`, 'err')
    if (p.stage === 'done') toast(`${p.tool} ${p.version} 安装激活完成`)
    if (p.stage === 'cancelled') toast(`${p.tool} ${p.version} 已取消`)
  })
})

onUnmounted(() => {
  offNavigate()
  offUpdate()
  offStatus()
  offInstall()
})
</script>

<template>
  <div class="app">
    <TitleBar
      :ver="ver"
      :is-dark="isDark"
      :has-update="hasUpdate"
      @toggle-theme="toggleTheme"
      @open-settings="goto('settings')"
    />

    <div class="shell">
      <aside class="rail" :class="{ collapsed }">
        <button
          class="rail-toggle"
          :title="collapsed ? '展开侧边栏' : '收起侧边栏'"
          @click="toggleRail"
        >
          <svg class="toggle-svg" viewBox="0 0 24 24" :class="{ 'is-flipped': collapsed }">
            <path d="m14 8-4 4 4 4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>

        <nav class="nav">
          <button
            :class="{ on: view.name === 'overview' }"
            :title="collapsed ? '环境总览' : ''"
            @click="goto('overview')"
          >
            <Icon name="dash" :size="17" />
            <span class="lb" v-if="!collapsed">环境总览</span>
          </button>
          <button
            :class="{ on: view.name === 'netproxy' }"
            :title="collapsed ? '网络代理' : ''"
            @click="goto('netproxy')"
          >
            <Icon name="proxy" :size="17" />
            <span class="lb" v-if="!collapsed">网络代理</span>
          </button>
          <button
            class="pos-rel"
            :class="{ on: view.name === 'settings' }"
            :title="collapsed ? '系统设置' : ''"
            @click="goto('settings')"
          >
            <Icon name="gear" :size="17" />
            <span class="lb" v-if="!collapsed">系统设置</span>
            <span v-if="hasUpdate" class="rail-update-dot" :class="{ 'is-collapsed': collapsed }" title="发现新版本"></span>
          </button>
        </nav>

        <div class="scroll-rail">
          <template v-for="g in grouped" :key="g.id">
            <div
              class="sec group-header"
              :class="{ 'is-collapsed': isGroupCollapsed(g.id) }"
              @click="toggleGroup(g.id)"
              :title="!collapsed ? (isGroupCollapsed(g.id) ? '点击展开' : '点击收起') : g.label"
            >
              <!-- 展开态：标题 + 计数 + 箭头；折叠态：居中的代表图标按钮（整行仍可点击收起/展开分组） -->
              <template v-if="!collapsed">
                <span class="group-title">{{ g.label }}</span>
                <div class="group-right">
                  <span class="group-count">{{ g.items.length }}</span>
                  <svg class="group-arrow" :class="{ rotated: isGroupCollapsed(g.id) }" viewBox="0 0 24 24">
                    <path d="m6 9 6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                  </svg>
                </div>
              </template>
              <span v-else class="group-chip">
                <Icon :name="groupIcon(g.id)" :size="16" class="group-icon" />
                <span class="group-chip-dot"></span>
              </span>
            </div>
            <div class="toollist-wrap" :class="{ 'is-hidden': isGroupCollapsed(g.id) }">
              <div class="toollist">
                <button
                  v-for="t in g.items"
                  :key="t.id"
                  :class="{ on: view.name === 'tool' && view.tool === t.id }"
                  :title="`${t.title || t.id} · ${t.current ? t.current.version : '未激活'}`"
                  @click="goto('tool', t.id)"
                >
                  <span class="lamp" :class="t.lamp"></span>
                  <Icon :name="t.id" :size="17" />
                  <span class="lb" v-if="!collapsed">{{ t.id }}</span>
                  <span class="v num" v-if="!collapsed">{{ t.current ? t.current.version : '—' }}</span>
                </button>
              </div>
            </div>
          </template>
        </div>
      </aside>

      <Overview v-if="view.name === 'overview'" @open-tool="goto('tool', $event)" />
      <ToolDetail v-else-if="view.name === 'tool'" :tool="view.tool" />
      <SettingsView v-else-if="view.name === 'settings'" />
      <NetProxyView v-else-if="view.name === 'netproxy'" />
    </div>

    <!-- 浮动消息 Toast -->
    <div class="toasts">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="t.kind">
        <span class="toast-dot" :class="t.kind"></span>
        <span class="toast-txt">{{ t.msg }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.app {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--bg);
}

.shell {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
}

.rail {
  width: 230px;
  flex: none;
  background: var(--surface);
  border-right: 1px solid var(--hairline);
  display: flex;
  flex-direction: column;
  padding: 12px 10px 10px;
  position: relative;
  transition: width 0.22s var(--ease), background-color 0.2s var(--ease);
}

.rail.collapsed {
  width: 58px;
  padding: 12px 8px 10px;
}

/* 折叠把手：骑在侧栏与内容区的分界线上，默认安静、悬停上色 */
.rail-toggle {
  position: absolute;
  top: 18px;
  right: -11px;
  z-index: 60;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--surface-raised);
  border: 1px solid var(--hairline-strong);
  color: var(--ink-3);
  display: grid;
  place-items: center;
  box-shadow:
    0 2px 8px rgba(0, 0, 0, 0.16),
    inset 0 1px 0 var(--glass-hi);
  transition: all 0.18s var(--ease);
}

/* 扩大点击热区，视觉上仍是小圆钮 */
.rail-toggle::before {
  content: '';
  position: absolute;
  inset: -7px;
}

.rail-toggle:hover {
  color: #FFFFFF;
  background: var(--accent);
  border-color: var(--accent);
  transform: scale(1.1);
  box-shadow:
    0 0 0 4px var(--accent-dim),
    0 4px 14px var(--accent-line);
}

.rail-toggle:hover .toggle-svg {
  color: #FFFFFF;
}

.rail-toggle:active {
  transform: scale(0.95);
}

.rail-toggle:focus-visible {
  outline: 2px solid var(--accent-line);
  outline-offset: 3px;
}

.toggle-svg {
  width: 13px;
  height: 13px;
  transition: transform 0.26s var(--ease);
}

.toggle-svg.is-flipped {
  transform: rotate(180deg);
}

.nav {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin-bottom: 6px;
}

.nav button {
  height: 34px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 10px;
  border-radius: var(--r-ctl);
  font-size: 13px;
  font-weight: 500;
  color: var(--ink-2);
  transition: all 0.15s var(--ease);
}

.rail.collapsed .nav button {
  justify-content: center;
  padding: 0;
}

.nav button:hover {
  background: var(--surface-raised);
  color: var(--ink-1);
}

.nav button.on {
  background: var(--accent-dim);
  color: var(--accent-ink);
  border: 1px solid var(--accent-line);
}

.scroll-rail {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding-right: 2px;
}

.scroll-rail::-webkit-scrollbar {
  width: 4px;
}

.scroll-rail::-webkit-scrollbar-thumb {
  background: var(--hairline);
  border-radius: 4px;
}

.pos-rel {
  position: relative;
}

.rail-update-dot {
  position: absolute;
  top: 8px;
  right: 12px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #ff3b30;
  box-shadow: 0 0 6px rgba(255, 59, 48, 0.8);
  pointer-events: none;
}

.rail-update-dot.is-collapsed {
  top: 6px;
  right: 14px;
}

/* 雷达式扩散：中心点不动，同色圆环从原位向外扩散淡出 */
.rail-update-dot::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  border: 1px solid #ff3b30;
  opacity: 0.75;
  pointer-events: none;
  animation: rail-dot-ripple 1.9s ease-out infinite;
}

@keyframes rail-dot-ripple {
  0% {
    transform: scale(1);
    opacity: 0.75;
  }
  70% {
    opacity: 0;
  }
  100% {
    transform: scale(2.9);
    opacity: 0;
  }
}

.sec {
  margin-top: 14px;
  margin-bottom: 4px;
  padding: 0 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--ink-3);
  letter-spacing: 0.02em;
}

.group-header {
  cursor: pointer;
  user-select: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 8px;
  border-radius: 6px;
  transition: all 0.15s var(--ease);
}

.group-header:hover {
  background: var(--surface-raised);
  color: var(--ink-1);
}

/* ===== 折叠态分组头：居中代表图标按钮（不再是一条空白分隔线） ===== */
.rail.collapsed .group-header {
  height: 28px;
  justify-content: center;
  padding: 0;
  margin-top: 8px;
}

.group-chip {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
}

/* 分组被收起：图标压暗 + 右下角 2px 状态点提示「此组已收起」 */
.rail.collapsed .group-header.is-collapsed .group-icon {
  opacity: 0.5;
}

.group-chip-dot {
  position: absolute;
  right: 4px;
  bottom: 5px;
  width: 2px;
  height: 2px;
  border-radius: 50%;
  background: var(--accent);
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s var(--ease);
}

.rail.collapsed .group-header.is-collapsed .group-chip-dot {
  opacity: 1;
}

.group-title {
  display: inline-block;
}

.group-right {
  display: flex;
  align-items: center;
  gap: 6px;
}

.group-count {
  font-size: 10px;
  font-weight: 600;
  color: var(--ink-3);
  background: var(--surface-soft);
  padding: 1px 5px;
  border-radius: 999px;
  border: 1px solid var(--hairline);
  font-family: var(--font-num);
}

.group-arrow {
  width: 12px;
  height: 12px;
  color: var(--ink-3);
  transition: transform 0.22s var(--ease), color 0.15s ease;
}

.group-header:hover .group-arrow {
  color: var(--ink-1);
}

.group-arrow.rotated {
  transform: rotate(-90deg);
}

.toollist-wrap {
  overflow: hidden;
  max-height: 800px;
  transition: max-height 0.25s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.2s ease;
  opacity: 1;
}

.toollist-wrap.is-hidden {
  max-height: 0;
  opacity: 0;
  pointer-events: none;
}

/* .sec-line 分隔线已停用：折叠态分组头改渲染 group-chip 图标按钮 */

.toollist {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.toollist button {
  height: 32px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 8px;
  border-radius: var(--r-ctl);
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 500;
  border: 1px solid transparent;
  transition: all 0.15s var(--ease);
}

.rail.collapsed .toollist button {
  justify-content: center;
  padding: 0;
}

.toollist button:hover {
  background: var(--surface-raised);
  color: var(--ink-1);
}

.toollist button.on {
  background: var(--surface-raised);
  color: var(--ink-1);
  border-color: var(--hairline-strong);
}

.toollist .lb {
  flex: 1;
  text-align: left;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.toollist .v {
  font-size: 11px;
  color: var(--ink-3);
  font-family: var(--font-num);
}



/* 消息通知 Toast */
.toasts {
  position: fixed;
  bottom: 20px;
  right: 20px;
  z-index: 999;
  display: flex;
  flex-direction: column;
  gap: 8px;
  pointer-events: none;
}

.toast {
  padding: 8px 14px;
  background: var(--overlay);
  border: 1px solid var(--hairline-strong);
  border-radius: var(--r-ctl);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.22);
  color: var(--ink-1);
  font-size: 12px;
  display: flex;
  align-items: center;
  gap: 8px;
  backdrop-filter: blur(12px);
  animation: toastIn 0.22s var(--ease);
  pointer-events: auto;
}

.toast.err {
  border-color: rgba(242, 109, 109, 0.4);
}

.toast.warn {
  border-color: rgba(229, 180, 84, 0.4);
}

.toast-dot {
  position: relative;
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
  /* currentColor 与自身底色同步，扩散环在暗/亮两主题都可见 */
  color: var(--accent);
}

.toast-dot.err {
  background: var(--danger);
  color: var(--danger);
  box-shadow: 0 0 6px var(--danger);
}

.toast-dot.warn {
  background: var(--warn);
  color: var(--warn);
  box-shadow: 0 0 6px var(--warn);
}

/* 雷达式扩散：点本身不动，同色圆环原位向外淡出 */
.toast-dot::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  border: 1px solid currentColor;
  opacity: 0.7;
  pointer-events: none;
  animation: toast-ripple 1.9s ease-out infinite;
}

@keyframes toast-ripple {
  0% {
    transform: scale(1);
    opacity: 0.7;
  }
  70% {
    opacity: 0;
  }
  100% {
    transform: scale(3);
    opacity: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .rail-update-dot::after,
  .toast-dot::after {
    animation: none;
    opacity: 0;
  }
}

@keyframes toastIn {
  from {
    opacity: 0;
    transform: translateY(10px) scale(0.96);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}
</style>
