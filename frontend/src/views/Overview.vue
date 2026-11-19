<script setup>
import { ref, inject, computed, onMounted, onUnmounted } from 'vue'
import { api, on } from '../bridge'
import Icon from '../components/Icon.vue'

const emit = defineEmits(['open-tool'])
const tools = inject('tools')
const toast = inject('toast')

const pop = ref(null) // { tool, rows }
const doctors = ref([])
const flip = ref('')
const upd = ref({ available: false })
const updBusy = ref(false)
const refreshing = ref(false)
const diagPane = ref(null)

let offUpdate = () => {}
let pollTimer = null

function isSystemSource(s) {
  return s === '系统' || s === 'system' || s === 'detected'
}

// 系统环境条目只做展示：它没有进版本库，无法通过 junction 切过去
function pickable(r) {
  return !isSystemSource(r.source)
}

function scrollToDoctor() {
  diagPane.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function pollOverview() {
  try {
    const list = await api.overview()
    if (list && list.length) {
      tools.value = list
    }
  } catch (e) {
    // 降级保护
  }
}

async function manualRefresh() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    // 强制清缓存复检：否则 60 秒内的探测结果会被直接复用，按钮等于没点
    const list = await api.refreshEnv()
    if (list && list.length) tools.value = list
    doctors.value = (await api.doctor()) || []
  } finally {
    setTimeout(() => {
      refreshing.value = false
    }, 450)
  }
}

function onWindowFocus() {
  pollOverview()
}

onMounted(async () => {
  try {
    doctors.value = (await api.doctor()) || []
    upd.value = (await api.pendingUpdate()) || { available: false }
  } catch (e) {
    // 降级保护
  }
  offUpdate = on('update', (u) => { upd.value = u })

  // 默认定时检索当前激活的什么版本（每 3.5 秒轮询 + 窗口聚焦时即刻检索）
  pollTimer = setInterval(pollOverview, 3500)
  window.addEventListener('focus', onWindowFocus)
})

onUnmounted(() => {
  offUpdate()
  if (pollTimer) clearInterval(pollTimer)
  window.removeEventListener('focus', onWindowFocus)
})

async function applyUpdate() {
  updBusy.value = true
  try {
    await api.applyUpdate()
  } catch (e) {
    updBusy.value = false
    toast(String(e), 'err')
  }
}

async function openPop(t) {
  try {
    const rows = await api.local(t.id)
    pop.value = { tool: t.id, rows: rows || [] }
  } catch (e) {
    toast(`加载 ${t.id} 版本失败: ${e}`, 'err')
  }
}

function closePop() {
  pop.value = null
}

async function pick(ver) {
  if (!pop.value) return
  const t = pop.value.tool
  const row = pop.value.rows.find(r => r.version === ver)
  if (row && !pickable(row)) return
  try {
    await api.use(t, ver)
    flip.value = t
    setTimeout(() => { flip.value = '' }, 320)
    tools.value = await api.overview()
    toast(`已成功切换 ${t} 至 ${ver}`)
  } catch (e) {
    toast(String(e), 'err')
  }
  closePop()
}

function formatSource(s) {
  if (!s) return ''
  if (s === 'nvm') return 'nvm'
  if (s === 'store') return 'store'
  if (s === 'external') return '外部'
  if (s === '系统' || s === 'system' || s === 'detected') return '系统'
  return s
}

const warnCount = computed(() =>
  doctors.value.filter(d => d.status === 'warn' || d.status === 'bad').length
)
</script>

