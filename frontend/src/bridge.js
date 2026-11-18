// 手写 wails 绑定代理：与 wails CLI 生成文件同构（window.go.app.App.*），
// dev 模式下浏览器直开时全部返回降级数据，保证 UI 可独立调试。
const g = () => (window.go && window.go.app && window.go.app.App) || null

function call (method, ...args) {
  const b = g()
  if (!b) return Promise.resolve(demo(method, args))
  return b[method](...args)
}

let demoNetProxy = {
  version: 1,
  mode: 'off',
  fixedId: '',
  settings: { testUrl: 'https://api.ipify.org?format=json', timeoutMs: 15000 },
  items: [
    {
      id: 'demo_1',
      name: 'Clash 混合代理 (Verge)',
      protocol: 'http',
      host: '127.0.0.1',
      port: 7897,
      username: '',
      hasPassword: false,
      enabled: true,
      source: 'clash',
      clashUid: '__mixed__',
      lastTest: { success: true, status: 200, ip: '104.28.19.42', durationMs: 186, error: '', at: Date.now() - 3600000 },
      createdAt: Date.now() - 86400000,
      updatedAt: Date.now() - 86400000
    },
    {
      id: 'demo_2',
      name: '香港高速出口 01',
      protocol: 'socks5',
      host: '127.0.0.1',
      port: 10808,
      username: '',
      hasPassword: false,
      enabled: true,
      source: 'manual',
      lastTest: null,
      createdAt: Date.now() - 43200000,
      updatedAt: Date.now() - 43200000
    }
  ]
}

function demo (method, args) {
  if (method === 'NetProxyList') return JSON.parse(JSON.stringify(demoNetProxy))
  if (method === 'NetProxySave') {
    const draft = args[0] || {}
    let it = demoNetProxy.items.find(i => i.id === draft.id)
    if (it) {
      Object.assign(it, draft, { updatedAt: Date.now() })
    } else {
      it = {
        id: 'np_' + Math.random().toString(16).slice(2, 10),
        name: draft.name,
        protocol: draft.protocol || 'http',
        host: draft.host,
        port: Number(draft.port),
        username: draft.username || '',
        hasPassword: !!draft.password,
        enabled: draft.enabled !== false,
        source: 'manual',
        lastTest: null,
        createdAt: Date.now(),
        updatedAt: Date.now()
      }
      demoNetProxy.items.push(it)
    }
    return JSON.parse(JSON.stringify(it))
  }
  if (method === 'NetProxyRemove') {
    const id = args[0]
    demoNetProxy.items = demoNetProxy.items.filter(i => i.id !== id)
    if (demoNetProxy.fixedId === id) {
      demoNetProxy.fixedId = ''
      if (demoNetProxy.mode === 'fixed') demoNetProxy.mode = 'off'
    }
    return null
  }
  if (method === 'NetProxyToggle') {
    const [id, enabled] = args
    const it = demoNetProxy.items.find(i => i.id === id)
    if (it) it.enabled = !!enabled
    return it ? JSON.parse(JSON.stringify(it)) : null
  }
  if (method === 'NetProxySaveSettings') {
    const req = args[0] || {}
    if (req.mode) demoNetProxy.mode = req.mode
    if (req.fixedId !== undefined) demoNetProxy.fixedId = req.fixedId
    if (req.testUrl) demoNetProxy.settings.testUrl = req.testUrl
    if (req.timeoutMs) demoNetProxy.settings.timeoutMs = req.timeoutMs
    return JSON.parse(JSON.stringify(demoNetProxy))
  }
  if (method === 'NetProxyTest') {
    const target = args[0] || {}
    const res = {
      success: true,
      status: 200,
      ip: '104.28.19.42',
      durationMs: 142 + Math.floor(Math.random() * 60),
      error: '',
      at: Date.now()
    }
    if (target.id) {
      const it = demoNetProxy.items.find(i => i.id === target.id)
      if (it) it.lastTest = res
    }
    return res
  }
  if (method === 'NetProxyClashSync') {
    return {
      ok: true,
      message: '成功从 Clash Verge 同步混合端口 (7897)',
      added: 1,
      updated: 0,
      items: demoNetProxy.items
    }
  }
  if (method === 'Overview') {
    const mk = (id, title, kind, group, lamp, cur) => ({
      id, title, kind, group, lamp, remoteState: 'pending',
      current: cur ? { version: cur, source: kind === 'js' ? 'store' : (id === 'node' ? 'nvm' : 'store'), size: '—', active: true } : null
    })
    return [
      mk('node', 'node.js', 'runtime', 'runtime', 'ok', '22.22.0'),
      mk('python', 'python', 'runtime', 'runtime', 'idle', null),
      mk('go', 'Go', 'runtime', 'runtime', 'ok', '1.26.5'),
      mk('deno', 'Deno', 'runtime', 'runtime', 'off', null),
      mk('bun', 'Bun', 'runtime', 'runtime', 'off', null),
      mk('npm', 'npm', 'js', 'packages', 'idle', null),
      mk('pnpm', 'pnpm', 'js', 'packages', 'up', null),
      mk('yarn', 'yarn classic', 'js', 'packages', 'off', null),
      mk('java', 'JDK (Temurin)', 'runtime', 'jvm', 'ok', '21.0.5'),
      mk('maven', 'Maven', 'runtime', 'jvm', 'idle', null),
      mk('gradle', 'Gradle', 'runtime', 'jvm', 'off', null),
      mk('dotnet', '.NET SDK', 'runtime', 'dotnet', 'off', null),
      mk('kubectl', 'kubectl', 'runtime', 'cloud', 'off', null),
      mk('terraform', 'Terraform', 'runtime', 'cloud', 'off', null)
    ]
  }
  if (method === 'Local') return []
  if (method === 'Remote') return []
  if (method === 'Doctor') return []
  if (method === 'Settings') return { autostart: false, closeToTray: true, checkUpdateOnStart: true, storePath: '(dev)', mirrors: {}, proxy: '', indexCacheMinutes: 10, version: 'dev', remoteState: {}, totalSize: '—' }
  return null
}

