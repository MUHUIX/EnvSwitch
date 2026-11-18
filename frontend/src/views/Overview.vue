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

let offUpdate = () => {}
onMounted(async () => {
  try {
    doctors.value = (await api.doctor()) || []
    upd.value = (await api.pendingUpdate()) || { available: false }
  } catch (e) {
    // 降级保护
  }
  offUpdate = on('update', (u) => { upd.value = u })
})

onUnmounted(() => offUpdate())

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

const warnCount = computed(() =>
  doctors.value.filter(d => d.status === 'warn' || d.status === 'bad').length
)
</script>

<template>
  <div class="main">
    <header class="topbar">
      <span class="crumb">
        <b>环境总览</b>
        <span class="crumb-sub">/ 本机多语言与工具链控制台</span>
      </span>
      <div class="top-actions">
        <button class="btn ghost sm" @click="emit('open-tool', tools[0]?.id || 'node')">
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

      <!-- 工具卡片网格 -->
      <div class="grid-tools">
        <div
          v-for="t in tools"
          :key="t.id"
          class="gauge-card"
          :class="{ dim: !t.current }"
          @click="emit('open-tool', t.id)"
        >
          <div class="card-head">
            <div class="brand-box">
              <Icon :name="t.id" :size="20" />
              <span class="tool-name">{{ t.id }}</span>
            </div>
            <span class="lamp" :class="t.lamp" :title="`状态: ${t.lamp}`"></span>
          </div>

          <div class="card-body">
            <div class="ver-wrap" :class="{ 'is-flipped': flip === t.id }">
              <span class="ver-text num" v-if="t.current">{{ t.current.version }}</span>
              <span class="ver-none" v-else>未激活</span>
              <span class="src-tag nvm" v-if="t.current && t.current.source === 'nvm'">nvm 收编</span>
            </div>
          </div>

          <div class="card-foot" @click.stop>
            <span class="src-label" v-if="t.current && t.current.source !== 'nvm'">
              {{ t.current.source }}
            </span>
            <span v-else></span>

            <button class="switch-btn" title="快捷切换已安装版本" @click.stop="openPop(t)">
              <Icon name="swap" :size="12" />
              <span>快速切换</span>
              <Icon name="chev" :size="11" />
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
                :class="{ active: r.active }"
                @click="pick(r.version)"
              >
                <span class="lamp" :class="r.active ? 'ok' : 'idle'"></span>
                <span class="num ver-label">{{ r.version }}</span>
                <span class="src-tag" :class="{ nvm: r.source === 'nvm' }">{{ r.source }}</span>
                <Icon v-if="r.active" name="check" :size="13" class="check-icon" />
              </button>

              <div v-if="!pop.rows.length" class="empty-note">
                尚未在本地安装任何版本
              </div>

              <div class="pop-divider"></div>
              <button class="pop-item action" @click="emit('open-tool', t.id)">
                <Icon name="down" :size="13" />
                <span>安装新版本…</span>
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 诊断与提示双面板 -->
      <div class="info-duo">
        <div class="info-panel">
          <div class="panel-header">
            <h4>环境与健康诊断</h4>
            <span class="sub-tip">自动扫描系统 PATH 与 Junction 状态</span>
          </div>
          <div class="diag-list">
            <div class="diag-item" v-for="d in doctors" :key="d.name">
              <span class="lamp" :class="{ ok: d.status === 'ok', up: d.status === 'warn', err: d.status === 'bad', idle: d.status === 'info' }"></span>
              <span class="diag-name">{{ d.name }}</span>
              <span class="diag-detail">{{ d.detail }}</span>
            </div>
          </div>
        </div>

        <div class="info-panel">
          <div class="panel-header">
            <h4>全栈环境使用小贴士</h4>
          </div>
          <div class="tips-list">
            <div class="tip-item">
              <span class="tip-dot"></span>
              <span><strong>Junction 毫秒切换</strong>：切换版本仅重写符号链接，无需管理员权限，绝不改乱全局 PATH。</span>
            </div>
            <div class="tip-item">
              <span class="tip-dot"></span>
              <span><strong>新终端即时生效</strong>：新开的终端（PowerShell / CMD / VSCode 终端）直接进入新版本。</span>
            </div>
            <div class="tip-item">
              <span class="tip-dot"></span>
              <span><strong>系统托盘极速切</strong>：右键右下角系统托盘图标，无需开启窗口即可秒切任意工具版本。</span>
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

