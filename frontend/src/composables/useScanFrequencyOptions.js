// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

/**
 * 「扫描频率」下拉选项。
 *
 * 设备地址页的新增/编辑地址表单与自动发现点位弹窗是**同一份**选项：两处都往
 * device_address.scan_frequency 里写值，选项不一致就会出现「表单里选不到的频率，
 * 表格里却显示着」这种对不上的情况。这里收成一处，两边都只负责取。
 *
 * 标签带秒数备注是给不换算 ms 的用户看的，秒数和数值必须连在一起显示，
 * 拆成两段拼接的文案没法翻译，所以整条走 i18n（见 utils/addressSyntax.js 的同款约定）。
 *
 * 取值不是随手定的：100 ms 是 Modbus 这类协议的常见轮询下限，60 s 是上限，
 * 中间的档位按「快档留密、慢档留疏」排；改动前先确认驱动侧不会因为频率太高而丢帧。
 */
const FREQUENCIES = [100, 200, 500, 1000, 2000, 5000, 10000, 30000, 60000]

export function useScanFrequencyOptions() {
  const { t } = useI18n()
  return computed(() =>
    FREQUENCIES.map((ms) => ({
      value: ms,
      // 不足 1 秒的档位没有可省略的零头，加「（0.5秒）」只是噪音
      label: ms % 1000 === 0
        ? t('deviceAddress.scanFrequencySeconds', { ms, s: ms / 1000 })
        : `${ms} ms`,
    })),
  )
}
