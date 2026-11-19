<script setup>
import { ref, inject, computed, onMounted, onUnmounted } from 'vue'
import { api, on } from '../bridge'
import Icon from '../components/Icon.vue'

const emit = defineEmits(['open-tool'])
const tools = inject('tools')
const toast = inject('toast')

const pop = ref(null) // { tool, rows }
const popUp = ref(false) // 下方空间不足时，弹层朝上展开
const popLeft = ref(false) // 右对齐会顶出可视区时，改为左对齐
const scrollPane = ref(null)
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

const POP_W = 216

async function openPop(t, ev) {
  // 同步取几何：await 之后 event.currentTarget 会被回收
  const card = ev?.currentTarget?.closest?.('.gauge-card')
  const rect = card ? card.getBoundingClientRect() : null
  const bound = scrollPane.value?.getBoundingClientRect()
  try {
    const rows = await api.local(t.id)
    pop.value = { tool: t.id, rows: rows || [] }
    // 弹层高度：头部 + 条目（列表最多 264px）+ 内边距
    const need = Math.min(pop.value.rows.length, 8) * 30 + 96
    // 下缘越过滚动区底部时朝上展开（顶部要避开滚动区自身的上边界）
    const bottomEdge = bound ? bound.bottom - 4 : window.innerHeight - 12
    const topEdge = bound ? bound.top + 4 : 12
    popUp.value = !!rect && rect.bottom + need > bottomEdge && rect.top - need > topEdge
    // 右对齐时左缘会越过滚动区可视左边界（左侧溢出不可滚动，会被裁掉）
    popLeft.value = !!rect && !!bound && (rect.right - POP_W) < bound.left + 4
  } catch (e) {
    toast(`加载 ${t.id} 版本失败: ${e}`, 'err')
  }
}

function closePop() {
  pop.value = null
  popUp.value = false
  popLeft.value = false
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

    <div class="scroll" ref="scrollPane" @click="closePop">
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
          :class="{ dim: !t.current, active: t.current, 'has-pop': pop && pop.tool === t.id }"
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
            <button
              class="switch-btn"
              :class="{ open: pop && pop.tool === t.id }"
              title="快捷切换版本"
              @click.stop="openPop(t, $event)"
            >
              <Icon name="swap" :size="11" />
              <span>快捷切换</span>
              <Icon name="chev" :size="10" class="chev" />
            </button>
          </div>

          <!-- 快捷版本选择弹层 -->
          <div
            v-if="pop && pop.tool === t.id"
            class="pop-menu"
            :class="{ up: popUp, 'align-left': popLeft }"
            @click.stop
          >
            <div class="pop-header">
              <span class="pop-title">快速切换</span>
              <span class="pop-tool num">{{ t.id }}</span>
              <span class="pop-count num" v-if="pop.rows.length">{{ pop.rows.length }}</span>
            </div>
            <div class="pop-list">
              <button
                v-for="(r, i) in pop.rows"
                :key="r.version"
                class="pop-item"
                :class="{ active: r.active, readonly: !pickable(r) }"
                :style="{ '--i': i }"
                :title="pickable(r) ? '切换到 ' + r.version : '系统环境，不支持快捷切换'"
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
              <button class="pop-item action" :style="{ '--i': pop.rows.length }" @click="emit('open-tool', t.id)">
                <Icon name="plus" :size="12" />
                <span>安装新版本</span>
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
  background: var(--warn-bg, rgba(229, 180, 84, 0.16));
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
  border-color: var(--accent-line);
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
  /* 标签不参与收缩：把挤压让给版本号（否则长版本号会把整行顶出弹层宽度） */
  flex: none;
  white-space: nowrap;
}

.src-tag.nvm {
  background: var(--accent-dim);
  border-color: var(--accent-line);
  color: var(--accent-ink);
}

