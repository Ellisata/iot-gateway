// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 通道脚本编辑器的默认示例。
 *
 * 新建通道或切到 script 格式时填入编辑器，同时也是空编辑器的占位内容。
 * 页面与编辑器共用这一份文本：编辑器据此判断当前内容是否仍是未改动的示例，
 * 从而显示「示例」标识，避免被当成已经写好的正式脚本。
 */
export const DEFAULT_ENCODE_SCRIPT = [
  'function encode(batch, ctx) {',
  '  var out = []',
  '  for (var i = 0; i < batch.records.length; i++) {',
  '    var r = batch.records[i]',
  '    out.push(JSON.stringify({',
  '      device: batch.deviceId,',
  '      point: r.deviceAddressId,',
  '      value: Number(r.value)',
  '    }))',
  '  }',
  '  return out',
  '}',
].join('\n')
