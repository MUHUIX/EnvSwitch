<script setup>
import { ref, computed, reactive, inject, onMounted } from 'vue'
import { api } from '../bridge'
import Icon from '../components/Icon.vue'

const toast = inject('toast')

// 数据状态
const loading = ref(false)
const netData = ref({
  version: 1,
  mode: 'off',
  fixedId: '',
  settings: { testUrl: 'https://api.ipify.org?format=json', timeoutMs: 15000 },
  items: []
})

const mode = ref('off')
const fixedId = ref('')
const selectedIds = ref(new Set())
const testing = reactive({})
const batchTesting = ref(false)
const batchDeleting = ref(false)
const clashBusy = ref(false)
const copiedId = ref('')
let copiedTimer = 0

const items = computed(() => netData.value?.items || [])
const selectedCount = computed(() => items.value.filter(i => selectedIds.value.has(i.id)).length)
const allSelected = computed(() => items.value.length > 0 && selectedCount.value === items.value.length)
const enabledCount = computed(() => items.value.filter(i => i.enabled).length)

// 当前激活生效的代理出口信息（用于开发者环境卡片展示）
const activeEgress = computed(() => {
  if (mode.value === 'off') return null
  if (mode.value === 'fixed') {
    return items.value.find(i => i.id === fixedId.value && i.enabled) || null
  }
  if (mode.value === 'rotate') {
    return items.value.find(i => i.enabled) || null
  }
  return null
})

// 时间格式化
function fmtAgo(timestamp) {
  if (!timestamp) return ''
  const diff = Date.now() - timestamp
  if (diff < 60000) return '刚刚'
  if (diff < 3600000) return `${Math.floor(diff / 60000)} 分钟前`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)} 小时前`
  return `${Math.floor(diff / 86400000)} 天前`
}

function fmtMs(ms) {
  if (ms == null || ms < 0) return '—'
  return `${ms} ms`
}

function addressOf(item) {
  return `${item.protocol}://${item.host}:${item.port}`
}

function copyAddress(item) {
  const addr = addressOf(item)
  navigator.clipboard.writeText(addr).then(() => {
    copiedId.value = item.id
    clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => { copiedId.value = '' }, 1500)
    toast('已复制代理地址')
  }).catch(() => {
    toast('复制失败（系统剪贴板不可用）', 'err')
  })
}

// 加载配置
async function load() {
  loading.value = true
  try {
    const res = await api.netproxyList()
    if (res) {
      netData.value = res
      mode.value = res.mode || 'off'
      fixedId.value = res.fixedId || ''
      const alive = new Set((res.items || []).map(i => i.id))
      selectedIds.value = new Set([...selectedIds.value].filter(id => alive.has(id)))
    }
  } catch (e) {
    toast(`加载代理配置失败: ${String(e)}`, 'err')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
})

// 策略变更与保存
async function saveStrategy(newMode, newFixedId = fixedId.value) {
  if (newMode === 'fixed' && !newFixedId) {
    const firstEnabled = items.value.find(i => i.enabled)
    if (firstEnabled) {
      newFixedId = firstEnabled.id
      fixedId.value = firstEnabled.id
    } else {
      toast('请先选择或启用一条固定出口', 'err')
      return
    }
  }

  try {
    const res = await api.netproxySaveSettings({
      mode: newMode,
      fixedId: newFixedId,
      testUrl: netData.value.settings?.testUrl,
      timeoutMs: netData.value.settings?.timeoutMs
    })
    if (res) {
      netData.value = res
      mode.value = res.mode
      fixedId.value = res.fixedId
      toast('出口策略已保存并实时生效')
    }
  } catch (e) {
    toast(`保存策略失败: ${String(e)}`, 'err')
    mode.value = netData.value.mode
    fixedId.value = netData.value.fixedId
  }
}

function onModeSelect(m) {
  mode.value = m
  saveStrategy(m, fixedId.value)
}

function onFixedSelect(id) {
  fixedId.value = id
  saveStrategy('fixed', id)
}