<template>
  <div class="main">
    <header class="topbar">
      <span class="crumb">
        <b>环境总览</b>
      </span>
      <div class="top-actions">
        <button class="btn ghost sm refresh-btn" :disabled="refreshing" @click="manualRefresh" title="实时检测系统环境">
          <Icon name="swap" :size="13" :class="{ 'is-spinning': refreshing }" />
          <span>{{ refreshing ? '检索中…' : '检索环境' }}</span>
        </button>

        <button class="btn ghost sm" @click="scrollToDoctor">
          <Icon name="alert" :size="14" />
          <span>环境诊断</span>
          <span v-if="warnCount" class="warn-pill">{{ warnCount }} 异常</span>
        </button>
      </div>
    </header>

    <div class="scroll" @click="closePop">
      <!-- 热更新横幅 -->
      <div v-if="upd.available" class="update-banner">
        <span class="lamp up"></span>
        <div class="banner-body">
          <div class="banner-title">新版本 {{ upd.version }} 已准备就绪</div>
          <div class="banner-desc">{{ upd.notes || '支持无感原地热替换，重启即刻生效' }} · 大小约 {{ upd.size }}</div>
        </div>
        <button class="btn primary sm" :disabled="updBusy" @click="applyUpdate">
          <Icon name="down" :size="13" />
          {{ updBusy ? '正在热更新…' : '立即热更新' }}
        </button>
      </div>

      <!-- 工具卡片网格（缩小卡片、高集成紧凑布局） -->
      <div class="grid-tools">
        <div
          v-for="t in tools"
          :key="t.id"
          class="gauge-card"
          :class="{ dim: !t.current, active: t.current }"
          @click="emit('open-tool', t.id)"
        >
          <!-- 头部：Logo + 工具名 + 状态灯 -->
          <div class="card-head">
            <div class="brand-box">
              <Icon :name="t.id" :size="17" />
              <span class="tool-name">{{ t.id }}</span>
            </div>
            <span class="lamp" :class="t.lamp" :title="`状态: ${t.lamp}`"></span>
          </div>

          <!-- 主体：版本号 + 来源标签 + 已装数量 -->
          <div class="card-body">
            <div class="ver-wrap" :class="{ 'is-flipped': flip === t.id }">
              <span class="ver-text num" v-if="t.current">{{ t.current.version }}</span>
              <span class="ver-none" v-else>未激活</span>
            </div>
            <div class="meta-row">
              <span
                class="src-tag"
                :class="t.current ? t.current.source : ''"
                v-if="t.current"
              >
                {{ formatSource(t.current.source) }}
              </span>
              <span class="ver-count" v-if="t.installed && t.installed.length > 1">
                {{ t.installed.length }} 版本
              </span>
            </div>
          </div>

          <!-- 底部快捷切换动作区 -->
          <div class="card-foot" @click.stop>
            <button class="switch-btn" title="快捷切换版本" @click.stop="openPop(t)">
              <Icon name="swap" :size="11" />
              <span>快捷切换</span>
              <Icon name="chev" :size="10" />
            </button>
          </div>

          <!-- 快捷版本选择弹层 -->
          <div v-if="pop && pop.tool === t.id" class="pop-menu" @click.stop>
            <div class="pop-header">{{ t.id }} · 快速切换</div>
            <div class="pop-list">
              <button
                v-for="r in pop.rows"
                :key="r.version"
                class="pop-item"
                :class="{ active: r.active, readonly: !pickable(r) }"
                @click="pick(r.version)"
              >
                <span class="lamp" :class="r.active ? 'ok' : 'idle'"></span>
                <span class="num ver-label">{{ r.version }}</span>
                <span class="src-tag" :class="r.source">{{ formatSource(r.source) }}</span>
                <Icon v-if="r.active" name="check" :size="13" class="check-icon" />
              </button>

              <div v-if="!pop.rows.length" class="empty-note">
                尚未在本地安装任何版本
              </div>

              <div class="pop-divider"></div>
              <button class="pop-item action" @click="emit('open-tool', t.id)">
                <Icon name="down" :size="12" />
                <span>安装新版本…</span>
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 环境健康诊断面板 -->
      <div class="info-duo" ref="diagPane">
        <div class="info-panel full-width">
          <div class="panel-header">
            <h4>环境健康诊断</h4>
          </div>
          <div class="diag-grid">
            <div class="diag-item" v-for="d in doctors" :key="d.name">
              <span class="lamp" :class="{ ok: d.status === 'ok', up: d.status === 'warn', err: d.status === 'bad', idle: d.status === 'info' }"></span>
              <span class="diag-name">{{ d.name }}</span>
              <span class="diag-detail">{{ d.detail }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.refresh-btn {
  gap: 5px;
}

.is-spinning {
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.warn-pill {
  padding: 1px 6px;
  background: rgba(229, 180, 84, 0.16);
  color: var(--warn);
  border-radius: 999px;
  font-size: 10.5px;
  font-weight: 600;
}

.update-banner {
  padding: 10px 14px;
  background: var(--surface);
  border: 1px solid var(--accent-line);
  border-radius: var(--r-card);
  display: flex;
  align-items: center;
  gap: 12px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.08);
  margin-bottom: 4px;
}

.banner-body {
  flex: 1;
}

.banner-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.banner-desc {
  font-size: 11px;
  color: var(--ink-2);
}

/* 缩小卡片：列宽改为 175px，紧凑精致 */
.grid-tools {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(175px, 1fr));
  gap: 10px;
}

