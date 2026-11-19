<script setup>
import { ref, watch, computed, inject, onMounted, onUnmounted } from 'vue'
import { api, on } from '../bridge'
import Icon from '../components/Icon.vue'

const props = defineProps({ tool: String })
const toast = inject('toast')
const nav = inject('nav')

const locals = ref([])
const remotes = ref([])
const current = ref(null)
const loading = ref(false)
const filter = ref('')
const installing = ref({}) // ver -> {pct, speed}
const confirmDel = ref('') // 待确认卸载的行标识（source|version，同一版本号可能有两行）
const removingVer = ref('') // 正在卸载的行标识（同一时刻只允许删一个）
const state = ref('')

let offInstall = () => {}
onMounted(() => {
  offInstall = on('install', (p) => {
    if (p.tool !== props.tool) return
    if (p.stage === 'download') {
      installing.value = {
        ...installing.value,
        [p.version]: { pct: p.pct / 100, speed: p.speed }
      }
    }
    if (p.stage === 'done' || p.stage === 'error' || p.stage === 'cancelled') {
      const { [p.version]: _, ...rest } = installing.value
      installing.value = rest
      reload()
    }
  })
})

onUnmounted(() => offInstall())

async function reload() {
  loading.value = true
  state.value = ''
  try {
    locals.value = (await api.local(props.tool)) || []
    current.value = locals.value.find(r => r.active) || null
  } catch (err) {
    locals.value = []
  } finally {
    loading.value = false
  }
}

async function loadRemote() {
  try {
    remotes.value = (await api.remote(props.tool)) || []
  } catch (e) {
    remotes.value = []
    state.value = String(e)
  }
}

// 在左侧栏切换工具时，本组件不会重新挂载（App.vue 里只换 props.tool），
// 右侧「官方可用版本」列表、搜索关键字、索引错误提示、以及上个工具的安装进度
// 都会残留在新工具页面上。所以换工具时先把这些与工具绑定的视图状态清干净，
// 再重载本机列表。安装任务仍在后台跑，其完成提示由 App.vue 的 toast 兜底，
// 这里丢掉的只是当前视图已无意义的进度条。
function switchTool() {
  remotes.value = []
  filter.value = ''
  state.value = ''
  installing.value = {}
  confirmDel.value = ''
  removingVer.value = ''
  reload()
}

watch(() => props.tool, switchTool, { immediate: true })

function shown(list) {
  const f = filter.value.trim().replace(/^v/, '')
  return f ? list.filter(r => r.version.startsWith(f)) : list
}

function isSystemSource(s) {
  return s === '系统' || s === 'system' || s === 'detected'
}

function sourceLabel(s) {
  if (isSystemSource(s)) return '系统'
  if (s === 'external') return '外部'
  return s || ''
}

function sourceNote(s) {
  if (isSystemSource(s)) return '系统环境'
  if (s === 'nvm') return 'nvm 收编'
  return '已激活'
}

// 本机可卸载的行：store 自有版本 + nvm 收编版本；
// 系统 PATH 探测出来的行只读（不在我们管的目录里，删它等于删用户的系统环境）。
const removable = computed(() => locals.value.filter(r => !isSystemSource(r.source)))

// 托底：只剩最后一个可卸载版本、且它正是当前生效版本时不许删，
// 否则本机将没有任何可切换目标（激活 junction 也会被摘掉）。
function isLastRemovable(r) {
  return removable.value.length <= 1 && !!r.active
}

function removeHint(r) {
  if (isSystemSource(r.source)) return '外部/系统环境不受管，不支持卸载'
  if (r.source === 'external') return '外部安装目录不受管，不支持卸载'
  if (isLastRemovable(r)) return '最后一个版本，先安装其他版本再卸载'
  if (r.active) return '卸载当前生效版本（会自动切到剩余最新）'
  return '卸载此版本'
}

// 行标识：同一份安装可能同时以 store 与 nvm 两个入口出现，不能只用版本号区分。
function rowKey(r) {
  return `${r.source}|${r.version}`
}

async function doUse(ver) {
  try {
    await api.use(props.tool, ver)
    toast(`已成功切换到 ${ver}`)
  } catch (e) {
    toast(String(e), 'err')
  }
  await reload()
}

async function doInstall(ver) {
  installing.value = { ...installing.value, [ver]: { pct: 0, speed: '' } }
  try {
    await api.install(props.tool, ver)
  } catch (e) {
    toast(String(e), 'err')
    const { [ver]: _, ...rest } = installing.value
    installing.value = rest
  }
}

async function doRemove(r) {
  // 兜底断言：入口按钮已禁用，这里再挡一次，防止绕过 UI 删掉不可删的行或最后一个版本
  if (!r || isSystemSource(r.source) || r.source === 'external') {
    toast(`${props.tool} ${r ? r.version : ''} 不可卸载（外部/系统环境）`, 'err')
    confirmDel.value = ''
    return
  }
  if (isLastRemovable(r)) {
    toast('最后一个版本，先安装其他版本再卸载', 'err')
    confirmDel.value = ''
    return
  }
  removingVer.value = rowKey(r)
  try {
    // cascade=true：删的正是当前生效版本时，后端摘掉激活链接并自动切到剩余最新
    await api.remove(props.tool, r.version, true)
    toast(`已卸载 ${r.version}`)
  } catch (e) {
    toast(String(e), 'err')
  } finally {
    removingVer.value = ''
  }
  confirmDel.value = ''
  await reload()
}