// 选择管理
function toggleSelect(id) {
  const next = new Set(selectedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selectedIds.value = next
}

function toggleSelectAll() {
  selectedIds.value = allSelected.value ? new Set() : new Set(items.value.map(i => i.id))
}

// 单条启停
async function toggleItem(item) {
  const next = !item.enabled
  try {
    const res = await api.netproxyToggle(item.id, next)
    if (res) {
      Object.assign(item, res)
      toast(next ? `已启用出口「${item.name}」` : `已停用出口「${item.name}」`)
    }
  } catch (e) {
    toast(`切换状态失败: ${String(e)}`, 'err')
    await load()
  }
}

// 单条连通性测试
async function testItem(item) {
  if (testing[item.id]) return
  testing[item.id] = true
  try {
    const res = await api.netproxyTest({
      id: item.id,
      protocol: item.protocol,
      host: item.host,
      port: item.port,
      username: item.username,
      enabled: item.enabled
    })
    item.lastTest = res
    if (res.success) {
      toast(`出口「${item.name}」连通正常 (${fmtMs(res.durationMs)}${res.ip ? ' · ' + res.ip : ''})`)
    } else {
      toast(`出口「${item.name}」连接失败: ${res.error || '超时'}`, 'err')
    }
  } catch (e) {
    toast(`测试异常: ${String(e)}`, 'err')
  } finally {
    testing[item.id] = false
  }
}

// 批量测试
async function batchTest() {
  if (batchTesting.value) return
  const targets = items.value.filter(i => selectedIds.value.has(i.id))
  if (!targets.length) return
  batchTesting.value = true
  toast(`正在批量测试选中的 ${targets.length} 条出口…`)
  try {
    for (const it of targets) {
      await testItem(it)
    }
    toast('批量测试完成')
  } finally {
    batchTesting.value = false
  }
}

// 删除
async function deleteItem(item) {
  if (!confirm(`确定删除出口「${item.name}」(${item.protocol}://${item.host}:${item.port}) 吗？`)) return
  try {
    await api.netproxyRemove(item.id)
    selectedIds.value.delete(item.id)
    toast('已删除出口')
    await load()
  } catch (e) {
    toast(`删除失败: ${String(e)}`, 'err')
  }
}

// 批量删除
async function batchDelete() {
  if (batchDeleting.value) return
  const targets = items.value.filter(i => selectedIds.value.has(i.id))
  if (!targets.length) return
  if (!confirm(`确定删除选中的 ${targets.length} 条出口吗？`)) return
  batchDeleting.value = true
  try {
    for (const item of targets) {
      await api.netproxyRemove(item.id)
      selectedIds.value.delete(item.id)
    }
    toast(`已批量删除 ${targets.length} 条出口`)
    await load()
  } catch (e) {
    toast(`批量删除失败: ${String(e)}`, 'err')
  } finally {
    batchDeleting.value = false
  }
}

// 从 Clash Verge 同步
async function syncFromClash() {
  if (clashBusy.value) return
  clashBusy.value = true
  try {
    const res = await api.netproxyClashSync()
    if (res && res.ok) {
      toast(res.message || 'Clash Verge 代理同步成功')
      await load()
    } else {
      toast(res?.message || '未检测到可用的 Clash Verge 代理配置', 'err')
    }
  } catch (e) {
    toast(`同步失败: ${String(e)}`, 'err')
  } finally {
    clashBusy.value = false
  }
}

// ===== 出口表单弹窗（新增/编辑） =====
const formOpen = ref(false)
const formEditingId = ref('')
const formBusy = ref(false)
const formTesting = ref(false)
const formTestMsg = ref(null)
const form = reactive({
  name: '',
  protocol: 'http',
  host: '',
  port: '',
  username: '',
  password: '',
  enabled: true
})

function openCreate() {
  formEditingId.value = ''
  form.name = ''
  form.protocol = 'http'
  form.host = '127.0.0.1'
  form.port = '7890'
  form.username = ''
  form.password = ''
  form.enabled = true
  formTestMsg.value = null
  formOpen.value = true
}

function openEdit(item) {
  formEditingId.value = item.id
  form.name = item.name
  form.protocol = item.protocol
  form.host = item.host
  form.port = String(item.port)
  form.username = item.username || ''
  form.password = ''
  form.enabled = item.enabled
  formTestMsg.value = null
  formOpen.value = true
}

// 弹窗内未保存先测
async function testDraft() {
  if (formTesting.value) return
  const host = form.host.trim()
  const port = Number(form.port)
  if (!host || !port || port < 1 || port > 65535) {
    formTestMsg.value = { ok: false, text: '请先填写有效的主机与端口 (1-65535)' }
    return
  }
  formTesting.value = true
  formTestMsg.value = { ok: true, text: '正在连接测试网址…' }
  try {
    const res = await api.netproxyTest({
      id: formEditingId.value,
      name: form.name,
      protocol: form.protocol,
      host: host,
      port: port,
      username: form.username,
      password: form.password,
      enabled: form.enabled
    })
    if (res.success) {
      formTestMsg.value = {
        ok: true,
        text: `连通可用 · 状态码 ${res.status || 200}${res.ip ? ' · 出口 IP ' + res.ip : ''} · 耗时 ${res.durationMs}ms`
      }
    } else {
      formTestMsg.value = { ok: false, text: res.error || '连接超时或握手失败' }
    }
  } catch (e) {
    formTestMsg.value = { ok: false, text: String(e) }
  } finally {
    formTesting.value = false
  }
}

async function saveForm() {
  if (formBusy.value) return
  const name = form.name.trim()
  const host = form.host.trim()
  const port = Number(form.port)
  if (!name) {
    toast('请填写代理名称', 'err')
    return
  }
  if (!host) {
    toast('请填写代理主机地址', 'err')
    return
  }
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    toast('代理端口必须是 1-65535 整数', 'err')
    return
  }

  formBusy.value = true
  try {
    await api.netproxySave({
      id: formEditingId.value || undefined,
      name,
      protocol: form.protocol,
      host,
      port,
      username: form.username.trim(),
      password: form.password,
      enabled: form.enabled
    })
    toast(formEditingId.value ? '已保存出口修改' : '已新增出口条目')
    formOpen.value = false
    await load()
  } catch (e) {
    toast(`保存失败: ${String(e)}`, 'err')
  } finally {
    formBusy.value = false
  }
}

