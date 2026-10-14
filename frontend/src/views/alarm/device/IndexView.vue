<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <AlarmTable target-type="device" />
</template>

<script setup>
// 设备报警页。表格实现只有一份（../AlarmTable.vue）—— 四个报警页是同一个页面，
// 只有 target_type 不同，本文件存在的意义只是让目录结构与菜单一一对应。
import AlarmTable from '../AlarmTable.vue'
</script>
