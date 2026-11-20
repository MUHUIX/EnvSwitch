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
const loc = ref(null) // 安装位置与环境配置快照（后端 Location）
const loading = ref(false)
const filter = ref('')
const installing = ref({}) // ver -> {pct, speed}
const confirmDel = ref('') // 待确认卸载的行标识（source|version，同一版本号可能有两行）
const removingVer = ref('') // 正在卸载的行标识（同一时刻只允许删一个）
const state = ref('')
const copied = ref('') // 刚复制成功的路径，用于图标短暂变成对勾

// 安装确认：先告诉用户会装到哪，再决定是否开跑；也可在这里换版本库
const pendingInstall = ref('')
const pendingStore = ref('')
const storeBusy = ref(false)
const storeProg = ref(null)

let offInstall = () => {}
let offStore = () => {}
onMounted(() => {
  offStore = on('store-migrate', (p) => {
    if (!storeBusy.value) return
    storeProg.value = { stage: p.stage, pct: p.pct, message: p.message }
  })
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

onUnmounted(() => {
  offInstall()
  offStore()
})

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
  // 位置信息失败不影响版本列表：详情页主体仍然可用
  try {
    loc.value = (await api.location(props.tool)) || null
  } catch (e) {
    loc.value = null
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
  loc.value = null
  copied.value = ''
  pendingInstall.value = ''
  pendingStore.value = ''
  storeProg.value = null
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

// 卸载入口的「不可用原因」：空串表示当前可点。
// 不用 disabled 属性承载它 —— 原生 disabled 按钮在 Chrome/WebView2 下不弹 title 提示，
// 用户只会觉得按钮点不动又没说明。改用 is-disabled 视觉态 + 点击兜底提示。
function removeBlocked(r) {
  if (removingVer.value) return '正在卸载其他版本，请稍候'
  if (isSystemSource(r.source) || r.source === 'external') return removeHint(r)
  if (isLastRemovable(r)) return removeHint(r)
  return ''
}

function onTrashClick(r) {
  const why = removeBlocked(r)
  if (why) {
    toast(why, 'err')
    return
  }
  confirmDel.value = rowKey(r)
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

// —— 安装位置与环境配置 ——

// 头部当前版本的路径：优先 junction 真实目标，未激活时回落到检测到的目录
const currentPath = computed(() => loc.value?.currentTarget || current.value?.rootDir || '')

// 安装位置卡片的收起状态：路径都是长串，多数人只看一眼，默认留一手收起的余地。
// 按应用级偏好记（不是按工具记）——换工具看详情时口径一致，不会每个页面都要重新收。
const locCollapsed = ref(localStorage.getItem('envswitch-loc-collapsed') === '1')

function toggleLoc() {
  locCollapsed.value = !locCollapsed.value
  localStorage.setItem('envswitch-loc-collapsed', locCollapsed.value ? '1' : '0')
}

// 上方「安装位置」四格：版本库、本工具目录、激活链接、新版本落点。
// 路径都可能很长，统一交给 CSS 省略号 + title 展示全文。
const locFacts = computed(() => {
  const l = loc.value
  if (!l) return []
  return [
    {
      key: 'store',
      label: '版本库根目录',
      value: l.storeDir,
      tag: l.storeCustom ? '自定义' : '默认',
      tagKind: l.storeCustom ? 'warn' : 'ok',
      openable: true
    },
    {
      key: 'tool',
      label: '本工具版本目录',
      value: l.toolStoreDir,
      tag: '',
      tagKind: 'plain',
      openable: true
    },
    {
      key: 'link',
      label: '当前激活链接',
      value: l.currentLink,
      sub: l.currentOk ? '→ ' + l.currentTarget : '尚未建立链接（切一个版本即可生成）',
      tag: l.currentOk ? '有效' : '未激活',
      tagKind: l.currentOk ? 'ok' : 'plain',
      openable: l.currentOk
    },
    {
      key: 'hint',
      label: '新版本安装位置',
      value: l.versionHint,
      sub: '每个版本一个独立目录，互不覆盖',
      tag: '模板',
      tagKind: 'plain',
      openable: false
    }
  ]
})

// 下方「环境配置」：PATH 注册 / 相关环境变量 / JS 命令 shim。
// PATH 与 shim 只读（由切换自动维护）；环境变量可托管，写入也走同一套后端。
const envBlocks = computed(() => {
  const l = loc.value
  if (!l) return []
  const blocks = []

  const pathRows = (l.pathEntries || []).map((p) => ({
    key: 'p:' + p.path,
    label: p.path,
    note: [
      p.role,
      p.present ? '已注册到用户 PATH' : '未注册',
      p.exists ? '' : '目录不存在',
      p.note
    ].filter(Boolean).join(' · '),
    state: p.present && p.exists ? 'ok' : (p.exists ? 'idle' : 'warn'),
    copy: p.path
  }))
  if (pathRows.length) blocks.push({ key: 'path', title: 'PATH 注册', rows: pathRows })

  const varRows = (l.envVars || []).map((v) => ({
    key: 'v:' + v.key,
    label: v.set ? `${v.key} = ${v.value}` : `${v.key} = 未设置`,
    note: v.managed
      ? `${v.note} · 由 EnvSwitch 托管${v.manageKey ? '' : `（在 ${v.manageTool} 页管理）`}，切换版本自动跟随`
      : v.note,
    state: v.managed || v.set ? 'ok' : 'idle',
    copy: v.set ? v.value : '',
    manageKey: v.manageKey || '',
    managed: !!v.managed,
    target: v.target || '',
    manageTool: v.manageTool || ''
  }))
  if (varRows.length) blocks.push({ key: 'env', title: '环境变量', rows: varRows })

  const shimRows = (l.shims || []).map((s) => ({
    key: 's:' + s.name,
    label: s.name,
    note: s.path + (s.exists ? ' · 已生成' : ' · 缺失（切换时自动重建）'),
    state: s.exists ? 'ok' : 'warn',
    copy: s.path
  }))
  if (shimRows.length) blocks.push({ key: 'shim', title: '命令 shim', rows: shimRows })

  return blocks
})

async function copyPath(p) {
  if (!p) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(p)
    } else {
      const ta = document.createElement('textarea')
      ta.value = p
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    copied.value = p
    setTimeout(() => {
      if (copied.value === p) copied.value = ''
    }, 1400)
    toast('路径已复制')
  } catch (e) {
    toast('复制失败: ' + e, 'err')
  }
}

async function openDir(p) {
  if (!p) return
  try {
    await api.openDir(p)
  } catch (e) {
    toast(String(e), 'err')
  }
}

// —— 安装确认 ——

const installTarget = computed(() => {
  if (!loc.value || !pendingInstall.value) return ''
  return `${loc.value.toolStoreDir}\\${pendingInstall.value}`
})

function confirmInstall(ver) {
  pendingInstall.value = ver
  pendingStore.value = ''
}

function closeInstall() {
  if (storeBusy.value) return
  pendingInstall.value = ''
  pendingStore.value = ''
}

// 位置卡上的「更改位置」：不装东西，只走迁移；复用同一个弹层
function openStoreMove() {
  pendingInstall.value = ''
  pendingStore.value = ''
  pickStoreFromInstall()
}

function startInstall() {
  const v = pendingInstall.value
  pendingInstall.value = ''
  pendingStore.value = ''
  if (v) doInstall(v)
}

async function pickStoreFromInstall() {
  if (storeBusy.value) return
  try {
    const dir = await api.pickDirectory('选择新的版本库目录（必须是空目录）', loc.value?.storeDir || '')
    if (dir) pendingStore.value = dir
  } catch (e) {
    toast(String(e), 'err')
  }
}

function samePath(a, b) {
  if (!a || !b) return false
  const norm = (p) => String(p).replace(/[\\/]+$/, '').toLowerCase()
  return norm(a) === norm(b)
}

// 在安装弹层里换位置：先迁移，成功后再让用户继续安装（不自动开跑）
async function doMoveStore() {
  if (storeBusy.value || !pendingStore.value) return
  const target = pendingStore.value
  storeBusy.value = true
  storeProg.value = { stage: 'scan', pct: 0, message: '准备迁移…' }
  try {
    await api.changeStore(target)
    pendingStore.value = ''
    await reload()
    toast(pendingInstall.value ? '版本库已迁移，可以继续安装' : '版本库已迁移')
  } catch (e) {
    toast(String(e), 'err')
    // 收尾失败但配置已切走：刷新落点并收起迁移确认
    await reload()
    if (samePath(loc.value?.storeDir, target)) pendingStore.value = ''
  } finally {
    storeBusy.value = false
    storeProg.value = null
  }
}

// —— 环境变量托管 ——

async function toggleManaged(row) {
  if (!row.manageKey) return
  const next = !row.managed
  try {
    await api.setEnvManaged(row.manageKey, next)
    toast(next ? `${row.manageKey} 已托管，切换版本自动跟随` : `${row.manageKey} 已恢复原值`)
    await reload()
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
          <div
            v-if="currentPath"
            class="cur-path num"
            :title="currentPath"
          >{{ currentPath }}</div>
        </div>
      </div>

      <!-- 安装位置：默认/自定义版本库、本工具目录、激活链接、新版本落点 -->
      <section v-if="loc" class="loc-panel" :class="{ collapsed: locCollapsed }">
        <div class="panel-head">
          <h4>安装位置</h4>
          <!-- 收起时把版本库根目录提到标题行，路径仍在手边可读到 -->
          <span v-if="locCollapsed" class="loc-brief num" :title="loc.storeDir">{{ loc.storeDir }}</span>
          <div class="loc-head-right">
            <span class="loc-flag" :class="{ custom: loc.storeCustom }">
              {{ loc.storeCustom ? '自定义版本库' : '默认版本库' }}
            </span>
            <template v-if="!locCollapsed">
              <button class="btn ghost sm" title="把版本库整体迁到其他磁盘" @click="openStoreMove">
                <Icon name="swap" :size="12" />
                <span>更改位置</span>
              </button>
              <button class="btn ghost sm" :title="`打开 ${loc.storeDir}`" @click="openDir(loc.storeDir)">
                <Icon name="folder" :size="12" />
                <span>打开版本库</span>
              </button>
            </template>
            <!-- 收起/展开整张卡片：偏好按应用记住，换工具看详情时口径一致 -->
            <button
              class="loc-collapse"
              :title="locCollapsed ? '展开安装位置' : '收起安装位置'"
              :aria-expanded="!locCollapsed"
              @click="toggleLoc"
            >
              <Icon name="chevron" :size="14" class="loc-arrow" />
            </button>
          </div>
        </div>

        <div class="loc-body" :class="{ 'is-hidden': locCollapsed }">
          <div class="loc-grid">
            <div v-for="f in locFacts" :key="f.key" class="loc-item">
              <div class="loc-k">
                <span class="loc-label">{{ f.label }}</span>
                <span v-if="f.tag" class="loc-tag" :class="f.tagKind">{{ f.tag }}</span>
                <div class="loc-ops">
                  <button
                    class="icon-btn"
                    :title="copied === f.value ? '已复制' : '复制路径'"
                    :aria-label="`复制 ${f.label}`"
                    @click="copyPath(f.value)"
                  >
                    <Icon :name="copied === f.value ? 'check' : 'copy'" :size="13" />
                  </button>
                  <button
                    v-if="f.openable"
                    class="icon-btn"
                    title="打开目录"
                    :aria-label="`打开 ${f.label}`"
                    @click="openDir(f.value)"
                  >
                    <Icon name="folder" :size="13" />
                  </button>
                </div>
              </div>
              <div class="loc-v num" :title="f.value">{{ f.value }}</div>
              <div v-if="f.sub" class="loc-sub num" :title="f.sub">{{ f.sub }}</div>
            </div>
          </div>

          <div v-if="loc.storeCustom" class="loc-foot">
            当前使用自定义版本库；默认位置为 <span class="num">{{ loc.storeDefault }}</span>
          </div>
        </div>
      </section>

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

              <!-- 主行：版本号 / 来源 / 占用；副行：这一版具体装在哪 -->
              <div class="v-main">
                <div class="v-top">
                  <span class="ver-name num">{{ r.version }}</span>
                  <span class="src-chip" :class="{ nvm: r.source === 'nvm' }">{{ sourceLabel(r.source) }}</span>
                  <span v-if="r.active" class="active-tag">当前生效</span>
                  <span class="size-col num">{{ r.size }}</span>
                </div>
                <button
                  v-if="r.rootDir"
                  class="v-path"
                  :title="`${r.rootDir}\nPATH 目录：${r.binDir}\n点击复制安装路径`"
                  @click="copyPath(r.rootDir)"
                >
                  <Icon name="folder" :size="11" />
                  <span class="num">{{ r.rootDir }}</span>
                </button>
              </div>

              <div class="action-col">
                <!-- 内联二次确认：非系统行（store 自有 + nvm 收编）一律可卸载，
                     激活行也可卸，只是文案说明卸完会自动切到剩余最新 -->
                <template v-if="confirmDel === rowKey(r)">
                  <button class="btn sm danger-b" :disabled="!!removingVer" @click="doRemove(r)">
                    <Icon name="trash" :size="12" />
                    <span>{{ removingVer === rowKey(r) ? '卸载中…' : (r.active ? '确认卸载并切换' : '确认删除') }}</span>
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
                    class="icon-btn trash-btn"
                    :class="{ 'is-ready': r.active, 'is-disabled': !!removeBlocked(r) }"
                    :aria-disabled="!!removeBlocked(r) || null"
                    :title="removeHint(r)"
                    :aria-label="removeHint(r)"
                    @click="onTrashClick(r)"
                  >
                    <Icon name="trash" :size="14" />
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
                  <button
                    class="btn sm primary"
                    :title="loc ? `安装到 ${loc.toolStoreDir}\\${r.version}` : `安装 ${r.version}`"
                    @click="confirmInstall(r.version)"
                  >
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

      <!-- 环境配置：PATH 注册 / 相关环境变量 / 命令 shim，全部只读 -->
      <section v-if="loc" class="env-panel">
        <div class="panel-head">
          <h4>环境配置</h4>
          <span class="badge-count">PATH 与 shim 自动维护 · 环境变量可托管</span>
        </div>
        <div v-if="!envBlocks.length" class="empty-hint">
          该工具没有需要额外注册的 PATH 条目或环境变量
        </div>
        <div v-else class="env-grid">
          <div v-for="b in envBlocks" :key="b.key" class="env-block">
            <div class="env-title">
              <span>{{ b.title }}</span>
              <span class="env-count">{{ b.rows.length }}</span>
            </div>
            <div v-for="row in b.rows" :key="row.key" class="env-row">
              <span class="env-dot" :class="row.state"></span>
              <div class="env-main">
                <div class="env-line num" :title="row.label">{{ row.label }}</div>
                <div class="env-note">{{ row.note }}</div>
              </div>
              <button
                v-if="row.copy"
                class="icon-btn env-copy"
                :title="copied === row.copy ? '已复制' : '复制'"
                @click="copyPath(row.copy)"
              >
                <Icon :name="copied === row.copy ? 'check' : 'copy'" :size="12" />
              </button>
              <button
                v-if="row.manageKey"
                class="toggle env-toggle"
                :class="{ on: row.managed }"
                :title="row.managed ? '取消托管，恢复托管前的值' : `托管为 ${row.target}（切换版本自动跟随）`"
                :aria-pressed="row.managed"
                @click="toggleManaged(row)"
              >
                <i class="toggle-ball"></i>
              </button>
              <span
                v-else-if="row.managed"
                class="managed-chip"
                :title="`由 ${row.manageTool} 的激活链接托管`"
              >托管</span>
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- 安装确认：先给落点，再决定开跑；也可以在这里换版本库 -->
    <div v-if="pendingInstall || pendingStore" class="modal-backdrop" @click.self="closeInstall">
      <div class="modal-card">
        <div class="modal-head">
          <span class="modal-title">
            {{ pendingInstall ? `安装 ${tool} ${pendingInstall}` : '迁移版本库' }}
          </span>
          <button class="modal-close" :disabled="storeBusy" @click="closeInstall">✕</button>
        </div>
        <div class="modal-body">
          <div v-if="pendingInstall" class="it-block">
            <span class="it-label">将安装到</span>
            <span class="it-path num" :title="installTarget">{{ installTarget }}</span>
          </div>
          <p v-if="pendingInstall" class="it-note">
            每个版本一个独立目录，切换只改 junction 链接、不复制文件；安装完成后可在「本机已安装」里切到该版本。
          </p>

          <div v-if="pendingStore && !storeBusy" class="move-box">
            <span class="mb-title">顺带把版本库迁到新位置？</span>
            <span class="mb-path num" :title="pendingStore">{{ pendingStore }}</span>
            <span class="mb-note">
              会整体搬走全部已装版本（目标必须是空目录），迁移期间请勿关闭应用；失败自动回滚，原目录不受影响。
            </span>
          </div>

          <div v-if="storeBusy && storeProg" class="move-prog">
            <div class="mp-top">
              <span>{{ storeProg.message || '迁移中…' }}</span>
              <span class="num">{{ Math.round(storeProg.pct || 0) }}%</span>
            </div>
            <div class="mp-bar">
              <div class="mp-fill" :style="{ width: Math.max(0, Math.min(100, storeProg.pct || 0)) + '%' }"></div>
            </div>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn sm ghost" :disabled="storeBusy" @click="pickStoreFromInstall">
            <Icon name="swap" :size="12" />
            <span>更改版本库位置</span>
          </button>
          <div class="foot-right">
            <button class="btn sm ghost" :disabled="storeBusy" @click="closeInstall">取消</button>
            <button v-if="pendingStore" class="btn sm primary" :disabled="storeBusy" @click="doMoveStore">
              确认迁移
            </button>
            <button
              v-if="pendingInstall"
              class="btn sm primary"
              :disabled="storeBusy"
              :title="pendingStore ? '不迁移，直接装到当前版本库' : `安装到 ${installTarget}`"
              @click="startInstall"
            >
              {{ pendingStore ? '直接安装' : '开始安装' }}
            </button>
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

/* 当前生效版本的真实目录：认路径比认版本号更直接 */
.cur-path {
  max-width: 340px;
  margin-top: 3px;
  font-size: 10.5px;
  color: var(--ink-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* —— 安装位置卡 —— */
.loc-panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 16px;
}

.loc-head-right {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

/* 收起态：标题行右侧直接给出版本库根目录，长路径中段省略 */
.loc-brief {
  flex: 1;
  min-width: 0;
  font-size: 11px;
  color: var(--ink-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 卡片收起把手：与侧栏分组头同一套箭头语言（收起时箭头转向右） */
.loc-collapse {
  width: 24px;
  height: 24px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: var(--r-ctl);
  border: 1px solid transparent;
  color: var(--ink-3);
  transition: all 0.15s var(--ease);
}

.loc-collapse:hover {
  color: var(--ink-1);
  background: var(--surface-raised);
  border-color: var(--hairline-strong);
}

.loc-collapse:focus-visible {
  outline: 2px solid var(--accent-line);
  outline-offset: 2px;
}

.loc-arrow {
  transition: transform 0.22s var(--ease);
}

.loc-panel.collapsed .loc-arrow {
  transform: rotate(-90deg);
}

/* 收起时标题行的分隔线与下边距都不要，卡片压回一条 */
.loc-panel.collapsed .panel-head {
  margin-bottom: 0;
  padding-bottom: 0;
  border-bottom: none;
}

.loc-body {
  overflow: hidden;
  max-height: 640px;
  opacity: 1;
  transition: max-height 0.26s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.2s ease;
}

.loc-body.is-hidden {
  max-height: 0;
  opacity: 0;
  pointer-events: none;
}

.loc-flag {
  font-size: 10.5px;
  padding: 2px 7px;
  border-radius: 999px;
  color: var(--accent-ink);
  background: var(--accent-dim);
  border: 1px solid var(--accent-line);
}

.loc-flag.custom {
  color: var(--warn);
  background: var(--warn-bg);
  border-color: var(--warn-line);
}

.loc-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.loc-item {
  min-width: 0;
  padding: 9px 10px 10px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: var(--r-ctl);
}

.loc-k {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.loc-label {
  font-size: 11px;
  color: var(--ink-3);
  white-space: nowrap;
}

.loc-tag {
  font-size: 9.5px;
  line-height: 1.5;
  padding: 0 5px;
  border-radius: 4px;
  border: 1px solid var(--hairline);
  color: var(--ink-3);
  background: var(--surface);
  white-space: nowrap;
}

.loc-tag.ok {
  color: var(--accent-ink);
  background: var(--accent-dim);
  border-color: var(--accent-line);
}

.loc-tag.warn {
  color: var(--warn);
  background: var(--warn-bg);
  border-color: var(--warn-line);
}

/* 复制/打开：常态安静，悬停整格时浮出 */
.loc-ops {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 2px;
  opacity: 0.45;
  transition: opacity 0.14s var(--ease);
}

.loc-item:hover .loc-ops,
.loc-ops:focus-within {
  opacity: 1;
}

.loc-v {
  margin-top: 5px;
  font-size: 12px;
  color: var(--ink-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.loc-sub {
  margin-top: 2px;
  font-size: 10.5px;
  color: var(--ink-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.loc-foot {
  margin-top: 10px;
  font-size: 10.5px;
  color: var(--ink-3);
}

/* —— 环境配置卡 —— */
.env-panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
  padding: 16px;
}

.env-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 16px;
}

.env-block {
  min-width: 0;
}

.env-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
  padding-bottom: 6px;
  border-bottom: 1px dashed var(--hairline);
  font-size: 11.5px;
  font-weight: 600;
  color: var(--ink-2);
}

.env-count {
  font-family: var(--font-num);
  font-size: 10px;
  font-weight: 400;
  color: var(--ink-3);
}

.env-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 7px 8px;
  border-radius: var(--r-ctl);
  transition: background-color 0.14s var(--ease);
}

.env-row:hover {
  background: var(--surface-raised);
}

/* 静态状态点：环境行一多，呼吸灯会变成噪声 */
.env-dot {
  width: 6px;
  height: 6px;
  margin-top: 5px;
  border-radius: 50%;
  flex: none;
  background: var(--lamp-off);
}

.env-dot.ok {
  background: var(--ok);
}

.env-dot.idle {
  background: var(--lamp-idle);
}

.env-dot.warn {
  background: var(--warn);
}

.env-main {
  flex: 1;
  min-width: 0;
}

.env-line {
  font-size: 11.5px;
  color: var(--ink-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.env-note {
  margin-top: 2px;
  font-size: 10.5px;
  line-height: 1.45;
  color: var(--ink-3);
}

.env-copy {
  opacity: 0;
}

.env-row:hover .env-copy,
.env-copy:focus-visible {
  opacity: 1;
}

/* 触屏/无悬停设备：不能把复制入口藏在 hover 里 */
@media (hover: none) {
  .loc-ops,
  .env-copy {
    opacity: 1;
  }
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

  .loc-grid {
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
  min-height: 46px;
  padding: 6px 10px;
  border-radius: var(--r-ctl);
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  border: 1px solid transparent;
  transition: all 0.12s var(--ease);
}

/* 版本行改成两行：上排版本/来源/占用，下排安装路径（可点击复制） */
.v-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.v-top {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.v-path {
  display: flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--ink-3);
  font-size: 10.5px;
  line-height: 1.3;
  text-align: left;
  cursor: pointer;
  transition: color 0.14s var(--ease);
}

.v-path span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.v-path:hover {
  color: var(--accent-ink);
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
  gap: 6px;
  margin-left: 8px;
}

/* 行内图标按钮：24×24 正方形（旧版是 26×24 的扁框，图标在框里不居中），
   常态无框无底 —— 版本行右侧只剩一片安静的图标，hover 才浮出 danger 底。 */
.icon-btn {
  width: 24px;
  height: 24px;
  flex: none;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--r-ctl);
  border: 1px solid transparent;
  background: transparent;
  color: var(--ink-3);
  transition: color 0.16s var(--ease), background-color 0.16s var(--ease),
    border-color 0.16s var(--ease), transform 0.16s var(--ease), opacity 0.16s ease;
}

/* 生效行没有「切换」按钮当邻居，卸载是唯一动作，常态提一档别看着像禁用 */
.icon-btn.is-ready {
  color: var(--ink-2);
}

.danger-b {
  color: var(--danger);
  background: var(--danger-bg);
  border-color: var(--danger-line);
}

.danger-b:hover:not([disabled]) {
  background: var(--danger-bg);
  border-color: var(--danger);
}

/* 卸载入口：常态只是行尾一枚弱色图标，hover 才浮出 danger 底并把图标提一档；
   is-disabled（系统行 / 外部目录 / 最后一个版本）保持灰、不给 danger 反馈，
   但按钮本身仍可点 —— 点了会 toast 出 disable 的原因（原生 disabled 不弹 title）。 */
.trash-btn:not(.is-disabled):hover {
  color: var(--danger);
  background: var(--danger-bg);
  border-color: var(--danger-line);
}

.trash-btn:not(.is-disabled):hover .env-icon {
  transform: translateY(-0.5px) scale(1.08);
}

.trash-btn:not(.is-disabled):active {
  transform: scale(0.9);
}

.trash-btn:focus-visible {
  border-color: var(--danger-line);
  box-shadow: 0 0 0 2px var(--danger-bg);
}

.trash-btn.is-disabled {
  opacity: 0.34;
  cursor: not-allowed;
}

@media (prefers-reduced-motion: reduce) {
  .trash-btn:not(.is-disabled):hover .env-icon,
  .trash-btn:not(.is-disabled):active {
    transform: none;
  }
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
  background: var(--accent-dim);
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

/* —— 安装确认弹层 —— */
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: grid;
  place-items: center;
  padding: 20px;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(3px);
}

.modal-card {
  width: 100%;
  max-width: 460px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--hairline-strong);
  border-radius: var(--r-panel);
  box-shadow: 0 16px 40px var(--pop-shadow);
}

.modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 15px 18px;
  border-bottom: 1px solid var(--hairline);
}

.modal-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--ink-1);
}

.modal-close {
  padding: 4px 6px;
  border-radius: 4px;
  color: var(--ink-3);
  font-size: 14px;
}

.modal-close:hover:not([disabled]) {
  background: var(--surface-raised);
  color: var(--ink-1);
}

.modal-close[disabled] {
  opacity: 0.4;
  cursor: not-allowed;
}

.modal-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
  max-height: 68vh;
  overflow-y: auto;
}

.it-block {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: var(--r-ctl);
}

.it-label {
  font-size: 11px;
  color: var(--ink-3);
}

.it-path {
  font-size: 12px;
  color: var(--ink-1);
  word-break: break-all;
  line-height: 1.5;
}

.it-note {
  margin: 0;
  font-size: 11px;
  line-height: 1.65;
  color: var(--ink-3);
}

.move-box {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  background: var(--warn-bg);
  border: 1px solid var(--warn-line);
  border-radius: var(--r-ctl);
}

.mb-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-1);
}

.mb-path {
  font-size: 11.5px;
  color: var(--warn);
  word-break: break-all;
  line-height: 1.5;
}

.mb-note {
  font-size: 10.5px;
  line-height: 1.55;
  color: var(--ink-3);
}

.move-prog {
  padding: 9px 11px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: var(--r-ctl);
}

.mp-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
  font-size: 11px;
  color: var(--ink-2);
}

.mp-bar {
  height: 5px;
  background: var(--hairline-strong);
  border-radius: 999px;
  overflow: hidden;
}

.mp-fill {
  height: 100%;
  background: var(--accent);
  transition: width 0.18s var(--ease);
}

.modal-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px;
  padding: 13px 18px;
  background: var(--surface-soft);
  border-top: 1px solid var(--hairline);
}

.foot-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* —— 环境变量托管开关 —— */
.toggle {
  width: 34px;
  height: 20px;
  flex: none;
  display: flex;
  align-items: center;
  padding: 2px;
  border-radius: 10px;
  background: var(--surface-raised);
  border: 1px solid var(--hairline-strong);
  cursor: pointer;
  transition: background-color 0.2s var(--ease), border-color 0.2s var(--ease);
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
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
  transition: transform 0.2s var(--ease);
}

.toggle.on .toggle-ball {
  transform: translateX(14px);
}

.managed-chip {
  flex: none;
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 9.5px;
  color: var(--accent-ink);
  background: var(--accent-dim);
  border: 1px solid var(--accent-line);
  white-space: nowrap;
}
</style>