// ===== 代理通用设置弹窗 =====
const settingsOpen = ref(false)
const settingsBusy = ref(false)
const settingsForm = reactive({
  testUrl: '',
  timeoutMs: 15000
})

function openSettings() {
  settingsForm.testUrl = netData.value.settings?.testUrl || 'https://api.ipify.org?format=json'
  settingsForm.timeoutMs = netData.value.settings?.timeoutMs || 15000
  settingsOpen.value = true
}

async function saveSettings() {
  if (settingsBusy.value) return
  settingsBusy.value = true
  try {
    const res = await api.netproxySaveSettings({
      mode: mode.value,
      fixedId: fixedId.value,
      testUrl: settingsForm.testUrl.trim(),
      timeoutMs: Number(settingsForm.timeoutMs) || 15000
    })
    if (res) {
      netData.value = res
      toast('代理设置已保存')
      settingsOpen.value = false
    }
  } catch (e) {
    toast(`保存失败: ${String(e)}`, 'err')
  } finally {
    settingsBusy.value = false
  }
}

// 终端代理命令复制
function copyTerminalCmd(shellType) {
  const eg = activeEgress.value
  if (!eg) {
    toast('当前处于直连模式或无可用出口', 'warn')
    return
  }
  const addr = `${eg.protocol}://${eg.host}:${eg.port}`
  let cmd = ''
  if (shellType === 'pwsh') {
    cmd = `$env:HTTP_PROXY="${addr}"; $env:HTTPS_PROXY="${addr}"; $env:ALL_PROXY="${addr}"`
  } else if (shellType === 'cmd') {
    cmd = `set HTTP_PROXY=${addr} && set HTTPS_PROXY=${addr} && set ALL_PROXY=${addr}`
  } else if (shellType === 'bash') {
    cmd = `export http_proxy="${addr}" https_proxy="${addr}" all_proxy="${addr}"`
  } else if (shellType === 'git') {
    cmd = `git config --global http.proxy ${addr} && git config --global https.proxy ${addr}`
  }
  navigator.clipboard.writeText(cmd).then(() => {
    toast(`已复制 ${shellType.toUpperCase()} 代理命令`)
  }).catch(() => {
    toast('复制失败', 'err')
  })
}
</script>

