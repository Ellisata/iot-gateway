// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 查询网关侧的插件环境信息（平台 / 插件目录 / 命名规则）
 * @returns {Promise<{os:string, arch:string, dir:string, glob:string, fileExt:string, enabled:boolean}>}
 */
export function getPluginInfo() {
  return request({
    url: '/plugin/info',
    method: 'get',
  })
}

/**
 * 批量上传协议插件文件
 *
 * @param {File[]} files - 待上传的插件文件（单次上限见后端 MaxPluginBatch）
 * @returns {Promise<{total:number, success:number, failed:number,
 *   errors:Array<{name:string, reason:string}>, dir:string, pending:number,
 *   hint:string, warnings:string[]}>}
 * total=总文件数, success=成功数, failed=失败数, errors=失败明细,
 * pending=本次写入数（需等下一轮扫描才会加载）
 */
export function importPlugins(files) {
  const formData = new FormData()
  // 多文件用**同名**字段重复追加，后端按 files 读取全部部件。
  files.forEach((file) => formData.append('files', file))

  return request({
    url: '/plugin/importPlugin',
    method: 'post',
    data: formData,
    // 插件单个 5~8MB，一整套几十 MB；默认 15s 在慢速局域网上会超时。
    timeout: 300000,
  })
}
