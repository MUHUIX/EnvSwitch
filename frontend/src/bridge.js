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

// dev 模式（浏览器直开、无 wails 运行时）的自更新演示：清单给一份真实形态的
// Markdown 说明，ApplyUpdate 按 core 的 stage/pct 契约推一遍进度，方便不打包调 UI。
const demoUpdate = {
  available: true,
  version: 'v1.3.1',
  size: '8.9 MB',
  error: '',
  notes: [
    '# EnvSwitch v1.3.0',
    '',
    '这版能把 nvm 收编进来的版本也直接卸载了，修掉热更新跨盘失败、明亮主题重做了一遍。',
    '',
    '## 新增：卸载 nvm 收编的版本',
    '',
    '- 卸载按钮不再只对「版本库」里的版本开放：nvm 收编进来的版本同样可卸载；系统 PATH 探测到的行仍是只读。',
    '- 真删目录前有一整套安全托底：目录必须真实存在且是目录、必须严格位于 nvm 根目录第一层、本身不能是 junction。',
    '- 卸载当前生效版本会自动摘掉激活链接并切到剩余最新版本；只剩最后一个可卸载版本时按钮禁用。',
    '- 版本写法托底：`v22.22.0` 这类带前缀的写法先归一化再找版本库。',
    '',
    '## 问题修复',
    '',
    '- **热更新报「放置新版本失败」**：更新包暂存在 C:\\ 而程序装在别的盘时，`os.Rename` 直接报 ERROR_NOT_SAME_DEVICE。',
    '- **热更新后新版本起不来**：旧进程只是隐藏不退，新进程拿不到单实例锁。',
    '- **托盘退出一次后，点 × 变成直接关程序**：旧实现把 closeToTray=false 写进了设置文件。',
    '- **卸载后版本还赖在列表里**：删完目录没清系统环境探测缓存。',
    '',
    '## 下载',
    '',
    '| 文件 | 用途 |',
    '| --- | --- |',
    '| `EnvSwitch-Setup-v1.3.0.exe` | 安装版 |',
    '',
    '已有 v1.x 可直接在设置页「软件更新」里热更新。'
  ].join('\n')
}

function simulateUpdate (step) {
  const steps = [
    { stage: 'download', pct: 0, message: '开始下载更新包' },
    { stage: 'download', pct: 26, speed: '3.4 MB/s', message: '正在下载更新包 2.3 MB / 8.9 MB' },
    { stage: 'download', pct: 61, speed: '4.1 MB/s', message: '正在下载更新包 6.3 MB / 8.9 MB' },
    { stage: 'download', pct: 85, speed: '3.8 MB/s', message: '正在下载更新包 8.9 MB / 8.9 MB' },
    { stage: 'unpack', pct: 90, message: '校验通过，正在解包…' },
    { stage: 'apply', pct: 96, message: '正在替换程序文件…' },
    { stage: 'restart', pct: 98, message: '替换完成，正在重启…' },
    { stage: 'done', pct: 100, message: '更新完成，正在重启 EnvSwitch…' }
  ]
  if (step != null && steps[step]) {
    setTimeout(() => devEmit('install', { tool: 'self', version: 'v1.3.1', speed: '', ...steps[step] }), 120)
    return
  }
  steps.forEach((s, i) => setTimeout(() => devEmit('install', { tool: 'self', version: 'v1.3.1', speed: '', ...s }), 320 * (i + 1)))
}