.src-tag.系统, .src-tag.system, .src-tag.detected {
  background: var(--info-bg, rgba(84, 150, 229, 0.12));
  border-color: var(--info-line, rgba(84, 150, 229, 0.28));
  color: var(--info, #6bb1ff);
}

.src-tag.store {
  background: var(--warn-bg, rgba(229, 180, 84, 0.12));
  border-color: var(--warn-line, rgba(229, 180, 84, 0.28));
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
  padding-top: 3px;
}

/* 快捷切换按钮：平时退为细线幽灵态，悬停/展开才上色 */
.switch-btn {
  height: 24px;
  padding: 0 8px;
  border-radius: var(--r-ctl);
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.01em;
  color: var(--ink-3);
  background: transparent;
  border: 1px solid var(--hairline);
  transition: all 0.18s var(--ease);
}

.switch-btn:hover {
  color: var(--accent-ink);
  background: var(--accent-dim);
  border-color: var(--accent-line);
  transform: translateY(-1px);
  box-shadow: var(--shadow-glow);
}

.switch-btn:active {
  transform: scale(0.96) translateY(0);
  box-shadow: none;
}

.switch-btn:focus-visible {
  outline: 2px solid var(--accent-line);
  outline-offset: 2px;
}

.switch-btn.open {
  color: var(--accent-ink);
  background: var(--accent-dim);
  border-color: var(--accent-line);
  box-shadow: var(--shadow-glow);
}

.switch-btn .chev {
  opacity: 0.7;
  transition: transform 0.22s var(--ease), opacity 0.18s var(--ease);
}

.switch-btn:hover .chev,
.switch-btn.open .chev {
  opacity: 1;
}

.switch-btn.open .chev {
  transform: rotate(180deg);
}

/* 卡片浮起：把整张卡片的层叠顺序抬到兄弟卡片之上，弹层才不会被遮挡 */
.gauge-card.has-pop {
  z-index: 40;
  opacity: 1;
  border-color: var(--accent-line);
  box-shadow: var(--shadow-card-hover);
  transform: translateY(-2px);
}

/* 快速切换弹层 */
.pop-menu {
  position: absolute;
  top: 40px;
  right: 6px;
  width: 216px;
  padding: 6px;
  background: var(--overlay);
  border: 1px solid var(--hairline-strong);
  border-radius: 12px;
  /* 两层阴影：黑色层服务暗色主题，冷色层在亮色主题里更有纵深 */
  box-shadow:
    0 20px 44px -16px var(--pop-shadow, rgba(8, 18, 28, 0.4)),
    0 4px 14px rgba(0, 0, 0, 0.2),
    inset 0 1px 0 var(--glass-hi);
  backdrop-filter: blur(20px) saturate(1.5);
  z-index: 50;
  transform-origin: top right;
  animation: popShow 0.18s var(--ease);
}

.pop-menu.up {
  top: auto;
  bottom: 34px;
  transform-origin: bottom right;
  animation-name: popShowUp;
}

.pop-menu.align-left {
  right: auto;
  left: 6px;
  transform-origin: top left;
}

.pop-menu.align-left.up {
  transform-origin: bottom left;
}

@keyframes popShow {
  from {
    opacity: 0;
    transform: translateY(-6px) scale(0.97);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@keyframes popShowUp {
  from {
    opacity: 0;
    transform: translateY(6px) scale(0.97);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

.pop-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 7px 7px;
  margin-bottom: 4px;
  border-bottom: 1px solid var(--hairline);
}

.pop-title {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.09em;
  color: var(--ink-3);
}

.pop-tool {
  font-size: 11px;
  font-weight: 600;
  color: var(--accent-ink);
  background: var(--accent-dim);
  padding: 1px 6px;
  border-radius: 4px;
}

.pop-count {
  margin-left: auto;
  min-width: 18px;
  text-align: center;
  font-size: 10px;
  font-weight: 600;
  color: var(--ink-3);
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: 999px;
  padding: 1px 5px;
}

.pop-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 264px;
  overflow-y: auto;
  /* 弹层定宽 216px：长版本号只做省略号，绝不横向溢出（否则底部会冒出横向滚动条） */
  overflow-x: hidden;
  /* 溢出裁剪在 padding 盒边缘：预留 2px，正好容纳 hover 的 translateX(2px) 不被切角 */
  padding-right: 2px;
}

.pop-list::-webkit-scrollbar {
  width: 3px;
}

.pop-list::-webkit-scrollbar-thumb {
  background: var(--hairline-strong);
  border-radius: 999px;
}

/* 兜底：彻底隐藏可能出现的横向滚动条 */
.pop-list::-webkit-scrollbar:horizontal {
  display: none;
  height: 0;
}

.pop-item {
  position: relative;
  height: 30px;
  padding: 0 8px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11.5px;
  color: var(--ink-2);
  /* flex 子项必须允许收缩到内容宽度以下，长版本号才会走省略号而不是撑破弹层 */
  min-width: 0;
  max-width: 100%;
  /* 逐条错峰入场，只动 opacity/transform */
  animation: itemIn 0.2s var(--ease) backwards;
  animation-delay: calc(var(--i, 0) * 20ms);
  transition: background-color 0.14s var(--ease), color 0.14s var(--ease), transform 0.14s var(--ease);
}

@keyframes itemIn {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.pop-item:hover {
  background: var(--accent-dim);
  color: var(--ink-1);
  transform: translateX(2px);
}

.pop-item:focus-visible {
  outline: 2px solid var(--accent-line);
  outline-offset: -2px;
}

.pop-item.readonly {
  cursor: default;
  opacity: 0.6;
}

.pop-item.readonly:hover {
  background: transparent;
  color: var(--ink-2);
  transform: none;
}

.pop-item.active {
  background: var(--accent-dim);
  color: var(--accent-ink);
  font-weight: 600;
}

/* 当前版本：左侧一道 accent 导轨，比单纯高亮更好识别 */
.pop-item.active::before {
  content: '';
  position: absolute;
  left: 1px;
  top: 6px;
  bottom: 6px;
  width: 2px;
  border-radius: 2px;
  background: var(--accent);
}

.ver-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
  font-size: 12px;
  letter-spacing: -0.01em;
}

.check-icon {
  color: var(--accent);
}

.empty-note {
  font-size: 11px;
  color: var(--ink-3);
  padding: 12px 8px;
  text-align: center;
  background: var(--surface-soft);
  border: 1px dashed var(--hairline-strong);
  border-radius: 6px;
}

.pop-divider {
  height: 1px;
  background: var(--hairline);
  margin: 5px 6px;
}

.pop-item.action {
  gap: 7px;
  font-size: 11px;
  color: var(--ink-3);
}

.pop-item.action:hover {
  color: var(--accent-ink);
}

@media (prefers-reduced-motion: reduce) {
  .pop-item {
    animation: none;
  }
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
