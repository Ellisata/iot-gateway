// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { ref, watch, onUnmounted } from 'vue'
import { getRealtimeSnapshot } from '@/api/modules/realtime'

// 点位配置页以「看/改配置」为主，实时值只是辅助信息：5 秒一次足够，
// 比实时监控页的 1~2 秒更省网关，也不至于让人盯着数字等。
const DEFAULT_INTERVAL = 5000
// 点位列表页长最大 100，后端 size 上限也是 100
const MAX_IDS = 100

/**
 * 按「当前页的点位 ID」轮询实时值。
 *
 * addressIds 传一个 computed（随列表/翻页变化）。只有 ID 集合真的变了才重启轮询，
 * 列表因保存/删除而重新拉取时不会额外多打一次请求。
 *
 * 与实时监控页同一套「不该轮询」的判据：用户暂停、标签页切到后台、页面卸载。
 * 取不到值时静默保留上一轮结果 —— 配置页的主任务是维护点位，弹错误只会挡路。
 *
 * @param {string} deviceObjectId 设备对象 ID
 * @param {import('vue').Ref<string[]>} addressIds 当前页的点位 ID
 * @param {{interval?: number}} [options]
 * @returns {{values: import('vue').Ref<Record<string, object>>,
 *   paused: import('vue').Ref<boolean>, togglePause: () => void,
 *   refresh: () => Promise<void>}}
 */
export function useRealtimeValues(deviceObjectId, addressIds, options = {}) {
  const interval = options.interval ?? DEFAULT_INTERVAL
  // addressId -> RealtimePointVO（含 value/quality/collected/updatedAt/stale）
  const values = ref({})
  const paused = ref(false)

  let timer = null
  let inFlight = false
  // 上一轮参与轮询的 ID 集合：用来忽略「列表刷新了但当前页还是这些点位」
  let lastKey = ''
  // 请求序号：翻页/暂停时令在飞请求作废，避免旧页的结果盖到新页上
  let requestSeq = 0
  let pendingRefresh = false

  function stopTimer() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  async function refresh() {
    const ids = (addressIds.value || []).slice(0, MAX_IDS)
    if (!ids.length) {
      values.value = {}
      return
    }
    if (inFlight) {
      // 这一轮请求跑完后马上按最新 ID 再取一次，别等下一个 5 秒
      pendingRefresh = true
      return
    }
    inFlight = true
    const seq = ++requestSeq
    try {
      const res = await getRealtimeSnapshot({
        deviceObjectId,
        // page/size 在 addressIds 模式下会被后端忽略，这里填合法值只为过参数校验
        page: 1,
        size: ids.length,
        addressIds: ids,
      })
      // 期间翻页/暂停过：这份结果对应的是旧 ID，丢弃
      if (seq !== requestSeq) return
      const next = {}
      for (const record of res?.records ?? []) {
        next[record.addressId] = record
      }
      values.value = next
    } catch {
      // 静默失败：保留上一轮值，避免整列在设备抖动时反复变空
    } finally {
      inFlight = false
      if (pendingRefresh) {
        pendingRefresh = false
        refresh()
      }
    }
  }

  function restart() {
    stopTimer()
    // 令在飞请求作废：它回来时不能把旧页的值写进新页
    requestSeq += 1
    if (paused.value || document.hidden) return
    if (!addressIds.value?.length) {
      values.value = {}
      return
    }
    refresh()
    timer = setInterval(refresh, interval)
  }

  // 翻页/搜索/增删点位后 ID 集合变化立即刷新；集合没变就不打断现有节奏
  watch(
    addressIds,
    (ids) => {
      const key = (ids || []).join(',')
      if (key === lastKey) return
      lastKey = key
      restart()
    },
    { immediate: true },
  )

  function togglePause() {
    paused.value = !paused.value
    restart()
  }

  // 标签页不可见时停轮询：用户看不见这一列，没必要继续压网关；
  // 切回来立刻补一次，避免看到切走前的旧值。
  function handleVisibilityChange() {
    if (document.hidden) stopTimer()
    else restart()
  }
  document.addEventListener('visibilitychange', handleVisibilityChange)

  onUnmounted(() => {
    stopTimer()
    document.removeEventListener('visibilitychange', handleVisibilityChange)
  })

  return { values, paused, togglePause, refresh }
}