async function refresh() {
  try {
    await api.refreshIndex(props.tool)
    await loadRemote()
    toast(`${props.tool} 远程索引已更新`)
  } catch (e) {
    toast(String(e), 'err')
  }
}
</script>

<template>
  <div class="main">
    <header class="topbar">
      <span class="crumb">
        <span class="crumb-link" @click="nav('overview')">总览</span>
        <Icon name="chevr" :size="13" />
        <b>{{ tool }}</b>
      </span>
      <div class="top-actions">
        <button class="btn ghost sm" @click="refresh">
          <Icon name="refresh" :size="13" />
          <span>刷新索引</span>
        </button>
        <button class="btn ghost sm" @click="loadRemote">
          <Icon name="down" :size="13" />
          <span>加载可用版本</span>
        </button>
      </div>
    </header>

    <div class="scroll">
      <!-- 头部状态与版本摘要 -->
      <div class="detail-header">
        <div class="tool-brand">
          <Icon :name="tool" :size="38" />
          <div>
            <h2 class="tool-title">{{ tool }}</h2>
          </div>
        </div>

        <div class="current-badge">
          <div class="cur-ver num">{{ current ? current.version : '未激活' }}</div>
          <div class="cur-note" v-if="current">{{ sourceNote(current.source) }}</div>
        </div>
      </div>

      <!-- 版本管理双栏 -->
      <div class="versions-grid">
        <!-- 本机已安装 -->
        <div class="v-panel">
          <div class="panel-head">
            <h4>本机已安装</h4>
            <span class="badge-count">{{ locals.length }} 个版本</span>
          </div>
          <div class="v-list">
            <div v-if="!locals.length" class="empty-hint">
              本机尚未安装任何 {{ tool }} 版本，可从右侧「可用版本」点击安装。
            </div>
            <div
              v-for="r in locals"
              :key="r.source + r.version + r.binDir"
              class="v-row"
              :class="{ 'is-active': r.active }"
            >
              <span class="lamp" :class="r.active ? 'ok' : 'idle'"></span>
              <span class="ver-name num">{{ r.version }}</span>
              <span class="src-chip" :class="{ nvm: r.source === 'nvm' }">{{ sourceLabel(r.source) }}</span>
              <span v-if="r.active" class="active-tag">当前生效</span>
              <span class="size-col num">{{ r.size }}</span>

              <div class="action-col">
                <!-- 内联二次确认：非系统行（store 自有 + nvm 收编）一律可卸载，
                     激活行也可卸，只是文案说明卸完会自动切到剩余最新 -->
                <template v-if="confirmDel === rowKey(r)">
                  <button class="btn sm danger-b" :disabled="!!removingVer" @click="doRemove(r)">
                    {{ removingVer === rowKey(r) ? '卸载中…' : (r.active ? '确认卸载并切换' : '确认删除') }}
                  </button>
                  <button class="btn sm ghost" :disabled="!!removingVer" @click="confirmDel = ''">取消</button>
                </template>
                <template v-else-if="isSystemSource(r.source)">
                  <span v-if="!r.active" class="ro-tag">系统环境</span>
                </template>
                <template v-else>
                  <button
                    v-if="!r.active"
                    class="btn sm primary"
                    :disabled="!!removingVer"
                    @click="doUse(r.version)"
                  >
                    切换
                  </button>
                  <span v-else-if="r.source !== 'store'" class="ro-tag">收编自 {{ sourceLabel(r.source) }}</span>
                  <button
                    class="btn sm ghost icon-only trash-btn"
                    :disabled="!!removingVer || isLastRemovable(r) || r.source === 'external'"
                    :title="removeHint(r)"
                    @click="confirmDel = rowKey(r)"
                  >
                    <Icon name="trash" :size="13" />
                  </button>
                </template>
              </div>
            </div>
          </div>
        </div>

        <!-- 远程可用版本 -->
        <div class="v-panel">
          <div class="panel-head">
            <h4>官方可用版本</h4>
            <div class="search-box">
              <input v-model="filter" placeholder="搜索版本号..." />
            </div>
          </div>
          <div class="v-list">
            <div v-if="!remotes.length" class="empty-hint">
              {{ state ? '索引拉取失败: ' + state : '点击右上角「加载可用版本」获取官方版本列表' }}
            </div>
            <div
              v-for="r in shown(remotes)"
              :key="r.version"
              class="v-row"
              :class="{ 'is-downloading': installing[r.version] }"
            >
              <span class="ver-name num">{{ r.version }}</span>
              <span v-if="r.label === 'LTS'" class="lts-tag">LTS</span>
              <span v-else-if="r.label" class="sub-tag">{{ r.label }}</span>
              <span v-if="r.installed" class="installed-tag">已安装</span>

              <template v-if="installing[r.version]">
                <div class="dl-progress">
                  <span class="spd-txt num">
                    {{ Math.round(installing[r.version].pct * 100) }}% · {{ installing[r.version].speed || '准备中' }}
                  </span>
                  <div class="progress-bar">
                    <div class="progress-fill" :style="{ width: `${installing[r.version].pct * 100}%` }"></div>
                  </div>
                  <button class="btn sm text" @click="api.cancelInstall(tool)">取消</button>
                </div>
              </template>
              <template v-else-if="!r.installed">
                <span class="size-col num">{{ r.size }}</span>
                <div class="action-col">
                  <button class="btn sm primary" @click="doInstall(r.version)">
                    <Icon name="down" :size="12" />
                    <span>安装</span>
                  </button>
                </div>
              </template>
              <span v-else class="size-col"></span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.crumb-link {
  cursor: pointer;
  color: var(--ink-2);
  transition: color 0.15s ease;
}

