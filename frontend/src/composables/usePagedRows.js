// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { computed, ref, watch } from 'vue'

/**
 * 前端分页：把一份可能很长的清单切成当前页交给 el-table。
 *
 * **为什么必须有这个**：扫描结果这类清单最多能到几千条，而 el-table 默认把传入
 * `data` 的每一行都挂成真实组件（点位扫描那张表一行就是 3 个 el-select + 2 个
 * el-input，再加每个下拉自带的 option/popper 子树）。整表一次性挂载是几万个组件
 * 实例，主线程会被 patch 占满几十秒，浏览器表现为"页面假死"。只渲染当前页，
 * 组件数就降到千级。虚拟滚动是另一条路，但 el-table 要换成 el-table-v2，
 * 会牵动现有的 selection / row-key 用法，不值当。
 *
 * 三处用它（设备扫描结果、探测失败明细、点扫结果），先前只有点扫那张表自己写了
 * 一份内联实现 —— 收在这里是为了别再长出第三份。
 *
 * 用法：
 *   const { currentPage, pageSize, pagedRows, totalRows } = usePagedRows(rows)
 *   <el-table :data="pagedRows">
 *   <el-pagination v-model:current-page="currentPage" v-model:page-size="pageSize"
 *     :total="totalRows" :page-sizes="[20, 50, 100]" layout="total, sizes, prev, pager, next, jumper" />
 *
 * @param {import('vue').ComputedRef<Array>} rows 全量清单（传 computed 或 ref）
 * @param {{pageSize?: number}} [options] 默认每页条数
 * @returns {{currentPage: import('vue').Ref<number>, pageSize: import('vue').Ref<number>,
 *   pagedRows: import('vue').ComputedRef<Array>, totalRows: import('vue').ComputedRef<number>}}
 */
export function usePagedRows(rows, { pageSize: initialPageSize = 20 } = {}) {
  const currentPage = ref(1)
  const pageSize = ref(initialPageSize)

  const totalRows = computed(() => rows.value.length)

  const pagedRows = computed(() => {
    const start = (currentPage.value - 1) * pageSize.value
    return rows.value.slice(start, start + pageSize.value)
  })

  const lastPage = computed(() => Math.max(1, Math.ceil(totalRows.value / pageSize.value)))

  // 行数变少后当前页可能已经越界（切换「显示 0 值」、建成设备后摘掉已建的行），
  // 症状是表格空着而分页器还停在原页码。把页码收回最后一页。
  //
  // 每次调用方换数据都会重算，收拢是幂等的，因此不需要调用方自己记得复位。
  watch(lastPage, (last) => {
    if (currentPage.value > last) currentPage.value = last
  })

  return { currentPage, pageSize, pagedRows, totalRows }
}
