// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 备份包列表（新到旧）
 * @returns {Promise<Array<{name, scope, size, modTime}>>}
 */
export function listBackups() {
  return request({
    url: '/backup/list',
    method: 'get',
  })
}

/**
 * 调度状态与待恢复任务
 * @returns {Promise<Object>} { enabled, dir, defaultScope, keep, maxTotalMB, intervalSeconds, debounceSeconds, pending }
 */
export function getBackupStatus() {
  return request({
    url: '/backup/status',
    method: 'get',
  })
}

/**
 * 立即备份
 * @param {Object} data - { scope: 'full' | 'config' }
 */
export function createBackup(data) {
  return request({
    url: '/backup/create',
    method: 'post',
    data,
    timeout: 120000,
  })
}

/**
 * 下载备份包
 * @param {string} name
 * @returns {Promise<Blob>}
 */
export function downloadBackup(name) {
  return request({
    url: '/backup/download',
    method: 'get',
    params: { name },
    responseType: 'blob',
    timeout: 300000,
  })
}

/**
 * 删除备份包
 * @param {string} name
 */
export function deleteBackup(name) {
  return request({
    url: '/backup/delete',
    method: 'delete',
    params: { name },
  })
}

/**
 * 把已有备份包暂存为“下次启动恢复”
 * @param {Object} data - { name, identity: 'takeover' | 'clone' }
 */
export function stageBackup(data) {
  return request({
    url: '/backup/stage',
    method: 'post',
    data,
    timeout: 120000,
  })
}

/**
 * 上传备份包并暂存
 * @param {FormData} formData - 含 file 与 identity
 */
export function uploadBackup(formData) {
  return request({
    url: '/backup/upload',
    method: 'post',
    data: formData,
    timeout: 600000,
  })
}

/**
 * 取消待恢复任务
 */
export function cancelPendingBackup() {
  return request({
    url: '/backup/cancelPending',
    method: 'post',
  })
}
