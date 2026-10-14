<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <AlarmTable target-type="point_threshold" />
</template>

<script setup>
// 点位超限报警页。表格实现只有一份（../AlarmTable.vue）—— 四个报警页是同一个页面，
// 只有 target_type 不同，本文件存在的意义只是让目录结构与菜单一一对应。
//
// target_type 是 point_threshold 而不是 point：后端刻意把超限报警与点位质量报警
// 分成两类（两者落库键相同，合并会互相覆盖，见 tracker.go 对 TypePointThreshold
// 的说明）。页面标题与图标也因此与点位页分开。
//
// 与隔壁 threshold-record/ 也不是一回事：那是原始越限逐条留证，不是报警事件。
import AlarmTable from '../AlarmTable.vue'
</script>
