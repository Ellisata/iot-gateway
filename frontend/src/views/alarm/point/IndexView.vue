<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <AlarmTable target-type="point" />
</template>

<script setup>
// 点位质量报警页。表格实现只有一份（../AlarmTable.vue）—— 四个报警页是同一个页面，
// 只有 target_type 不同，本文件存在的意义只是让目录结构与菜单一一对应。
//
// 别和隔壁 threshold/ 混：那边是 target_type='point_threshold' 的超限报警，
// 后端刻意把它与 point 分开，理由是两者报警行的落库键会互相覆盖（见 tracker.go
// 对 TypePointThreshold 的说明）。
import AlarmTable from '../AlarmTable.vue'
</script>
