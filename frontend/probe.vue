<!-- 临时探针：只为量「点位发现」弹窗里那张表的真实行高与可见行数。用完即删。 -->
<template>
  <el-dialog :model-value="true" width="1080px" title="probe" :close-on-click-modal="false">
    <el-table
      ref="tableRef"
      :data="rows"
      stripe
      border
      size="small"
      max-height="380"
      row-key="name"
    >
      <el-table-column type="selection" width="46" reserve-selection />
      <el-table-column label="地址" width="140">
        <template #default="{ row }">
          <span class="font-mono text-xs">{{ row.name }}</span>
        </template>
      </el-table-column>
      <el-table-column label="当前值" width="130">
        <template #default="{ row }">
          <span class="font-mono text-xs">{{ row.value }}</span>
        </template>
      </el-table-column>
      <el-table-column label="标签" min-width="160">
        <template #default="{ row }">
          <el-input v-model="row.label" size="small" maxlength="100" />
        </template>
      </el-table-column>
      <el-table-column label="通用数据类型" width="150">
        <template #default="{ row }">
          <el-select v-model="row.commonDataType" size="small" :persistent="false">
            <el-option v-for="o in typeOptions" :key="o" :label="o" :value="o" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="读写权限" width="120">
        <template #default="{ row }">
          <el-select v-model="row.rwPermission" size="small" :persistent="false">
            <el-option label="R（只读）" value="R" />
            <el-option label="RW（读写）" value="RW" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="扫描频率" width="150">
        <template #default="{ row }">
          <el-select v-model="row.scanFrequency" size="small" :persistent="false">
            <el-option v-for="n in 9" :key="n" :label="`${n * 100} ms`" :value="n * 100" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="描述" min-width="150">
        <template #default="{ row }">
          <el-input v-model="row.description" size="small" maxlength="200" />
        </template>
      </el-table-column>
    </el-table>
  </el-dialog>

  <pre id="out" style="position: fixed; left: 0; top: 0; z-index: 99999; background: #fff">{{ out }}</pre>
</template>

<script setup>
import { onMounted, ref } from 'vue'

const typeOptions = ['int16', 'uint16', 'int32', 'uint32', 'float', 'bool', 'string']

const rows = ref(
  Array.from({ length: 20 }, (_, i) => ({
    name: `4000${i + 1}`,
    value: String(i),
    label: `点位 ${i + 1}`,
    commonDataType: 'int16',
    rwPermission: 'R',
    scanFrequency: 1000,
    description: '',
  })),
)

const out = ref('measuring…')

const nextFrame = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))

onMounted(async () => {
  // el-table 在 mounted 后用 rAF 跑 doLayout，多等几帧再量
  for (let i = 0; i < 6; i++) await nextFrame()

  const rowsEl = document.querySelectorAll('.el-table__body-wrapper tbody tr')
  const bodyWrapper = document.querySelector('.el-table__body-wrapper')
  const scrollWrap = document.querySelector('.el-table__body-wrapper .el-scrollbar__wrap')
  const header = document.querySelector('.el-table__header-wrapper')

  const trs = Array.from(rowsEl)
  const heights = trs.map((tr) => +tr.getBoundingClientRect().height.toFixed(2))

  // 以滚动容器为「可见窗口」，数有多少行完整落在里面
  const view = scrollWrap || bodyWrapper
  const viewRect = view.getBoundingClientRect()
  const visibleFull = trs.filter((tr) => {
    const r = tr.getBoundingClientRect()
    return r.top >= viewRect.top - 0.5 && r.bottom <= viewRect.bottom + 0.5
  }).length

  out.value = JSON.stringify(
    {
      rowCount: trs.length,
      rowHeights: [...new Set(heights)],
      headerHeight: header ? +header.getBoundingClientRect().height.toFixed(2) : null,
      bodyWrapperHeight: bodyWrapper ? +bodyWrapper.getBoundingClientRect().height.toFixed(2) : null,
      bodyWrapperClientHeight: bodyWrapper ? bodyWrapper.clientHeight : null,
      scrollWrapHeight: scrollWrap ? +scrollWrap.getBoundingClientRect().height.toFixed(2) : null,
      scrollWrapClientHeight: scrollWrap ? scrollWrap.clientHeight : null,
      scrollWrapScrollHeight: scrollWrap ? scrollWrap.scrollHeight : null,
      horizontalScrollbar: scrollWrap ? scrollWrap.scrollWidth > scrollWrap.clientWidth : null,
      clientWidth: scrollWrap ? scrollWrap.clientWidth : null,
      scrollWidth: scrollWrap ? scrollWrap.scrollWidth : null,
      tableRectHeight: +document.querySelector('.el-table').getBoundingClientRect().height.toFixed(2),
      visibleFullRows: visibleFull,
    },
    null,
    1,
  )
})
</script>
