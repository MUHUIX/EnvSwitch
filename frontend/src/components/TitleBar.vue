<script setup>
import { ref, onMounted } from 'vue'
import { win, openExternal } from '../bridge'
import Icon from './Icon.vue'

const REPO = 'https://github.com/HUIdada1/EnvSwitch'

const props = defineProps({
  ver: { type: String, default: 'v1.0.0' },
  isDark: { type: Boolean, default: true }
})

const emit = defineEmits(['toggle-theme', 'open-settings'])

const isMaximized = ref(false)

function onToggleMaximize() {
  win.toggleMaximise()
  isMaximized.value = !isMaximized.value
}
</script>

<template>
  <header class="titlebar" style="--wails-draggable: drag">
    <div class="tb-brand" style="--wails-draggable: drag">
      <div class="logo-wrap">
        <img class="tb-logo" src="/logo.png" alt="EnvSwitch" />
      </div>
      <span class="tb-name">EnvSwitch</span>
      <span class="tb-author-pill">
        <span class="author-dot"></span>
        作者：沐晖
      </span>
    </div>

    <div class="tb-actions" style="--wails-draggable: no-drag">
      <!-- 版本号胶囊 -->
      <span class="tb-pill ver-num">{{ ver || 'v1.0.0' }}</span>

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

      <!-- 快捷设置 -->
      <button class="action-btn" title="首选项与设置" @click="emit('open-settings')">
        <Icon name="gear" :size="15" />
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
  width: 24px;
  height: 24px;
  border-radius: 6px;
  padding: 1px;
  background: linear-gradient(135deg, rgba(255, 255, 255, 0.2), rgba(255, 255, 255, 0.03));
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.2);
  display: flex;
  align-items: center;
  justify-content: center;
}

.tb-logo {
  width: 100%;
  height: 100%;
  border-radius: 5px;
  object-fit: cover;
  display: block;
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
  gap: 5px;
  padding: 2px 8px;
  font-size: 11px;
  font-weight: 500;
  color: var(--ink-2);
  background: var(--surface-soft);
  border: 1px solid var(--hairline);
  border-radius: 999px;
  transition: background-color 0.2s var(--ease), color 0.2s var(--ease);
}

.author-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 6px var(--accent);
}

.tb-actions {
  display: flex;
  align-items: center;
  gap: 4px;
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
  margin-right: 4px;
}

.action-btn {
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  border-radius: 6px;
  color: var(--ink-2);
  background: transparent;
  border: 1px solid transparent;
  transition: all 0.15s var(--ease);
}

.action-btn:hover {
  background: var(--surface-raised);
  border-color: var(--hairline);
  color: var(--ink-1);
}

.action-btn:active {
  transform: scale(0.94);
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
</style>