.gauge-card {
  position: relative;
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 10px 12px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-height: 102px;
  transition: all 0.16s var(--ease);
  user-select: none;
}

.gauge-card:hover {
  background: var(--surface-raised);
  border-color: var(--hairline-strong);
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
}

.gauge-card.active {
  border-color: rgba(63, 191, 168, 0.22);
}

.gauge-card.dim {
  opacity: 0.85;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.brand-box {
  display: flex;
  align-items: center;
  gap: 7px;
}

.tool-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-1);
  letter-spacing: -0.01em;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.ver-wrap {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-height: 22px;
  transition: transform 0.22s var(--ease);
}

.ver-wrap.is-flipped {
  transform: translateY(-2px);
}

.ver-text {
  font-size: 15.5px;
  font-weight: 700;
  color: var(--ink-1);
  letter-spacing: -0.02em;
  line-height: 1.2;
}

.ver-none {
  font-size: 12.5px;
  color: var(--ink-3);
  line-height: 1.2;
}

.meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.src-tag {
  font-size: 9.5px;
  padding: 1px 5px;
  border-radius: 3px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  color: var(--ink-3);
  font-family: var(--font-num);
  line-height: 1.2;
}

.src-tag.nvm {
  background: rgba(63, 191, 168, 0.12);
  border-color: rgba(63, 191, 168, 0.3);
  color: var(--accent-ink);
}

.src-tag.系统, .src-tag.system, .src-tag.detected {
  background: rgba(84, 150, 229, 0.12);
  border-color: rgba(84, 150, 229, 0.28);
  color: #6bb1ff;
}

.src-tag.store {
  background: rgba(229, 180, 84, 0.12);
  border-color: rgba(229, 180, 84, 0.28);
  color: var(--warn);
}

.ver-count {
  font-size: 10px;
  color: var(--ink-3);
  font-family: var(--font-num);
}

.card-foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  margin-top: auto;
  padding-top: 2px;
}

.switch-btn {
  height: 22px;
  padding: 0 7px;
  border-radius: 4px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10.5px;
  color: var(--ink-2);
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  transition: all 0.15s var(--ease);
}

.switch-btn:hover {
  background: var(--surface-raised);
  border-color: var(--accent-line);
  color: var(--ink-1);
}

/* 快速切换弹层 */
.pop-menu {
  position: absolute;
  top: 42px;
  right: 8px;
  width: 200px;
  background: var(--overlay);
  border: 1px solid var(--hairline-strong);
  border-radius: var(--r-panel);
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.24);
  backdrop-filter: blur(20px);
  z-index: 50;
  padding: 5px;
  animation: popShow 0.16s var(--ease);
}

@keyframes popShow {
  from {
    opacity: 0;
    transform: translateY(-4px) scale(0.97);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

.pop-header {
  font-size: 10.5px;
  font-weight: 600;
  color: var(--ink-3);
  padding: 5px 7px 3px;
}

.pop-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.pop-item {
  height: 28px;
  padding: 0 7px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11.5px;
  color: var(--ink-2);
  transition: all 0.12s var(--ease);
}

.pop-item:hover {
  background: var(--surface-raised);
  color: var(--ink-1);
}

.pop-item.readonly {
  cursor: default;
  opacity: 0.7;
}

.pop-item.readonly:hover {
  background: transparent;
  color: var(--ink-2);
}

.pop-item.active {
  background: var(--accent-dim);
  color: var(--accent-ink);
  font-weight: 600;
}

.ver-label {
  flex: 1;
  text-align: left;
}

.check-icon {
  color: var(--accent);
}

.empty-note {
  font-size: 11px;
  color: var(--ink-3);
  padding: 8px 6px;
  text-align: center;
}

.pop-divider {
  height: 1px;
  background: var(--hairline);
  margin: 3px 0;
}

/* 诊断与提示双栏 */
.info-duo {
  display: block;
  margin-top: 10px;
}

.info-panel.full-width {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 14px 16px;
}

.panel-header {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 12px;
}

.panel-header h4 {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-1);
}

.diag-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 8px 16px;
}

.diag-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11.5px;
  padding: 4px 6px;
  border-radius: 4px;
  background: var(--surface-soft);
}

.diag-name {
  font-weight: 600;
  color: var(--ink-1);
  min-width: 80px;
}

.diag-detail {
  color: var(--ink-2);
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
