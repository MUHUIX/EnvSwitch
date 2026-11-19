<script setup>
import { ref, computed, inject, onMounted, onUnmounted } from 'vue'
import { api, on, openExternal } from '../bridge'
import Icon from '../components/Icon.vue'

const toast = inject('toast')
const isDark = inject('isDark', ref(true))
const st = ref(null)
const checkingUpdate = ref(false)
const updateInfo = ref(null)
const updateMsg = ref('')
const applyingUpdate = ref(false)
// tool === 'self' 的 install 事件驱动：{ stage, pct, speed, message }
const selfProg = ref(null)
const selfError = ref('')
const notesOpen = ref(false)
const skipVer = ref(localStorage.getItem('envswitch-skip-update') || '')

// —— 版本库迁移 ——
// 只支持"整体换盘"：目标必须是空目录，后端负责迁移、校验、重建 junction，
// 失败自动回滚；这里只做确认与进度展示。
const storeConfirm = ref('') // 待确认的新路径
const storeBusy = ref(false)
const storeProg = ref(null) // { stage, pct, message }

// 构建版本号统一带 v 前缀（-ldflags 注入的是 tag 名），展示层不再重复拼 v；
// dev 等非数字版本保持原样，避免出现 "vdev"。
function tagVer(v) {
  if (!v) return ''
  const s = String(v)
  return /^\d/.test(s) ? `v${s}` : s
}
const verText = computed(() => tagVer(st.value?.version || '1.0.0'))

// —— 更新说明 ——
// release 正文是一份 Markdown。这里不引 md 渲染器，只把它压成「一句概述 + 若干要点」：
// 标题符号、表格、下载文件清单、加粗与行内代码全部剥掉，默认最多列 5 条，
// 剩下的看用户要不要展开。
const NOTE_LIMIT = 5

function cleanMd(s) {
  return String(s)
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/\s+/g, ' ')
    .trim()
}

function parseNotes(raw) {
  const items = []
  let summary = ''
  if (!raw) return { summary, items }
  let skipSection = false
  for (const line of String(raw).split(/\r?\n/)) {
    const t = line.trim()
    if (!t) continue
    if (/^#{1,6}\s/.test(t)) {
      // 小节标题只用来识别「下载 / 安装方式」这类不该占更新卡片的段落
      skipSection = /下载|安装方式|校验值|文件清单/.test(t)
      continue
    }
    if (skipSection) continue
    if (t.startsWith('|') || t.startsWith('>') || t.startsWith('![') || /^:?-{3,}:?$/.test(t)) continue
    const bullet = t.match(/^(?:[-*+]|\d+[.)])\s+(.+)$/)
    if (bullet) {
      const txt = cleanMd(bullet[1])
      if (txt) items.push(txt)
      continue
    }
    if (!summary) summary = cleanMd(t)
  }
  return { summary, items }
}

const notes = computed(() => parseNotes(updateInfo.value?.notes))
const shownNotes = computed(() =>
  notesOpen.value ? notes.value.items : notes.value.items.slice(0, NOTE_LIMIT)
)
const moreCount = computed(() => Math.max(0, notes.value.items.length - NOTE_LIMIT))

// —— 更新进度 ——
const STAGE_LABEL = {
  download: '下载更新包',
  unpack: '校验并解包',
  apply: '替换程序文件',
  restart: '重启应用',
  done: '更新完成'
}
const progPct = computed(() => Math.max(0, Math.min(100, Math.round(Number(selfProg.value?.pct) || 0))))
const progLabel = computed(() => selfProg.value?.message || STAGE_LABEL[selfProg.value?.stage] || '准备中')

const showUpdateCard = computed(
  () => !!updateInfo.value?.available && skipVer.value !== updateInfo.value.version
)

function setUpdate(u) {
  if (!u?.available) return
  updateInfo.value = u
  updateMsg.value = `发现新版本 ${tagVer(u.version)}`
  notesOpen.value = false
  selfError.value = ''
}

function skipUpdate() {
  const v = updateInfo.value?.version
  if (!v) return
  skipVer.value = v
  localStorage.setItem('envswitch-skip-update', v)
  updateMsg.value = `已暂不更新 ${tagVer(v)}，有更新的版本时再提醒`
}

function restoreUpdate() {
  skipVer.value = ''
  localStorage.removeItem('envswitch-skip-update')
  if (updateInfo.value?.available) updateMsg.value = `发现新版本 ${tagVer(updateInfo.value.version)}`
}

let offUpdate = () => {}
let offInstall = () => {}
let offStore = () => {}

