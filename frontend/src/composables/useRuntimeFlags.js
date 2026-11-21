// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { ref } from 'vue'

import { listSystemConfig } from '@/api/modules/systemConfig'

// 与后端 runtimecfg 注册表一致；改名会让这里静默回落到默认值。
const THING_MODEL_MENU_KEY = 'ui.thingModelMenu.enabled'

/**
 * 影响界面展示的运行期开关。
 *
 * 状态放在模块级：侧栏与「运行开关」页各持同一份 ref，页面保存后侧栏
 * 立刻跟随变化，不需要刷新整页。接口失败时保持当前值（首次为默认展示），
 * 菜单不会因为一次请求失败而消失。
 */
const thingModelMenuEnabled = ref(true)

function findField(groups, key) {
  for (const group of groups || []) {
    const field = (group.fields || []).find((item) => item.key === key)
    if (field) return field
  }
  return null
}

/** 用 /systemConfig/list 的返回结果更新界面开关。 */
export function applyRuntimeFlags(groups) {
  const field = findField(groups, THING_MODEL_MENU_KEY)
  if (field) {
    thingModelMenuEnabled.value = !!field.value
  }
}

/** 主动拉取一次运行期开关，供布局挂载时调用。 */
export async function refreshRuntimeFlags() {
  try {
    const res = await listSystemConfig()
    applyRuntimeFlags(res?.groups)
  } catch (err) {
    // 拦截器已经弹过错误提示；菜单按默认值展示即可
    console.error('获取运行开关失败', err)
  }
}

export function useRuntimeFlags() {
  return { thingModelMenuEnabled }
}