<template>
  <div class="main netproxy-view">
    <!-- 顶部标题栏 -->
    <header class="topbar">
      <span class="crumb">
        <b>网络代理</b>
      </span>
      <div class="top-actions">
        <button class="btn ghost sm" :disabled="clashBusy" @click="syncFromClash">
          <Icon name="sync" :size="13" :class="{ 'spin-anim': clashBusy }" />
          <span>{{ clashBusy ? '同步中…' : '从 Clash Verge 同步' }}</span>
        </button>
        <button class="btn ghost sm" @click="openSettings">
          <Icon name="gear" :size="13" />
          <span>代理设置</span>
        </button>
        <button class="btn ghost sm" :disabled="loading" @click="load">
          <Icon name="refresh" :size="13" :class="{ 'spin-anim': loading }" />
          <span>刷新</span>
        </button>
        <button class="btn primary sm" @click="openCreate">
          <Icon name="plus" :size="13" />
          <span>新增出口</span>
        </button>
      </div>
    </header>

    <div class="scroll">
      <div class="netproxy-wrap">
        <!-- 卡片一：出口策略（参考 AgentHub 策略控制台） -->
        <div class="cfg-group">
          <h4 class="group-title">出站代理策略</h4>
          <div class="cfg-panel strategy-panel">
            <div class="strategy-row">
              <div class="strategy-info">
                <span class="strategy-name">出口策略模式</span>
                <span class="strategy-desc">
                  直连 = 绕过代理；固定 = 全部下载与网络请求指定走单条出口；轮询 = 每次请求自动轮询已启用的出口
                </span>
                <div class="strategy-meta">
                  共 {{ items.length }} 条出口 · 已启用 {{ enabledCount }} 条
                  <template v-if="activeEgress">
                    · 当前出网: <b class="num accent-text">{{ activeEgress.name }} ({{ activeEgress.protocol }}://{{ activeEgress.host }}:{{ activeEgress.port }})</b>
                  </template>
                  <template v-else-if="mode !== 'off'">
                    · <span class="warn-text">当前暂无可用的启用出口</span>
                  </template>
                </div>
              </div>

              <div class="strategy-controls">
                <div class="mode-seg">
                  <button
                    class="seg-btn"
                    :class="{ on: mode === 'off' }"
                    @click="onModeSelect('off')"
                  >直连</button>
                  <button
                    class="seg-btn"
                    :class="{ on: mode === 'fixed' }"
                    @click="onModeSelect('fixed')"
                  >固定出口</button>
                  <button
                    class="seg-btn"
                    :class="{ on: mode === 'rotate' }"
                    @click="onModeSelect('rotate')"
                  >轮询出口</button>
                </div>

                <!-- 固定出口时的下拉选择框 -->
                <div v-if="mode === 'fixed'" class="fixed-select-wrap">
                  <select
                    class="cfg-select fixed-select"
                    :value="fixedId"
                    @change="onFixedSelect($event.target.value)"
                  >
                    <option value="" disabled>请选择固定出口</option>
                    <option v-for="it in items" :key="it.id" :value="it.id">
                      {{ it.name }} ({{ it.host }}:{{ it.port }}) {{ !it.enabled ? '· 已停用' : '' }}
                    </option>
                  </select>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 批量操作浮动工具条（选中时展开） -->
        <div v-if="selectedCount > 0" class="batch-bar">
          <div class="batch-left">
            <span class="batch-badge">{{ selectedCount }}</span>
            <span class="batch-txt">项已选择</span>
          </div>
          <div class="batch-actions">
            <button class="btn sm" :disabled="batchTesting" @click="batchTest">
              <Icon name="play" :size="12" />
              <span>{{ batchTesting ? '批量测试中…' : '批量测试' }}</span>
            </button>
            <button class="btn sm danger-btn" :disabled="batchDeleting" @click="batchDelete">
              <Icon name="trash" :size="12" />
              <span>{{ batchDeleting ? '删除中…' : '批量删除' }}</span>
            </button>
            <button class="btn sm ghost" @click="selectedIds = new Set()">
              <span>取消选择</span>
            </button>
          </div>
        </div>

        <!-- 卡片二：出口池列表卡片 -->
        <div class="cfg-group">
          <div class="group-header-row">
            <h4 class="group-title">出口池管理</h4>
          </div>

          <div class="cfg-panel tbl-panel">
            <div class="table-container">
              <table class="netproxy-table">
                <thead>
                  <tr>
                    <th class="col-check">
                      <input
                        type="checkbox"
                        class="custom-check"
                        :checked="allSelected"
                        :disabled="!items.length"
                        @change="toggleSelectAll"
                      />
                    </th>
                    <th class="col-name">出口名称</th>
                    <th class="col-addr">代理地址</th>
                    <th class="col-auth">认证</th>
                    <th class="col-enable">启用</th>
                    <th class="col-test">连通性状态</th>
                    <th class="col-ops">操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="it in items" :key="it.id" :class="{ 'row-selected': selectedIds.has(it.id) }">
                    <td class="col-check">
                      <input
                        type="checkbox"
                        class="custom-check"
                        :checked="selectedIds.has(it.id)"
                        @change="toggleSelect(it.id)"
                      />
                    </td>

                    <td class="col-name">
                      <div class="name-cell">
                        <span class="np-title">{{ it.name }}</span>
                        <div class="tags-row">
                          <span class="badge badge-dim">{{ it.protocol.toUpperCase() }}</span>
                          <span v-if="it.source === 'clash'" class="badge badge-info">Clash Verge</span>
                          <span v-if="mode === 'fixed' && fixedId === it.id" class="badge badge-ok">固定生效</span>
                        </div>
                      </div>
                    </td>

                    <td class="col-addr">
                      <div class="addr-cell">
                        <span class="addr-text num">{{ it.host }}:{{ it.port }}</span>
                        <button
                          class="copy-btn"
                          :title="copiedId === it.id ? '已复制' : '复制地址'"
                          @click="copyAddress(it)"
                        >
                          <Icon :name="copiedId === it.id ? 'check' : 'copy'" :size="12" />
                        </button>
                      </div>
                    </td>

                    <td class="col-auth">
                      <span v-if="it.hasPassword" class="auth-tag">账号+密码</span>
                      <span v-else-if="it.username" class="auth-tag">仅账号</span>
                      <span v-else class="auth-none">无认证</span>
                    </td>

                    <td class="col-enable">
                      <span
                        class="toggle"
                        :class="{ on: it.enabled }"
                        :title="it.enabled ? '点击停用' : '点击启用'"
                        @click="toggleItem(it)"
                      >
                        <i class="toggle-ball"></i>
                      </span>
                    </td>

                    <td class="col-test">
                      <div class="test-cell">
                        <template v-if="testing[it.id]">
                          <span class="testing-tag">
                            <span class="dot-flashing"></span>
                            <span>测试中…</span>
                          </span>
                        </template>
                        <template v-else-if="it.lastTest">
                          <div class="test-result">
                            <span class="badge" :class="it.lastTest.success ? 'badge-ok' : 'badge-err'">
                              {{ it.lastTest.success ? '可用' : '失败' }}
                            </span>
                            <span class="test-meta num">
                              <span v-if="it.lastTest.success">{{ fmtMs(it.lastTest.durationMs) }}</span>
                              <span v-if="it.lastTest.ip" class="ip-tag">· {{ it.lastTest.ip }}</span>
                              <span class="time-ago">· {{ fmtAgo(it.lastTest.at) }}</span>
                            </span>
                          </div>
                          <div v-if="!it.lastTest.success && it.lastTest.error" class="err-tip" :title="it.lastTest.error">
                            {{ it.lastTest.error }}
                          </div>
                        </template>
                        <template v-else>
                          <span class="untested-text">未测试</span>
                        </template>
                      </div>
                    </td>

                    <td class="col-ops">
                      <div class="ops-cell">
                        <button
                          class="action-link"
                          :disabled="testing[it.id]"
                          @click="testItem(it)"
                        >测试</button>

                        <button
                          v-if="it.source !== 'clash'"
                          class="action-link"
                          @click="openEdit(it)"
                        >编辑</button>
                        <button
                          v-else
                          class="action-link disabled-link"
                          title="来自 Clash Verge 同步，请在 Clash 中修改"
                        >编辑</button>

                        <button
                          class="action-link danger-link"
                          @click="deleteItem(it)"
                        >删除</button>
                      </div>
                    </td>
                  </tr>

                  <!-- 空状态 -->
                  <tr v-if="!items.length">
                    <td colspan="7" class="empty-cell">
                      <div class="empty-state">
                        <Icon name="proxy" :size="32" class="empty-icon" />
                        <span class="empty-title">暂未添加任何网络代理出口</span>
                        <span class="empty-sub">支持 HTTP / HTTPS 与 SOCKS5 代理协议，或点击右上角从 Clash Verge 一键自动发现</span>
                        <button class="btn primary sm" style="margin-top: 10px;" @click="openCreate">
                          <Icon name="plus" :size="13" />
                          <span>新增出口条目</span>
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <!-- 卡片三：终端与开发环境联动（EnvSwitch 定制提效工具） -->
        <div class="cfg-group">
          <div class="group-header-row">
            <h4 class="group-title">终端与工具链网络代理命令</h4>
          </div>

          <div class="cfg-panel dev-panel">
            <div class="dev-card-grid">
              <div class="dev-item">
                <div class="dev-info">
                  <span class="dev-title">PowerShell 代理命令</span>
                  <span class="dev-desc">设置当前会话的 $env:HTTP_PROXY / HTTPS_PROXY</span>
                </div>
                <button class="btn sm ghost" @click="copyTerminalCmd('pwsh')">
                  <Icon name="copy" :size="12" />
                  <span>复制命令</span>
                </button>
              </div>

              <div class="dev-item">
                <div class="dev-info">
                  <span class="dev-title">Windows CMD 代理命令</span>
                  <span class="dev-desc">设置当前会话的 set HTTP_PROXY / HTTPS_PROXY</span>
                </div>
                <button class="btn sm ghost" @click="copyTerminalCmd('cmd')">
                  <Icon name="copy" :size="12" />
                  <span>复制命令</span>
                </button>
              </div>

              <div class="dev-item">
                <div class="dev-info">
                  <span class="dev-title">Git 全局代理命令</span>
                  <span class="dev-desc">为 git clone 与 push 加速全局代理</span>
                </div>
                <button class="btn sm ghost" @click="copyTerminalCmd('git')">
                  <Icon name="copy" :size="12" />
                  <span>复制命令</span>
                </button>
              </div>

              <div class="dev-item">
                <div class="dev-info">
                  <span class="dev-title">WSL / Bash 代理命令</span>
                  <span class="dev-desc">Linux 子系统或 Git Bash 环境变量导出</span>
                </div>
                <button class="btn sm ghost" @click="copyTerminalCmd('bash')">
                  <Icon name="copy" :size="12" />
                  <span>复制命令</span>
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 弹窗一：新增 / 编辑出口 -->
    <div v-if="formOpen" class="modal-backdrop" @click.self="formOpen = false">
      <div class="modal-card">
        <div class="modal-head">
          <span class="modal-title">{{ formEditingId ? '编辑网络出口' : '新增网络出口' }}</span>
          <button class="modal-close" @click="formOpen = false">✕</button>
        </div>

        <div class="modal-body">
          <div class="form-row">
            <label class="form-label">出口名称 <span class="req">*</span></label>
            <input
              class="cfg-input"
              v-model="form.name"
              placeholder="例如：本地 Clash 代理、香港线路 01"
              autofocus
            />
          </div>

          <div class="form-row-grid">
            <div class="form-row">
              <label class="form-label">代理协议 <span class="req">*</span></label>
              <select class="cfg-select" v-model="form.protocol">
                <option value="http">HTTP / HTTPS</option>
                <option value="socks5">SOCKS5</option>
              </select>
            </div>

            <div class="form-row">
              <label class="form-label">端口号 (1-65535) <span class="req">*</span></label>
              <input
                type="number"
                min="1"
                max="65535"
                class="cfg-input num"
                v-model="form.port"
                placeholder="7890"
              />
            </div>
          </div>

          <div class="form-row">
            <label class="form-label">主机地址 (IP 或域名) <span class="req">*</span></label>
            <input
              class="cfg-input num"
              v-model="form.host"
              placeholder="127.0.0.1 或 proxy.example.com"
            />
          </div>

          <div class="form-row-grid">
            <div class="form-row">
              <label class="form-label">认证账号 (可选)</label>
              <input
                class="cfg-input"
                v-model="form.username"
                placeholder="无认证请留空"
              />
            </div>

            <div class="form-row">
              <label class="form-label">认证密码 (可选)</label>
              <input
                type="password"
                class="cfg-input"
                v-model="form.password"
                placeholder="无认证请留空"
              />
            </div>
          </div>

          <div class="form-row switch-row">
            <span class="form-label">启用该出口</span>
            <span
              class="toggle"
              :class="{ on: form.enabled }"
              @click="form.enabled = !form.enabled"
            >
              <i class="toggle-ball"></i>
            </span>
          </div>

          <!-- 测试反馈区 -->
          <div v-if="formTestMsg" class="form-test-box" :class="formTestMsg.ok ? 'test-box-ok' : 'test-box-err'">
            <span class="test-icon">{{ formTestMsg.ok ? '✓' : '⚠' }}</span>
            <span class="test-text">{{ formTestMsg.text }}</span>
          </div>
        </div>

        <div class="modal-foot">
          <button
            class="btn ghost sm"
            :disabled="formTesting"
            @click="testDraft"
          >
            <Icon name="play" :size="12" />
            <span>{{ formTesting ? '测试中…' : '连接测试' }}</span>
          </button>
          <div class="foot-right">
            <button class="btn ghost sm" @click="formOpen = false">取消</button>
            <button class="btn primary sm" :disabled="formBusy" @click="saveForm">
              {{ formBusy ? '保存中…' : '保存出口' }}
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 弹窗二：代理全局设置 -->
    <div v-if="settingsOpen" class="modal-backdrop" @click.self="settingsOpen = false">
      <div class="modal-card">
        <div class="modal-head">
          <span class="modal-title">代理连通性探测设置</span>
          <button class="modal-close" @click="settingsOpen = false">✕</button>
        </div>

        <div class="modal-body">
          <div class="form-row">
            <label class="form-label">连通性测试网址 (URL)</label>
            <input
              class="cfg-input num"
              v-model="settingsForm.testUrl"
              placeholder="https://api.ipify.org?format=json"
            />
            <span class="field-hint">用于测试出口连通性与回显公网出口 IP</span>
          </div>

          <div class="form-row">
            <label class="form-label">连接超时时长 (毫秒)</label>
            <input
              type="number"
              min="1000"
              max="60000"
              step="1000"
              class="cfg-input num"
              v-model="settingsForm.timeoutMs"
              placeholder="15000"
            />
            <span class="field-hint">超出此时间则判定代理连接失败 (建议 5000 ~ 30000 毫秒)</span>
          </div>
        </div>

        <div class="modal-foot">
          <div class="foot-right" style="margin-left: auto;">
            <button class="btn ghost sm" @click="settingsOpen = false">取消</button>
            <button class="btn primary sm" :disabled="settingsBusy" @click="saveSettings">
              {{ settingsBusy ? '保存中…' : '保存设置' }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.netproxy-view {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.netproxy-wrap {
  display: flex;
  flex-direction: column;
  gap: 20px;
  max-width: 1080px;
  margin: 0 auto;
  width: 100%;
}

.cfg-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.group-header-row {
  display: flex;
  align-items: baseline;
  gap: 12px;
}

.group-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.cfg-panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--r-panel);
  overflow: hidden;
  transition: border-color 0.2s var(--ease), background-color 0.2s var(--ease);
}

/* 策略卡片 */
.strategy-panel {
  padding: 16px 20px;
}

.strategy-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
}