onMounted(async () => {
  try {
    st.value = await api.settings()
    setUpdate(await api.pendingUpdate())
  } catch (err) {
    // 降级保护
  }
  offUpdate = on('update', setUpdate)
  offInstall = on('install', (p) => {
    if (p.tool !== 'self') return
    if (p.stage === 'error') {
      applyingUpdate.value = false
      selfProg.value = null
      selfError.value = p.message || '更新失败'
      return
    }
    selfError.value = ''
    selfProg.value = { stage: p.stage, pct: p.pct, speed: p.speed, message: p.message }
  })
  offStore = on('store-migrate', (p) => {
    if (!storeBusy.value) return
    storeProg.value = { stage: p.stage, pct: p.pct, message: p.message }
  })
})

onUnmounted(() => {
  offUpdate()
  offInstall()
  offStore()
})

async function refreshSettings() {
  try {
    st.value = await api.settings()
  } catch (e) {
    // 降级保护：保留旧值，下次进入设置页再取
  }
}

async function pickStoreDir() {
  if (storeBusy.value) return
  try {
    const dir = await api.pickDirectory('选择新的版本库目录（必须是空目录）', st.value?.storePath || '')
    if (dir) storeConfirm.value = dir
  } catch (e) {
    toast(String(e), 'err')
  }
}

function samePath(a, b) {
  if (!a || !b) return false
  const norm = (p) => String(p).replace(/[\\/]+$/, '').toLowerCase()
  return norm(a) === norm(b)
}

async function doChangeStore() {
  if (storeBusy.value || !storeConfirm.value) return
  const target = storeConfirm.value
  storeBusy.value = true
  storeProg.value = { stage: 'scan', pct: 0, message: '准备迁移…' }
  try {
    await api.changeStore(target)
    storeConfirm.value = ''
    await refreshSettings()
    toast('版本库已迁移')
  } catch (e) {
    toast(String(e), 'err')
    // 可能只是收尾失败（旧目录被占用），配置已经切过去了：刷新并收起确认条
    await refreshSettings()
    if (samePath(st.value?.storePath, target)) storeConfirm.value = ''
  } finally {
    storeBusy.value = false
    storeProg.value = null
  }
}

async function checkUpdate() {
  checkingUpdate.value = true
  updateMsg.value = '正在检查更新…'
  selfProg.value = null
  selfError.value = ''
  try {
    const res = await api.checkUpdateNow()
    if (res?.available) {
      setUpdate(res)
    } else if (res?.error) {
      updateMsg.value = `检查失败：${res.error}`
    } else {
      updateInfo.value = null
      updateMsg.value = '当前已是最新版本'
    }
  } catch (e) {
    updateMsg.value = `检查失败：${e}`
  } finally {
    checkingUpdate.value = false
  }
}

async function doApplyUpdate() {
  if (applyingUpdate.value) return
  applyingUpdate.value = true
  selfError.value = ''
  selfProg.value = { stage: 'download', pct: 0, speed: '', message: '正在准备更新…' }
  try {
    await api.applyUpdate()
    // 成功路径不返回：进度事件推到 100% 后进程随即重启
  } catch (e) {
    applyingUpdate.value = false
    selfProg.value = null
    selfError.value = String(e).replace(/^(Error|error)\s*:\s*/, '')
  }
}