function demo (method, args) {
  if (method === 'ApplyUpdate') { simulateUpdate(); return null }
  if (method === 'PendingUpdate' || method === 'CheckUpdateNow') {
    // ?apply=1 自动走一遍进度；?apply=1&step=N 只推第 N 步，便于定格截图
    const q = new URLSearchParams(location.search)
    if (method === 'PendingUpdate' && q.get('apply') === '1') {
      if (q.get('step') === 'err') {
        setTimeout(() => devEmit('install', {
          tool: 'self', stage: 'error', version: 'v1.3.1', pct: 0,
          message: '放置新版本失败: rename C:\\Users\\me\\AppData\\Local\\EnvSwitch\\update\\staged\\EnvSwitch.exe E:\\EnvSwitch\\EnvSwitch.exe: 系统无法将文件移到不同的磁盘驱动器。'
        }), 120)
      } else {
        simulateUpdate(q.has('step') ? Number(q.get('step')) : null)
      }
    }
    return JSON.parse(JSON.stringify(demoUpdate))
  }
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
      id, title, kind, group, lamp, remoteState: 'pending', installed: [],
      current: cur ? { version: cur, source: kind === 'js' ? 'store' : (id === 'node' ? 'nvm' : 'store'), size: '—', active: true } : null
    })
    return [
      mk('node', 'node.js', 'runtime', 'runtime', 'ok', '22.22.0'),
      mk('python', 'python', 'runtime', 'runtime', 'idle', null),
      mk('go', 'Go', 'runtime', 'runtime', 'ok', '1.26.5'),
      mk('rust', 'Rust', 'runtime', 'runtime', 'off', null),
      mk('php', 'PHP', 'runtime', 'runtime', 'off', null),
      mk('deno', 'Deno', 'runtime', 'runtime', 'off', null),
      mk('bun', 'Bun', 'runtime', 'runtime', 'off', null),
      mk('npm', 'npm', 'js', 'packages', 'idle', null),
      mk('pnpm', 'pnpm', 'js', 'packages', 'up', null),
      mk('yarn', 'yarn classic', 'js', 'packages', 'off', null),
      mk('pip', 'pip', 'runtime', 'packages', 'ok', '22.3'),
      mk('nvm', 'nvm-windows', 'runtime', 'packages', 'ok', '1.2.2'),
      mk('uv', 'uv', 'runtime', 'packages', 'off', null),
      mk('git', 'Git', 'runtime', 'packages', 'ok', '2.49.0'),
      mk('java', 'JDK (Temurin)', 'runtime', 'enterprise', 'ok', '1.8.0_102'),
      mk('maven', 'Maven', 'runtime', 'enterprise', 'idle', null),
      mk('gradle', 'Gradle', 'runtime', 'enterprise', 'off', null),
      mk('dotnet', '.NET SDK', 'runtime', 'enterprise', 'off', null),
      mk('docker', 'Docker', 'runtime', 'cloud', 'ok', '27.3.1'),
      mk('kubectl', 'kubectl', 'runtime', 'cloud', 'off', null),
      mk('terraform', 'Terraform', 'runtime', 'cloud', 'off', null),
      mk('redis', 'Redis', 'runtime', 'cloud', 'ok', '5.0.14')
    ]
  }
  if (method === 'Local') return []
  if (method === 'Remote') return []
  if (method === 'Location') {
    const tool = args[0] || 'node'
    const root = 'C:\\Users\\demo\\AppData\\Local\\EnvSwitch'
    return {
      home: root,
      storeDir: root + '\\store',
      storeDefault: root + '\\store',
      storeCustom: false,
      toolStoreDir: root + '\\store\\' + tool,
      versionHint: root + '\\store\\' + tool + '\\<版本号>',
      binSub: '',
      currentLink: root + '\\current\\' + tool,
      currentTarget: root + '\\store\\' + tool + '\\22.22.0',
      currentOk: true,
      pathEntries: [{
        path: root + '\\current\\' + tool,
        role: '激活链接',
        present: true,
        exists: true,
        note: '首次切换会自动写入用户 PATH'
      }],
      envVars: tool === 'java'
        ? [{
            key: 'JAVA_HOME',
            value: 'C:\\Program Files\\Java\\jdk-17',
            set: true,
            note: 'JDK 根目录（Maven/Gradle 依赖） · 外部安装目录',
            managed: false,
            manageKey: 'JAVA_HOME',
            manageTool: 'java',
            target: root + '\\store\\java\\17.0.9'
          }]
        : [],
      shimsDir: root + '\\bin\\shims',
      shims: [],
      exeDir: 'C:\\Program Files\\EnvSwitch'
    }
  }
  if (method === 'Doctor') return []
  if (method === 'RefreshEnv') return demo('Overview', args)
  if (method === 'Settings') return { autostart: false, closeToTray: true, autoCheckUpdate: true, checkUpdateOnStart: true, storePath: '(dev)', mirrors: {}, proxy: '', indexCacheMinutes: 10, version: 'dev', remoteState: {}, totalSize: '—', installedCount: 0, managedEnv: [] }
  if (method === 'PickDirectory') return ''
  if (method === 'ChangeStore') return null
  if (method === 'SetEnvManaged') return null
  return null
}

export const api = {
  overview: () => call('Overview'),
  refreshEnv: () => call('RefreshEnv'),
  local: (t) => call('Local', t),
  remote: (t) => call('Remote', t),
  location: (t) => call('Location', t),
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
  openDir: (p) => call('OpenDir', p),
  changeStore: (p) => call('ChangeStore', p),
  pickDirectory: (title, dir) => call('PickDirectory', title, dir),
  setEnvManaged: (k, on) => call('SetEnvManaged', k, !!on),
  pendingUpdate: () => call('PendingUpdate'),
  checkUpdateNow: () => call('CheckUpdateNow'),
  applyUpdate: () => call('ApplyUpdate'),
  netproxyList: () => call('NetProxyList'),
  netproxySave: (draft) => call('NetProxySave', draft),
  netproxyRemove: (id) => call('NetProxyRemove', id),
  netproxyToggle: (id, enabled) => call('NetProxyToggle', id, !!enabled),
  netproxySaveSettings: (req) => call('NetProxySaveSettings', req),
  netproxyTest: (draft) => call('NetProxyTest', draft),
  netproxyClashSync: () => call('NetProxyClashSync')
}

// wails 事件桥：浏览器 dev 模式没有 window.runtime，改用本模块的事件表，
// 让 demo 的模拟推送（自更新进度等）能驱动界面，不打包也能调 UI。
const devChannels = new Map()
function devEmit (name, payload) {
  const set = devChannels.get(name)
  if (set) set.forEach(cb => cb(payload))
}
export function on (name, cb) {
  if (window.runtime && window.runtime.EventsOn) return window.runtime.EventsOn(name, cb)
  if (!devChannels.has(name)) devChannels.set(name, new Set())
  devChannels.get(name).add(cb)
  return () => {
    const set = devChannels.get(name)
    if (set) set.delete(cb)
  }
}

// 窗口操作：frameless 模式下由 TitleBar 调用；浏览器 dev 模式静默降级
const rt = () => window.runtime
const goApi = () => (window.go && window.go.app && window.go.app.App) || null
export const win = {
  minimise () { rt()?.WindowMinimise() },
  toggleMaximise () { rt()?.WindowToggleMaximise() },
  // wails 前端 runtime 没有 WindowClose（调了会静默无效），
  // 关闭走后端 App.CloseWindow：closeToTray 开→隐入托盘，关→真退
  close () {
    const b = goApi()
    if (b?.CloseWindow) { b.CloseWindow(); return }
    rt()?.WindowHide() // 浏览器 dev 模式无后端：退化为隐藏
  },
  isMaximised () {
    const r = rt()
    if (!r?.WindowIsMaximised) return Promise.resolve(false)
    return r.WindowIsMaximised()
  },
  isWails () { return !!window.runtime }
}
export function openExternal (url) {
  if (window.runtime?.BrowserOpenURL) return window.runtime.BrowserOpenURL(url)
  window.open(url, '_blank')
}
