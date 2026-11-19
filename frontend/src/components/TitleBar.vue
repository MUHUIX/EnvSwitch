<script setup>
import { ref, onMounted } from 'vue'
import { win, openExternal } from '../bridge'
import Icon from './Icon.vue'

const REPO = 'https://github.com/MUHUIX/EnvSwitch'

defineProps({
  ver: { type: String, default: 'v1.0.0' },
  isDark: { type: Boolean, default: true }
})

const emit = defineEmits(['toggle-theme'])

const isMaximized = ref(false)

async function syncMaximized() {
  try {
    isMaximized.value = await win.isMaximised()
  } catch (e) {
    // 浏览器 dev 模式无窗口 API，保持默认
  }
}

onMounted(syncMaximized)

async function onToggleMaximize() {
  win.toggleMaximise()
  // 窗口状态由系统异步变更，稍后回读，避免图标与真实状态不一致
  setTimeout(syncMaximized, 120)
}
</script>

<template>
  <header class="titlebar" style="--wails-draggable: drag">
    <div class="tb-brand" style="--wails-draggable: drag">
      <div class="logo-wrap" :class="{ 'is-light': !isDark }">
        <img
          class="tb-logo dark-logo"
          :class="{ active: isDark }"
          src="/logo-dark.png"
          alt="EnvSwitch"
        />
        <img
          class="tb-logo light-logo"
          :class="{ active: !isDark }"
          src="/logo-light.png"
          alt="EnvSwitch"
        />
      </div>
      <span class="tb-name">EnvSwitch</span>
    </div>

    <div class="tb-actions" style="--wails-draggable: no-drag">
      <!-- 版本号胶囊 -->
      <span class="tb-pill ver-num">{{ ver || 'v1.0.0' }}</span>

      <!-- 作者信息胶囊（置于版本号右侧） -->
      <span class="tb-author-pill">作者：沐晖</span>

      <!-- GitHub 仓库跳转 -->
      <button class="action-btn" title="GitHub 仓库 · 欢迎 Star" @click="openExternal(REPO)">
        <Icon name="github" :size="15" />
      </button>

      <!-- 明暗主题切换 -->
      <button
        class="action-btn"
        :title="isDark ? '切换至明亮模式' : '切换至暗黑模式'"
        @click="emit('toggle-theme')"
      >
        <Icon :name="isDark ? 'sun' : 'moon'" :size="15" />
      </button>

      <span class="tb-divider"></span>

      <!-- 窗口控制三键 -->
      <div class="win-controls">
        <button class="win-btn" title="最小化" @click="win.minimise()">
          <svg viewBox="0 0 10 10"><path d="M1 5h8" /></svg>
        </button>
        <button class="win-btn" :title="isMaximized ? '还原' : '最大化'" @click="onToggleMaximize">
          <svg v-if="isMaximized" viewBox="0 0 10 10">
            <rect x="1.2" y="3" width="5.8" height="5.8" rx="0.6" />
            <path d="M3 3V1.6h5.4v5.4H7" />
          </svg>
          <svg v-else viewBox="0 0 10 10">
            <rect x="1.5" y="1.5" width="7" height="7" rx="0.8" />
          </svg>
        </button>
        <button class="win-btn win-close" title="关闭（最小化到托盘）" @click="win.close()">
          <svg viewBox="0 0 10 10"><path d="M1.8 1.8l6.4 6.4M8.2 1.8l-6.4 6.4" /></svg>
        </button>
      </div>
    </div>
  </header>
</template>

<style scoped>
.titlebar {
  height: 42px;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 10px 0 14px;
  background: var(--titlebar-bg);
  backdrop-filter: blur(24px) saturate(1.4);
  -webkit-backdrop-filter: blur(24px) saturate(1.4);
  border-bottom: 1px solid var(--hairline);
  box-shadow: inset 0 1px 0 var(--glass-hi), 0 2px 10px rgba(0, 0, 0, 0.08);
  user-select: none;
  z-index: 100;
  transition: background-color 0.2s var(--ease), border-color 0.2s var(--ease);
}

.tb-brand {
  display: flex;
  align-items: center;
  gap: 10px;
}

.logo-wrap {
  position: relative;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  padding: 1px;
  background: linear-gradient(135deg, rgba(255, 255, 255, 0.22), rgba(255, 255, 255, 0.04));
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.25);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  transition: background 0.25s var(--ease), box-shadow 0.25s var(--ease);
}

.logo-wrap.is-light {
  background: linear-gradient(135deg, rgba(255, 255, 255, 0.95), rgba(225, 232, 240, 0.7));
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08), 0 0 0 1px rgba(0, 0, 0, 0.06);
}

.tb-logo {
  position: absolute;
  inset: 1px;
  width: calc(100% - 2px);
  height: calc(100% - 2px);
  border-radius: 5px;
  object-fit: cover;
  display: block;
  opacity: 0;
  transform: scale(0.92);
  transition: opacity 0.22s var(--ease), transform 0.22s var(--ease);
  pointer-events: none;
}

.tb-logo.active {
  opacity: 1;
  transform: scale(1);
}

.tb-name {
  font-size: 13.5px;
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--ink-1);
}

.tb-author-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  font-size: 11px;
  font-weight: 500;
  color: var(--ink-2);
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: 999px;
  transition: background-color 0.2s var(--ease), color 0.2s var(--ease);
}

.tb-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

.tb-pill {
  padding: 2px 7px;
  font-size: 11px;
  font-weight: 600;
  color: var(--accent-ink);
  background: var(--accent-dim);
  border: 1px solid var(--accent-line);
  border-radius: 6px;
  font-family: var(--font-num);
}

.action-btn {
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  border-radius: 6px;
  color: var(--ink-2);
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
  cursor: pointer;
  transition: all 0.15s var(--ease);
}

.action-btn:hover {
  background: var(--surface-raised);
  border-color: var(--hairline-strong);
  color: var(--ink-1);
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.08);
  transform: translateY(-1px);
}

.action-btn:active {
  transform: scale(0.94);
  box-shadow: none;
}

.tb-divider {
  width: 1px;
  height: 16px;
  background: var(--hairline);
  margin: 0 6px;
}

.win-controls {
  display: flex;
  align-items: center;
  gap: 2px;
}

.win-btn {
  width: 34px;
  height: 26px;
  display: grid;
  place-items: center;
  border-radius: 5px;
  background: transparent;
  border: none;
  transition: background-color 0.12s var(--ease);
}

.win-btn svg {
  width: 10px;
  height: 10px;
  stroke: var(--ink-2);
  stroke-width: 1.1;
  fill: none;
}

.win-btn:hover {
  background: var(--surface-raised);
}

.win-btn:hover svg {
  stroke: var(--ink-1);
}

.win-btn.win-close:hover {
  background: rgba(235, 87, 87, 0.18);
}

.win-btn.win-close:hover svg {
  stroke: #ff5c5c;
}

/* ===== 明亮主题局部修正 =====
   暗色那套「红底 + 亮红描边」搬到白面板上，关闭图标的对比只有 2.3:1（图形元素要求 3:1），
   所以在明亮主题换成浅红底 + 深红笔画；暗色表现保持原样。 */
[data-theme="light"] .win-btn.win-close:hover {
  background: rgba(194, 43, 43, 0.12);
}

[data-theme="light"] .win-btn.win-close:hover svg {
  stroke: #C22B2B;
}

/* 标题栏内高光：白底上用纯白 inset 高光是"无效果"，改成更淡的分隔感 */
[data-theme="light"] .titlebar {
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.9), 0 2px 10px rgba(11, 16, 23, 0.06);
}
</style>
