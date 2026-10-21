<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  侧边菜单图标集。

  统一采用 24×24 网格、1.6 描边、圆头圆角，与登录页品牌图形保持同一套线条语言，
  避免混用 Element Plus 图标时出现的粗细、圆角和语义不一致问题。
-->
<template>
  <svg
    width="1em"
    height="1em"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    focusable="false"
    aria-hidden="true"
  >
    <!-- 首页：仪表盘栅格 -->
    <template v-if="name === 'dashboard'">
      <rect x="3" y="3" width="7.5" height="7.5" rx="1.8" />
      <rect x="13.5" y="3" width="7.5" height="7.5" rx="1.8" />
      <rect x="3" y="13.5" width="7.5" height="7.5" rx="1.8" />
      <rect x="13.5" y="13.5" width="7.5" height="7.5" rx="1.8" />
    </template>

    <!-- 工业物联网：设备互联拓扑 -->
    <template v-else-if="name === 'industrial'">
      <circle cx="12" cy="5" r="2.2" />
      <circle cx="5" cy="18" r="2.2" />
      <circle cx="19" cy="18" r="2.2" />
      <path d="M10.8 7 6.2 15.9" />
      <path d="m13.2 7 4.6 8.9" />
      <path d="M7.2 18h9.6" />
    </template>

    <!-- 设备对象：芯片 -->
    <template v-else-if="name === 'deviceObject'">
      <rect x="7" y="7" width="10" height="10" rx="2" />
      <rect x="10" y="10" width="4" height="4" rx="1" />
      <path d="M9.5 3v4M14.5 3v4M9.5 17v4M14.5 17v4" />
      <path d="M3 9.5h4M3 14.5h4M17 9.5h4M17 14.5h4" />
    </template>

    <!-- 局域网扫描：雷达（同心弧在左上留出扫描缺口） -->
    <template v-else-if="name === 'deviceScan'">
      <circle cx="12" cy="12" r="1.5" />
      <path d="M12 12 5.5 5.5" />
      <path d="M12 3.5a8.5 8.5 0 1 1-6-6" />
      <path d="M12 7.5a4.5 4.5 0 1 1-3.18-3.18" />
    </template>

    <!-- 协议管理：带代码符号的文档 -->
    <template v-else-if="name === 'protocol'">
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Z" />
      <path d="M14 3v5h5" />
      <path d="m11 11.5-2.2 2.2L11 16" />
      <path d="m13 11.5 2.2 2.2L13 16" />
    </template>

    <!-- 协议表单配置：滑杆 -->
    <template v-else-if="name === 'protocolForm'">
      <path d="M4 7h8.5M17.5 7H20M4 17h4.5M13.5 17H20" />
      <circle cx="15" cy="7" r="2.5" />
      <circle cx="11" cy="17" r="2.5" />
    </template>

    <!-- 数据推送：纸飞机 -->
    <template v-else-if="name === 'dataPush'">
      <path d="M21 3 10.5 13.5" />
      <path d="M21 3 14 21l-3.5-7.5L3 10l18-7Z" />
    </template>

    <!-- 通道管理：一路分多路 -->
    <template v-else-if="name === 'channel'">
      <circle cx="5" cy="12" r="2" />
      <circle cx="19" cy="6" r="2" />
      <circle cx="19" cy="18" r="2" />
      <path d="M7 12h1.5a4 4 0 0 0 4-4V6H17" />
      <path d="M7 12h1.5a4 4 0 0 1 4 4v2H17" />
    </template>

    <!-- 通道表单管理：文档 + 编辑 -->
    <template v-else-if="name === 'channelForm'">
      <path d="M12 4H6.5A2.5 2.5 0 0 0 4 6.5v11A2.5 2.5 0 0 0 6.5 20h11a2.5 2.5 0 0 0 2.5-2.5V12" />
      <path d="M17.4 3.6a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4 8.4-8.4Z" />
    </template>

    <!-- 订阅管理：RSS -->
    <template v-else-if="name === 'subscription'">
      <path d="M5 5a14 14 0 0 1 14 14" />
      <path d="M5 11a8 8 0 0 1 8 8" />
      <circle cx="6" cy="18" r="1.6" />
    </template>

    <!-- MQTT 订阅：消息气泡 -->
    <template v-else-if="name === 'mqttSubscription'">
      <path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z" />
      <path d="M8.6 11.5h.01M12 11.5h.01M15.4 11.5h.01" />
    </template>

    <!-- 订阅设备与点位：瞄准点 -->
    <template v-else-if="name === 'subscriptionPoint'">
      <circle cx="12" cy="12" r="7" />
      <circle cx="12" cy="12" r="2.5" />
      <path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3" />
    </template>

    <!-- 报警管理：铃铛 -->
    <template v-else-if="name === 'alarm'">
      <path d="M18 8.5a6 6 0 0 0-12 0c0 5-2.2 6.5-2.2 6.5h16.4S18 13.5 18 8.5Z" />
      <path d="M13.7 19a2 2 0 0 1-3.4 0" />
    </template>

    <!-- 设备报警：显示器 + 告警 -->
    <template v-else-if="name === 'alarmDevice'">
      <rect x="3" y="4" width="18" height="12" rx="2.2" />
      <path d="M12 7.5v3.2M12 13.4h.01" />
      <path d="M9 20h6M12 16v4" />
    </template>

    <!-- 通道报警：断开的插头 -->
    <template v-else-if="name === 'alarmChannel'">
      <path d="M9.5 7.5V3M14.5 7.5V3" />
      <path d="M7 7.5h10v2.5a5 5 0 0 1-10 0V7.5Z" />
      <path d="M12 15v3.5" />
      <path d="M9.5 21h5" />
    </template>

    <!-- 点位报警：异常脉冲 -->
    <template v-else-if="name === 'alarmPoint'">
      <path d="M3 12h3.5l2.5-6 4 12 2.5-6H21" />
    </template>

    <!-- 点位超限报警：阈值仪表 -->
    <template v-else-if="name === 'alarmThreshold'">
      <path d="M4 17.5a8 8 0 0 1 16 0" />
      <path d="m12 13.5 3.8-4.3" />
      <circle cx="12" cy="13.5" r="1.3" />
    </template>

    <!-- 越限记录：历史 -->
    <template v-else-if="name === 'alarmRecord'">
      <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
      <path d="M3 3v5h5" />
      <path d="M12 7v5l4 2" />
    </template>

    <!-- 可靠性统计：柱状图 -->
    <template v-else-if="name === 'alarmReliability'">
      <path d="M3.5 20h17" />
      <path d="M7.5 20v-6.5M12 20V7M16.5 20v-9" />
    </template>

    <!-- 通知配置：扩音器 -->
    <template v-else-if="name === 'notification'">
      <path d="M4 9.5h3.2l9-5v15l-9-5H4a1.6 1.6 0 0 1-1.6-1.6v-1.8A1.6 1.6 0 0 1 4 9.5Z" />
      <path d="M7.5 14.5v4" />
      <path d="M19 9.2a4.2 4.2 0 0 1 0 5.6" />
    </template>

    <!-- 日志管理：日志本 -->
    <template v-else-if="name === 'logs'">
      <rect x="4.5" y="3" width="15" height="18" rx="2.5" />
      <path d="M9 3v18" />
      <path d="M12.5 8h4M12.5 12h4M12.5 16h2.5" />
    </template>

    <!-- 日志文件查看：文件 + 行 -->
    <template v-else-if="name === 'logFile'">
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Z" />
      <path d="M14 3v5h5" />
      <path d="M9 13h6M9 17h4" />
    </template>

    <!-- 密钥管理：钥匙 -->
    <template v-else-if="name === 'secret'">
      <circle cx="8" cy="15.5" r="4.2" />
      <path d="m11 12.5 9-9" />
      <path d="m16.5 7 3 3M14 9.5l2.5 2.5" />
    </template>

    <!-- API Key：标签 -->
    <template v-else-if="name === 'apiKey'">
      <path d="M3.5 12.3V5.5a2 2 0 0 1 2-2h6.8a2 2 0 0 1 1.4.6l6.2 6.2a2 2 0 0 1 0 2.8l-6.8 6.8a2 2 0 0 1-2.8 0l-6.2-6.2a2 2 0 0 1-.6-1.4Z" />
      <circle cx="8.3" cy="8.3" r="1.3" />
    </template>

    <!-- 网关设置：带指示灯的网关机箱 -->
    <template v-else-if="name === 'gateway'">
      <rect x="3" y="6" width="18" height="12" rx="2.2" />
      <path d="M7 10.5h.01M7 13.5h.01" />
      <path d="M11 10.5h6M11 13.5h4" />
    </template>

    <!-- 运行开关：拨动开关 -->
    <template v-else-if="name === 'runtimeSwitch'">
      <rect x="2.5" y="7" width="19" height="10" rx="5" />
      <circle cx="16.5" cy="12" r="2.6" />
    </template>

    <!-- 系统设置：齿轮 -->
    <template v-else-if="name === 'settings'">
      <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2Z" />
      <circle cx="12" cy="12" r="3" />
    </template>

    <!-- 开放接口文档：打开的书 -->
    <template v-else-if="name === 'apiDoc'">
      <path d="M12 6.8C10.3 5.5 8 5 5.3 5H3.5v13.5h1.8c2.7 0 5 .5 6.7 1.8 1.7-1.3 4-1.8 6.7-1.8h1.8V5h-1.8c-2.7 0-5 .5-6.7 1.8Z" />
      <path d="M12 6.8v13.5" />
    </template>
  </svg>
</template>

<script setup>
defineProps({
  name: {
    type: String,
    required: true,
  },
})
</script>
