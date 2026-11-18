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
 * 查询已加载插件清单
 *
 * 协议页用它把协议行映射到供给它的插件文件：一个插件可以供给多个协议，
 * 卸载的粒度是文件而不是单行。
 *
 * @returns {Promise<Array<{name:string, protocols:string[], scope:string,
 *   version:string, sha256:string, up:boolean, epoch:number}>>}
 */
export function listPlugins() {
  return request({
    url: '/plugin/list',
    method: 'get',
  })
}

/**
 * 卸载插件：停进程、删除插件文件，并清理它供给的协议行
 *
 * 若仍有设备使用该插件的协议，后端会拒绝（90009）并给出设备清单。
 *
 * @param {string} file - 插件文件名（basename，如 iot-driver-modbus.exe）
 * @returns {Promise}
 */
export function uninstallPlugin(file) {
  return request({
    url: '/plugin/uninstallPlugin',
    method: 'post',
    data: { file },
    // 后端要先退役插件进程（优雅停机宽限 + 强杀回收，最坏约 2×2s）再删文件；
    // 全局 15s 在慢速机器上偏紧，按上传路径的先例放宽。
    timeout: 60000,
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