.strategy-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1;
}

.strategy-name {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.strategy-desc {
  font-size: 12px;
  color: var(--ink-2);
  line-height: 1.5;
}

.strategy-meta {
  font-size: 11.5px;
  color: var(--ink-3);
  margin-top: 2px;
}

.strategy-controls {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: none;
}

.mode-seg {
  display: flex;
  background: var(--surface-raised);
  padding: 3px;
  border-radius: var(--r-ctl);
  border: 1px solid var(--hairline);
}

.seg-btn {
  padding: 4px 14px;
  font-size: 12.5px;
  color: var(--ink-2);
  border-radius: 4px;
  transition: all 0.15s var(--ease);
}

.seg-btn.on {
  background: var(--accent);
  color: #FFFFFF;
  font-weight: 600;
  box-shadow: 0 1px 4px var(--accent-dim);
}

.fixed-select-wrap {
  min-width: 200px;
}

.fixed-select {
  width: 100%;
  height: 32px;
}

/* 批量操作浮动条 */
.batch-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 16px;
  background: var(--surface-raised);
  border: 1px solid var(--accent-line);
  border-radius: var(--r-card);
  animation: fadeIn 0.18s var(--ease);
}

@keyframes fadeIn {
  from { opacity: 0; transform: translateY(-4px); }
  to { opacity: 1; transform: translateY(0); }
}

