// 手写 wails 绑定代理：与 wails CLI 生成文件同构（window.go.app.App.*），
// dev 模式下浏览器直开时全部返回降级数据，保证 UI 可独立调试。
const g = () => (window.go && window.go.app && window.go.app.App) || null

function call (method, ...args) {
  const b = g()
  if (!b) return Promise.resolve(demo(method, args))
  return b[method](...args)
}

function demo (method, args) {
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
  applyUpdate: () => call('ApplyUpdate')
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
