<script setup>
import { computed } from 'vue'
import { BRAND_LOGOS, UI_ICONS } from '../icons'

const props = defineProps({
  name: { type: String, required: true },
  size: { type: [Number, String], default: 18 }
})

const isBrand = computed(() => !!BRAND_LOGOS[props.name])
const svgHtml = computed(() => {
  if (BRAND_LOGOS[props.name]) {
    return BRAND_LOGOS[props.name]
  }
  return UI_ICONS[props.name] || UI_ICONS.dash
})
</script>

<template>
  <svg
    class="env-icon"
    :class="{ 'is-brand': isBrand, 'is-ui': !isBrand }"
    :style="{ width: `${size}px`, height: `${size}px` }"
    viewBox="0 0 24 24"
    v-html="svgHtml"
  ></svg>
</template>

<style scoped>
.env-icon {
  display: inline-block;
  vertical-align: middle;
  flex: none;
  transition: transform 0.16s cubic-bezier(0.2, 0, 0, 1), opacity 0.16s ease;
}

.env-icon.is-ui {
  stroke: currentColor;
  stroke-width: 1.5;
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.env-icon.is-brand {
  border-radius: 4px;
}
</style>
