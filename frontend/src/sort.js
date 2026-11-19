// 环境列表统一排序：已安装（本机有可用版本）的排前面，
// 已安装组、未安装组内部各自按名称 26 字母表顺序（忽略大小写）排列。
export function sortTools(list) {
  return (list || []).slice().sort((a, b) => {
    const ai = a.installed && a.installed.length ? 0 : 1
    const bi = b.installed && b.installed.length ? 0 : 1
    if (ai !== bi) return ai - bi
    return String(a.id).localeCompare(String(b.id), 'en', { sensitivity: 'base' })
  })
}
