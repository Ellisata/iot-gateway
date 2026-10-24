// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { ref } from 'vue'
import { getPushChannelTypeList } from '@/api/modules/pushChannel'

// 模块级缓存：通道选择器与通道页共用，避免同一页面重复请求。
const types = ref([])
const loading = ref(false)
let inFlight = null

export function usePushChannelTypes() {
  async function load(force = false) {
    if (!force && types.value.length) return types.value
    if (inFlight) return inFlight

    loading.value = true
    inFlight = getPushChannelTypeList()
      .then((res) => {
        const list = Array.isArray(res) ? res : (res?.records ?? [])
        types.value = list
        return list
      })
      .finally(() => {
        loading.value = false
        inFlight = null
      })
    return inFlight
  }

  function metaOf(type) {
    return types.value.find((item) => item.type === type) || null
  }

  function labelOf(type) {
    return metaOf(type)?.label || type
  }

  return { types, loading, load, metaOf, labelOf }
}