.batch-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.batch-badge {
  background: var(--accent);
  color: #FFFFFF;
  font-size: 11px;
  font-weight: 600;
  padding: 1px 7px;
  border-radius: 10px;
}

.batch-txt {
  font-size: 12.5px;
  color: var(--ink-1);
}

.batch-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 表格面板 */
.tbl-panel {
  padding: 0;
}

.table-container {
  width: 100%;
  overflow-x: auto;
}

.netproxy-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
  text-align: left;
}

.netproxy-table thead {
  background: var(--surface-soft);
  border-bottom: 1px solid var(--hairline);
}

.netproxy-table th {
  padding: 10px 14px;
  font-weight: 600;
  color: var(--ink-2);
  font-size: 12px;
}

.netproxy-table td {
  padding: 12px 14px;
  border-bottom: 1px solid var(--hairline);
  color: var(--ink-1);
  vertical-align: middle;
}

.netproxy-table tbody tr {
  transition: background-color 0.15s var(--ease);
}

.netproxy-table tbody tr:hover {
  background: var(--surface-soft);
}

.netproxy-table tbody tr.row-selected {
  background: var(--accent-dim);
}

.col-check {
  width: 40px;
  text-align: center;
}

.custom-check {
  cursor: pointer;
  accent-color: var(--accent);
  width: 15px;
  height: 15px;
}