.warn-pill {
  padding: 1px 6px;
  background: rgba(229, 180, 84, 0.16);
  color: var(--warn);
  border-radius: 999px;
  font-size: 10.5px;
  font-weight: 600;
}

.update-banner {
  padding: 12px 16px;
  background: var(--surface);
  border: 1px solid var(--accent-line);
  border-radius: var(--r-card);
  display: flex;
  align-items: center;
  gap: 12px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.08);
}

.banner-body {
  flex: 1;
}

.banner-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-1);
}

.banner-desc {
  font-size: 11.5px;
  color: var(--ink-2);
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.brand-box {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tool-name {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.card-body {
  padding: 4px 0;
}

.ver-wrap {
  display: flex;
  align-items: baseline;
  gap: 6px;
  transition: transform 0.22s var(--ease);
}

.ver-wrap.is-flipped {
  transform: translateY(-3px);
}

.ver-text {
  font-size: 18px;
  font-weight: 700;
  color: var(--ink-1);
}

.ver-none {
  font-size: 14px;
  color: var(--ink-3);
}

.src-tag {
  font-size: 10px;
  font-family: var(--font-num);
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  color: var(--ink-3);
}

.src-tag.nvm {
  background: rgba(63, 191, 168, 0.12);
  border-color: rgba(63, 191, 168, 0.3);
  color: var(--accent-ink);
}

.card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: auto;
  padding-top: 4px;
}

.src-label {
  font-size: 11px;
  color: var(--ink-3);
  font-family: var(--font-num);
}

.switch-btn {
  height: 24px;
  padding: 0 8px;
  border-radius: var(--r-ctl);
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--ink-2);
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  transition: all 0.15s var(--ease);
}

.switch-btn:hover {
  background: var(--surface-raised);
  border-color: var(--hairline-strong);
  color: var(--ink-1);
}

/* 快速切换弹层 */
.pop-menu {
  position: absolute;
  top: 48px;
  right: 12px;
  width: 220px;
  background: var(--overlay);
  border: 1px solid var(--hairline-strong);
  border-radius: var(--r-panel);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.24);
  backdrop-filter: blur(20px);
  z-index: 50;
  padding: 6px;
  animation: popShow 0.18s var(--ease);
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

.pop-header {
  font-size: 11px;
  font-weight: 600;
  color: var(--ink-3);
  padding: 6px 8px 4px;
}

.pop-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.pop-item {
  height: 30px;
  padding: 0 8px;
  border-radius: var(--r-ctl);
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--ink-2);
  transition: all 0.12s var(--ease);
}

.pop-item:hover {
  background: var(--surface-raised);
  color: var(--ink-1);
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
  font-size: 11.5px;
  color: var(--ink-3);
  padding: 10px 8px;
  text-align: center;
}

.pop-divider {
  height: 1px;
  background: var(--hairline);
  margin: 4px 0;
}

/* 诊断与提示双栏 */
.info-duo {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
  margin-top: 6px;
}

@media (max-width: 900px) {
  .info-duo {
    grid-template-columns: 1fr;
  }
}

.info-panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 16px 18px;
}

.panel-header {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 12px;
}

.panel-header h4 {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.sub-tip {
  font-size: 11px;
  color: var(--ink-3);
}

.diag-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.diag-item {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
}

.diag-name {
  font-weight: 600;
  color: var(--ink-1);
  min-width: 100px;
}

.diag-detail {
  color: var(--ink-2);
  font-size: 11.5px;
}

.tips-list {
  display: flex;
  flex-direction: column;
  gap: 9px;
}

.tip-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-2);
}

.tip-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  margin-top: 6px;
  flex: none;
}
</style>