async function setKv(k, v) {
  try {
    await api.setField(k, v)
    toast('设置已保存')
    return true
  } catch (e) {
    toast(String(e), 'err')
    return false
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
  const prev = st.value[name]
  st.value[name] = !prev
  // 迁移/其他版本库操作进行时后端会拒绝写入，开关要退回原状态
  if (!(await setKv(name, st.value[name]))) st.value[name] = prev
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

            <!-- 发现新版本：要点式说明 + 固定位置的「立即更新」+ 全流程进度 -->
            <div class="update-card" v-if="showUpdateCard">
              <div class="uc-head">
                <span class="uc-tag-new">新版本</span>
                <span class="uc-ver num">{{ tagVer(updateInfo.version) }}</span>
                <span class="uc-size num">当前 {{ verText }} · 安装包 {{ updateInfo.size }}</span>
                <button class="uc-skip" :disabled="applyingUpdate" @click="skipUpdate">暂不更新</button>
              </div>

              <p class="uc-summary" v-if="notes.summary">{{ notes.summary }}</p>

              <ul class="uc-notes" v-if="notes.items.length">
                <li v-for="(n, i) in shownNotes" :key="i">{{ n }}</li>
              </ul>

              <button class="uc-more" v-if="moreCount" @click="notesOpen = !notesOpen">
                {{ notesOpen ? '收起更新说明' : `展开其余 ${moreCount} 条` }}
                <Icon class="uc-more-ic" :class="{ open: notesOpen }" name="chevr" :size="11" />
              </button>

              <div class="uc-prog" v-if="selfProg">
                <div class="uc-prog-top">
                  <span>{{ progLabel }}</span>
                  <span class="uc-prog-num num">
                    <template v-if="selfProg.stage === 'download' && selfProg.speed">{{ selfProg.speed }} · </template>{{ progPct }}%
                  </span>
                </div>
                <div class="uc-bar">
                  <div class="uc-bar-fill" :style="{ width: progPct + '%' }"></div>
                </div>
              </div>

              <p class="uc-err" v-if="selfError">
                <Icon name="alert" :size="13" />
                <span class="uc-err-txt">{{ selfError }}</span>
                <a class="uc-dl" href="javascript:void(0)" @click="openExternal(REPO + '/releases/latest')">下载安装包手动更新</a>
              </p>

              <div class="uc-foot">
                <a
                  class="uc-link"
                  href="javascript:void(0)"
                  @click="openExternal(REPO + '/releases/tag/' + updateInfo.version)"
                >查看完整更新说明</a>
                <span class="uc-hint">原地热替换 · 失败自动回滚</span>
                <button class="btn primary uc-apply" :disabled="applyingUpdate" @click="doApplyUpdate">
                  <Icon :name="applyingUpdate ? 'refresh' : 'down'" :size="14" :class="{ spin: applyingUpdate }" />
                  <span>{{ applyingUpdate ? '正在更新…' : '立即更新' }}</span>
                </button>
              </div>
            </div>

            <div class="uc-skipped" v-else-if="updateInfo?.available">
              <span>已暂不更新 {{ tagVer(updateInfo.version) }}</span>
              <a class="uc-link" href="javascript:void(0)" @click="restoreUpdate">重新显示</a>
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
                <span class="cfg-desc">各个工具版本的物理安装路径；可整体换到其他磁盘</span>
              </div>
              <div class="cfg-ctrl path-row">
                <input class="cfg-input wide-input num" :value="st.storePath" readonly />
                <button
                  class="btn sm ghost open-dir-btn"
                  :disabled="storeBusy"
                  title="更换版本库所在磁盘或目录"
                  @click="pickStoreDir"
                >
                  <Icon name="swap" :size="13" />
                  <span>更改位置</span>
                </button>
                <button class="btn sm ghost open-dir-btn" @click="api.openStoreDir()" title="在文件资源管理器中打开">
                  <Icon name="folder" :size="13" />
                  <span>打开目录</span>
                </button>
              </div>
            </div>

            <!-- 迁移确认：先选目录，再明确告知要搬多少版本、失败会回滚 -->
            <div v-if="storeConfirm && !storeBusy" class="store-confirm">
              <div class="sc-main">
                <span class="sc-title">迁移版本库到新位置？</span>
                <span class="sc-path num" :title="storeConfirm">{{ storeConfirm }}</span>
                <span class="sc-note">
                  将整体搬走 {{ st.installedCount || 0 }} 个已装版本（目录必须是空的），迁移期间请勿关闭应用；
                  失败会自动回滚，原目录不受影响。
                </span>
              </div>
              <div class="sc-actions">
                <button class="btn sm ghost" @click="storeConfirm = ''">取消</button>
                <button class="btn sm primary" @click="doChangeStore">确认迁移</button>
              </div>
            </div>

            <div v-if="storeBusy && storeProg" class="store-prog">
              <div class="sp-top">
                <span>{{ storeProg.message || '迁移中…' }}</span>
                <span class="num">{{ Math.round(storeProg.pct || 0) }}%</span>
              </div>
              <div class="sp-bar">
                <div class="sp-fill" :style="{ width: Math.max(0, Math.min(100, storeProg.pct || 0)) + '%' }"></div>
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
  box-shadow: 0 0 10px var(--accent-line), inset 0 1px 1px rgba(255, 255, 255, 0.2);
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
  justify-content: flex-end;
  flex-wrap: wrap;
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

/* 版本库迁移：确认条与进度条都贴在「本地存储」面板里 */
.store-confirm {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin: 12px 16px;
  padding: 12px 14px;
  background: var(--warn-bg);
  border: 1px solid var(--warn-line);
  border-radius: var(--r-ctl);
}

.sc-main {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.sc-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.sc-path {
  font-size: 11.5px;
  color: var(--warn);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sc-note {
  font-size: 11px;
  line-height: 1.5;
  color: var(--ink-3);
}

.sc-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.store-prog {
  margin: 12px 16px;
  padding: 10px 14px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: var(--r-ctl);
}

.sp-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 6px;
  font-size: 11.5px;
  color: var(--ink-2);
}

.sp-top > span:first-child {
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.sp-bar {
  height: 5px;
  background: var(--hairline-strong);
  border-radius: 999px;
  overflow: hidden;
}

.sp-fill {
  height: 100%;
  background: var(--accent);
  transition: width 0.18s var(--ease);
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

/* ================== 发现新版本：更新卡片 ================== */
.update-card {
  display: flex;
  flex-direction: column;
  gap: 9px;
  margin: 2px 16px 14px;
  padding: 12px 14px;
  background: var(--surface-soft);
  border: 1px solid var(--accent-line);
  border-radius: 10px;
}

.uc-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.uc-tag-new {
  font-size: 10.5px;
  font-weight: 700;
  color: var(--accent-ink);
  background: var(--accent-dim);
  border: 1px solid var(--accent-line);
  border-radius: 999px;
  padding: 1px 8px;
  flex: none;
}

.uc-ver {
  font-size: 14px;
  font-weight: 700;
  color: var(--ink-1);
  letter-spacing: -0.01em;
}

.uc-size {
  flex: 1;
  font-size: 11px;
  color: var(--ink-3);
}

.uc-skip {
  flex: none;
  font-size: 11px;
  color: var(--ink-3);
  background: none;
  border: none;
  padding: 2px 6px;
  border-radius: 5px;
  transition: all 0.15s var(--ease);
}

.uc-skip:hover:not([disabled]) {
  color: var(--ink-1);
  background: var(--surface-raised);
}

.uc-skip[disabled] {
  opacity: 0.45;
  cursor: not-allowed;
}

.uc-summary {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--ink-2);
}

.uc-notes {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.uc-notes li {
  position: relative;
  padding-left: 13px;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--ink-2);
}

.uc-notes li::before {
  content: '';
  position: absolute;
  left: 2px;
  top: 6px;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--accent);
  opacity: 0.7;
}

.uc-more {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: var(--accent);
  background: none;
  border: none;
  padding: 0;
}

.uc-more:hover {
  text-decoration: underline;
}

.uc-more-ic {
  transition: transform 0.18s var(--ease);
}

.uc-more-ic.open {
  transform: rotate(90deg);
}

/* —— 进度：下载 → 校验解包 → 替换 → 重启，一条进度条走完全程 —— */
.uc-prog {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.uc-prog-top {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  font-size: 11px;
  color: var(--ink-2);
}

.uc-prog-num {
  flex: none;
  font-weight: 600;
  color: var(--accent);
}

.uc-bar {
  height: 5px;
  border-radius: 999px;
  background: var(--hairline-strong);
  overflow: hidden;
}

.uc-bar-fill {
  height: 100%;
  border-radius: 999px;
  background: var(--accent);
  box-shadow: 0 0 8px var(--accent-line);
  transition: width 0.35s var(--ease);
}

.uc-err {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: 0;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--danger);
  word-break: break-all;
}

.uc-foot {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-top: 1px;
  border-top: 1px solid var(--hairline);
  margin-top: 1px;
}

.uc-hint {
  font-size: 10.5px;
  color: var(--ink-3);
}

/* 主操作固定在卡片右下角，尺寸不随说明文字长度变化 */
.uc-apply {
  margin-left: auto;
  flex: none;
  width: 116px;
  height: 32px;
  justify-content: center;
  white-space: nowrap;
}

.uc-err-txt {
  flex: 1;
  min-width: 0;
  word-break: break-all;
}

/* 跨盘等旧版本固有误操作导致失败时，给一条不再依赖自更新的退路 */
.uc-dl {
  flex: none;
  font-size: 11px;
  color: var(--accent);
  text-decoration: none;
  white-space: nowrap;
}

.uc-dl:hover {
  text-decoration: underline;
}

.uc-skipped {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0 16px 14px;
  font-size: 11px;
  color: var(--ink-3);
}

.uc-link {
  font-size: 11px;
  color: var(--accent);
  text-decoration: none;
}

.uc-link:hover {
  text-decoration: underline;
}

.spin {
  animation: uc-spin 0.9s linear infinite;
}

@keyframes uc-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .spin {
    animation: none;
  }
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
