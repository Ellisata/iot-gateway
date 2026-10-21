<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  侧边菜单图标集。

  统一采用 Tabler Icons（24×24 网格、圆头圆角描边），与登录页品牌图形保持同一套线条语言，
  避免混用 Element Plus 图标时出现的粗细、圆角和语义不一致问题。

  尺寸取 1em 跟随 el-icon 的 font-size（侧栏里为 18px），描边固定 1.6，
  让不同语义的线条图形在深色侧栏里保持同一视觉重量。
  对外只暴露 name 字符串，后续更换图标库或增删图标不影响 SideMenu.vue。
-->
<template>
  <component
    :is="icon"
    size="1em"
    stroke="1.6"
    aria-hidden="true"
    focusable="false"
  />
</template>

<script>
import {
  IconActivityHeartbeat,
  IconAdjustmentsHorizontal,
  IconBell,
  IconBook,
  IconChartBar,
  IconCpu,
  IconCrosshair,
  IconDeviceDesktopExclamation,
  IconFileCode,
  IconFilePencil,
  IconFileText,
  IconGauge,
  IconHistory,
  IconKey,
  IconLayoutDashboard,
  IconMessageDots,
  IconNotebook,
  IconPlugConnectedX,
  IconPoint,
  IconRadar,
  IconRss,
  IconSend,
  IconServer,
  IconSettings,
  IconSitemap,
  IconSpeakerphone,
  IconTag,
  IconToggleRight,
  IconTopologyStar,
} from '@tabler/icons-vue'

// name 与菜单语义一一对应，新增菜单项时只在这里补一行
const ICONS = {
  dashboard: IconLayoutDashboard,
  industrial: IconTopologyStar,
  deviceObject: IconCpu,
  deviceScan: IconRadar,
  protocol: IconFileCode,
  protocolForm: IconAdjustmentsHorizontal,
  dataPush: IconSend,
  channel: IconSitemap,
  channelForm: IconFilePencil,
  subscription: IconRss,
  mqttSubscription: IconMessageDots,
  subscriptionPoint: IconCrosshair,
  alarm: IconBell,
  alarmDevice: IconDeviceDesktopExclamation,
  alarmChannel: IconPlugConnectedX,
  alarmPoint: IconActivityHeartbeat,
  alarmThreshold: IconGauge,
  alarmRecord: IconHistory,
  alarmReliability: IconChartBar,
  notification: IconSpeakerphone,
  logs: IconNotebook,
  logFile: IconFileText,
  secret: IconKey,
  apiKey: IconTag,
  gateway: IconServer,
  runtimeSwitch: IconToggleRight,
  settings: IconSettings,
  apiDoc: IconBook,
}
</script>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  name: {
    type: String,
    required: true,
    validator: (value) => value in ICONS,
  },
})

// 未知名称回退为圆点，避免整行图标消失且保留占位
const icon = computed(() => ICONS[props.name] ?? IconPoint)
</script>