export const api = {
  overview: () => call('Overview'),
  local: (t) => call('Local', t),
  remote: (t) => call('Remote', t),
  doctor: () => call('Doctor'),
  settings: () => call('Settings'),
  use: (t, v) => call('Use', t, v),
  install: (t, v) => call('Install', t, v),
  cancelInstall: (t) => call('CancelInstall', t),
  remove: (t, v, cascade) => call('Remove', t, v, !!cascade),
  setAutostart: (on) => call('SetAutostart', on),
  setField: (k, v) => call('SetField', k, String(v)),
  refreshIndex: (t) => call('RefreshIndex', t),
  openStoreDir: () => call('OpenStoreDir'),
  pendingUpdate: () => call('PendingUpdate'),
  checkUpdate: () => call('CheckUpdateNow'),
  applyUpdate: () => call('ApplyUpdate'),
  netproxyList: () => call('NetProxyList'),
  netproxySave: (draft) => call('NetProxySave', draft),
  netproxyRemove: (id) => call('NetProxyRemove', id),
  netproxyToggle: (id, enabled) => call('NetProxyToggle', id, !!enabled),
  netproxySaveSettings: (req) => call('NetProxySaveSettings', req),
  netproxyTest: (draft) => call('NetProxyTest', draft),
  netproxyClashSync: () => call('NetProxyClashSync')
}

// wails 事件桥（dev 模式无 window.runtime 时空实现）
export function on (name, cb) {
  if (window.runtime && window.runtime.EventsOn) return window.runtime.EventsOn(name, cb)
  return () => {}
}

// 窗口操作：frameless 模式下由 TitleBar 调用；浏览器 dev 模式静默降级
const rt = () => window.runtime
export const win = {
  minimise () { rt()?.WindowMinimise() },
  toggleMaximise () { rt()?.WindowToggleMaximise() },
  close () { rt()?.WindowClose() },
  isWails () { return !!window.runtime }
}
export function openExternal (url) {
  if (window.runtime?.BrowserOpenURL) return window.runtime.BrowserOpenURL(url)
  window.open(url, '_blank')
}