.col-name {
  min-width: 180px;
}

.name-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.np-title {
  font-weight: 600;
  color: var(--ink-1);
}

.tags-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.badge {
  font-size: 10.5px;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 500;
}

.badge-dim {
  background: var(--surface-raised);
  color: var(--ink-2);
  border: 1px solid var(--hairline);
}

.badge-info {
  background: rgba(59, 130, 246, 0.12);
  color: #3B82F6;
  border: 1px solid rgba(59, 130, 246, 0.25);
}

.badge-ok {
  background: rgba(16, 185, 129, 0.12);
  color: var(--ok);
  border: 1px solid rgba(16, 185, 129, 0.25);
}

.badge-err {
  background: rgba(239, 68, 68, 0.12);
  color: var(--danger);
  border: 1px solid rgba(239, 68, 68, 0.25);
}

.col-addr {
  width: 200px;
}

.addr-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.addr-text {
  font-size: 12px;
  color: var(--ink-1);
}

.copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 4px;
  color: var(--ink-3);
  transition: all 0.15s;
}

.copy-btn:hover {
  background: var(--surface-raised);
  color: var(--ink-1);
}

.col-auth {
  width: 100px;
}

.auth-tag {
  font-size: 11px;
  color: var(--ink-2);
}

.auth-none {
  font-size: 11px;
  color: var(--ink-3);
}

.col-enable {
  width: 70px;
}

.col-test {
  min-width: 220px;
}

.test-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.test-result {
  display: flex;
  align-items: center;
  gap: 8px;
}

.test-meta {
  font-size: 11px;
  color: var(--ink-2);
}

.ip-tag {
  color: var(--ink-1);
  font-weight: 500;
}

.time-ago {
  color: var(--ink-3);
}

.testing-tag {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: var(--warn);
}

.untested-text {
  font-size: 11.5px;
  color: var(--ink-3);
}

