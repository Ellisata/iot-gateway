// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 点位质量码的展示辅助。
 *
 * 质量码是一个整数（192=正常），坏点位的低 6 位给出成因。实时监控页与点位配置页的
 * 实时值列都要把坏点位的成因翻成人话，放在这里避免两处各写一份映射后悄悄漂移。
 */

// 坏子码 → i18n key。取值只有后端 driver.QualityBadXxx 这三个（低 6 位的子码），
// 故用显式映射而不是拼 key —— 拼错了 vue-i18n 会把 key 原样显示出来，
// 而"realtime.qualityBad.undefined"这种东西出现在运维页面上比不显示更糟。
const QUALITY_BAD_KEYS = {
  1: 'realtime.qualityBadAddress',
  2: 'realtime.qualityBadComms',
  3: 'realtime.qualityBadParse',
}

// qualityBadText 返回坏点位的文案；未归因（0）时退回原始质量码 ——
// 不把"未知"伪装成某个具体原因。
export function qualityBadText(t, quality) {
  const key = QUALITY_BAD_KEYS[quality]
  return key ? t(key) : String(quality)
}
