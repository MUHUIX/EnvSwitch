// 环境列表统一排序（总览卡片 + 左侧栏条目共用同一口径）：
// 「已安装使用」的排前面 —— 判据取两者之一：
//   ① 本机有可用版本（installed 非空：EnvSwitch 版本库 / nvm 收编）；
//   ② 系统里检测到并在用（current 非空：如 Git、Docker 这类外部安装，
//      版本库里没有它，但界面明确显示生效版本，属于「已安装使用」）。
// 旧实现只认 ①，导致外部安装的工具明明在用却被排到未安装区。
// 已安装组、未安装组内部各自按显示名 26 字母表顺序（忽略大小写）排列。
export function sortTools(list) {
  const rank = (t) => ((t.installed && t.installed.length) || t.current ? 0 : 1)
  // 界面显示的是 id（node / python / openjdk…），排序键与显示保持一致；
  // 没有 id 的兜底用 title，避免出现空串排最前。
  const name = (t) => String(t.id || t.title || '')
  return (list || []).slice().sort((a, b) => {
    const d = rank(a) - rank(b)
    if (d) return d
    return name(a).localeCompare(name(b), 'en', { sensitivity: 'base' })
  })
}