.crumb-link:hover {
  color: var(--accent);
}

.detail-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 22px;
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.04);
}

.tool-brand {
  display: flex;
  align-items: center;
  gap: 16px;
}

.tool-title {
  font-size: 20px;
  font-weight: 700;
  color: var(--ink-1);
}

.current-badge {
  text-align: right;
}

.cur-ver {
  font-size: 22px;
  font-weight: 800;
  color: var(--accent);
}

.cur-note {
  font-size: 11px;
  color: var(--ink-3);
}

.versions-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

@media (max-width: 900px) {
  .versions-grid {
    grid-template-columns: 1fr;
  }
}

.v-panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 16px;
  display: flex;
  flex-direction: column;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--hairline);
}

.panel-head h4 {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.badge-count {
  font-size: 11px;
  color: var(--ink-3);
}

.search-box input {
  height: 26px;
  width: 140px;
  padding: 0 8px;
  border-radius: var(--r-ctl);
  border: 1px solid var(--hairline);
  background: var(--surface-soft);
  font-size: 11.5px;
  outline: none;
}

.search-box input:focus {
  border-color: var(--accent);
}

.v-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 520px;
  overflow-y: auto;
  padding-right: 4px;
}

.v-row {
  height: 40px;
  padding: 0 10px;
  border-radius: var(--r-ctl);
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  border: 1px solid transparent;
  transition: all 0.12s var(--ease);
}

.v-row:hover {
  background: var(--surface-raised);
}

.v-row.is-active {
  background: var(--surface-raised);
  border-left: 3px solid var(--accent);
}

.ver-name {
  font-weight: 600;
  color: var(--ink-1);
  min-width: 75px;
}

.src-chip {
  font-size: 10px;
  font-family: var(--font-num);
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  color: var(--ink-3);
}

.src-chip.nvm {
  color: var(--accent-ink);
  background: var(--accent-dim);
}

.active-tag {
  font-size: 10.5px;
  font-weight: 600;
  color: var(--accent);
}

.size-col {
  margin-left: auto;
  font-size: 11.5px;
  color: var(--ink-3);
}

.action-col {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: 8px;
}

.icon-only {
  width: 26px;
  padding: 0;
  display: grid;
  place-items: center;
}

.danger-b {
  color: var(--danger);
  border-color: var(--danger-line, rgba(242, 109, 109, 0.3));
}

.danger-b:hover:not([disabled]) {
  background: var(--danger-bg, rgba(242, 109, 109, 0.1));
  border-color: var(--danger-line, rgba(242, 109, 109, 0.3));
}

/* 卸载按钮：常态弱色，hover 转 danger；禁用（系统行/最后一个版本）保持灰且不变形 */
.trash-btn {
  color: var(--ink-3);
}

.trash-btn:hover:not([disabled]) {
  color: var(--danger);
  background: var(--danger-bg, rgba(242, 109, 109, 0.1));
  border-color: var(--danger-line, rgba(242, 109, 109, 0.3));
}

.trash-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.ro-tag {
  font-size: 11px;
  color: var(--ink-3);
  white-space: nowrap;
}

.lts-tag {
  font-size: 10px;
  font-weight: 700;
  padding: 1px 5px;
  background: rgba(63, 191, 168, 0.16);
  color: var(--accent-ink);
  border-radius: 4px;
}

.sub-tag {
  font-size: 10px;
  padding: 1px 4px;
  background: var(--surface-soft);
  color: var(--ink-3);
  border-radius: 4px;
}

.installed-tag {
  font-size: 10px;
  color: var(--ink-3);
}

.empty-hint {
  font-size: 12px;
  color: var(--ink-3);
  padding: 24px 0;
  text-align: center;
}

/* 下载中进度 */
.dl-progress {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

.spd-txt {
  font-size: 11px;
  color: var(--accent);
}

.progress-bar {
  width: 70px;
  height: 5px;
  background: var(--hairline-strong);
  border-radius: 999px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background: var(--accent);
  transition: width 0.15s ease;
}
</style>