.err-tip {
  font-size: 10.5px;
  color: var(--danger);
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.col-ops {
  width: 150px;
  text-align: right;
}

.ops-cell {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
}

.action-link {
  font-size: 12px;
  color: var(--accent);
  padding: 2px 4px;
  border-radius: 4px;
  transition: opacity 0.15s;
}

.action-link:hover {
  opacity: 0.8;
  text-decoration: underline;
}

.action-link:disabled {
  opacity: 0.4;
  cursor: not-allowed;
  text-decoration: none;
}

.disabled-link {
  color: var(--ink-3);
  cursor: not-allowed;
}

.danger-link {
  color: var(--danger);
}

.empty-cell {
  padding: 48px 16px !important;
  text-align: center;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.empty-icon {
  color: var(--ink-3);
  opacity: 0.4;
}

.empty-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--ink-1);
}

.empty-sub {
  font-size: 12px;
  color: var(--ink-3);
  max-width: 400px;
  line-height: 1.5;
}

/* 开发者环境卡片 */
.dev-panel {
  padding: 16px 20px;
}

.dev-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
}

.dev-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: var(--r-card);
}

.dev-info {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.dev-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.dev-desc {
  font-size: 11px;
  color: var(--ink-3);
}

/* 开关样式（和 Settings 一致） */
.toggle {
  width: 38px;
  height: 22px;
  border-radius: 11px;
  background: var(--surface-raised);
  border: 1px solid var(--hairline-strong);
  display: flex;
  align-items: center;
  padding: 2px;
  cursor: pointer;
  transition: all 0.2s var(--ease);
}

.toggle.on {
  background: var(--accent);
  border-color: var(--accent);
}

.toggle-ball {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #FFFFFF;
  transition: transform 0.2s var(--ease);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.25);
}

.toggle.on .toggle-ball {
  transform: translateX(16px);
}

/* 表单输入与选择控件 */
.cfg-input {
  height: 32px;
  padding: 0 10px;
  background: var(--surface-raised);
  border: 1px solid var(--hairline);
  border-radius: var(--r-ctl);
  font-size: 12.5px;
  color: var(--ink-1);
  outline: none;
  transition: border-color 0.15s, background-color 0.15s;
}

.cfg-input:focus {
  border-color: var(--accent);
}

.cfg-select {
  height: 32px;
  padding: 0 10px;
  background: var(--surface-raised);
  border: 1px solid var(--hairline);
  border-radius: var(--r-ctl);
  font-size: 12.5px;
  color: var(--ink-1);
  outline: none;
  cursor: pointer;
}

.cfg-select:focus {
  border-color: var(--accent);
}

/* 弹窗通用样式 */
.modal-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  backdrop-filter: blur(4px);
  z-index: 1000;
  display: grid;
  place-items: center;
  padding: 20px;
  animation: fadeIn 0.15s var(--ease);
}

.modal-card {
  width: 100%;
  max-width: 480px;
  background: var(--surface);
  border: 1px solid var(--hairline-strong);
  border-radius: var(--r-panel);
  box-shadow: 0 16px 40px rgba(0, 0, 0, 0.35);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  animation: scaleIn 0.18s var(--ease);
}

@keyframes scaleIn {
  from { opacity: 0; transform: scale(0.96); }
  to { opacity: 1; transform: scale(1); }
}

.modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--hairline);
}

.modal-title {
  font-size: 14.5px;
  font-weight: 600;
  color: var(--ink-1);
}

.modal-close {
  font-size: 15px;
  color: var(--ink-3);
  padding: 4px 6px;
  border-radius: 4px;
}

.modal-close:hover {
  background: var(--surface-raised);
  color: var(--ink-1);
}

.modal-body {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.modal-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 20px;
  background: var(--surface-soft);
  border-top: 1px solid var(--hairline);
}

.foot-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.form-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-row-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.form-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--ink-2);
}

.req {
  color: var(--danger);
}

.switch-row {
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
  padding-top: 4px;
}

.field-hint {
  font-size: 11px;
  color: var(--ink-3);
}

.form-test-box {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-radius: var(--r-ctl);
  font-size: 12px;
}

.test-box-ok {
  background: rgba(16, 185, 129, 0.1);
  border: 1px solid rgba(16, 185, 129, 0.25);
  color: var(--ok);
}

.test-box-err {
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.25);
  color: var(--danger);
}

.spin-anim {
  animation: spin 0.9s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.accent-text {
  color: var(--accent);
}

.warn-text {
  color: var(--warn);
}

.danger-btn {
  color: var(--danger);
  border-color: rgba(239, 68, 68, 0.35);
}

.danger-btn:hover {
  background: rgba(239, 68, 68, 0.12);
  border-color: var(--danger);
}
</style>
