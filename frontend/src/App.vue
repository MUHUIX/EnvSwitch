<script setup>
import { ref, computed, provide, onMounted, onUnmounted } from 'vue'
import { api, on } from './bridge'
import Icon from './components/Icon.vue'
import TitleBar from './components/TitleBar.vue'
import Overview from './views/Overview.vue'
import ToolDetail from './views/ToolDetail.vue'
import SettingsView from './views/Settings.vue'

const view = ref({ name: 'overview', tool: null })
const tools = ref([])
const toasts = ref([])
const collapsed = ref(localStorage.getItem('envswitch-rail') === 'collapsed')
const ver = ref('')
const isDark = ref(localStorage.getItem('envswitch-theme') !== 'light')
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

const GROUPS = [
  ['runtime', '语言运行时'],
  ['packages', '包管理与工具链'],
  ['jvm', '企业运行时 (JVM/.NET)'],
  ['cloud', '云原生与运维']
]

const grouped = computed(() =>
  GROUPS.map(([id, label]) => ({
    id,
    label,
    items: tools.value.filter(t => t.group === id)
  })).filter(g => g.items.length)
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

let offNavigate = () => {}
onMounted(async () => {
  applyTheme(isDark.value)
  try {
    tools.value = await api.overview()
    const s = await api.settings()
    ver.value = s?.version || 'v1.0.0'
  } catch (err) {
    // 降级保护
  }
  offNavigate = on('navigate', (tool) => goto('tool', tool))
})

onUnmounted(() => offNavigate())

on('status', (list) => { tools.value = list })
on('install', (p) => {
  if (p.stage === 'error') toast(`${p.tool} ${p.version} 失败\n${p.message}`, 'err')
  if (p.stage === 'done') toast(`${p.tool} ${p.version} 安装激活完成`)
  if (p.stage === 'cancelled') toast(`${p.tool} ${p.version} 已取消`)
})
</script>

<template>
  <div class="app">
    <TitleBar
      :ver="ver"
      :is-dark="isDark"
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
            :class="{ on: view.name === 'settings' }"
            :title="collapsed ? '系统设置' : ''"
            @click="goto('settings')"
          >
            <Icon name="gear" :size="17" />
            <span class="lb" v-if="!collapsed">系统设置</span>
          </button>
        </nav>

        <div class="scroll-rail">
          <template v-for="g in grouped" :key="g.id">
            <div class="sec">
              <span v-if="!collapsed">{{ g.label }}</span>
              <span v-else class="sec-line"></span>
            </div>
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
          </template>
        </div>

        <div class="rail-foot" v-if="!collapsed">
          <span class="foot-txt">作者：沐晖 · MIT 开源</span>
        </div>
      </aside>

      <Overview v-if="view.name === 'overview'" @open-tool="goto('tool', $event)" />
      <ToolDetail v-else-if="view.name === 'tool'" :tool="view.tool" />
      <SettingsView v-else-if="view.name === 'settings'" />
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

.rail-toggle {
  position: absolute;
  top: 13px;
  right: -10px;
  z-index: 10;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--surface-raised);
  border: 1px solid var(--hairline-strong);
  color: var(--ink-2);
  display: grid;
  place-items: center;
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.12);
  transition: all 0.15s var(--ease);
}

.rail-toggle:hover {
  color: var(--ink-1);
  background: var(--accent);
  border-color: var(--accent);
}

.rail-toggle:hover .toggle-svg {
  color: #FFFFFF;
}

.toggle-svg {
  width: 13px;
  height: 13px;
  transition: transform 0.22s var(--ease);
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

.sec {
  margin-top: 14px;
  margin-bottom: 4px;
  padding: 0 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--ink-3);
  letter-spacing: 0.02em;
}

.sec-line {
  display: block;
  height: 1px;
  background: var(--hairline);
  margin: 6px 4px;
}

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

.rail-foot {
  margin-top: auto;
  padding: 8px 6px 2px;
  border-top: 1px solid var(--hairline);
}

.foot-txt {
  font-size: 11px;
  color: var(--ink-3);
  white-space: nowrap;
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

.toast-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
}

.toast-dot.err {
  background: var(--danger);
  box-shadow: 0 0 6px var(--danger);
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
