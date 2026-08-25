import request from '@/api'

/**
 * 获取设备地址列表（分页）
 * @param {Object} params - 查询参数
 * @param {number} params.deviceObjectId - 设备对象 ID
 * @param {string} [params.name] - 按名称搜索（可选）
 * @param {number} params.page - 页码（从 1 开始）
 * @param {number} params.size - 每页条数
 * @returns {Promise<{records: Array, total: number}>}
 */
export function getDeviceAddressList(params = {}) {
  return request({
    url: '/deviceAddress/pageDeviceAddress',
    method: 'get',
    params: {
      deviceId: params.deviceObjectId,
      page: params.page,
      size: params.size,
      name: params.name || undefined,
    },
  })
}

/**
 * 新增设备地址
 * @param {Object} data
 * @param {int} data.deviceObjectId - 所属设备对象 ID
 * @param {string} data.name - 名称
 * @param {string} data.label - 标签（地址名称的中文说明，如温度、电流）
 * @param {string} data.dataType - 数据类型
 * @param {string} data.rwPermission - 读写权限（R/W/RW）
 * @param {number} data.scanFrequency - 扫描频率（ms）
 * @param {string} [data.description] - 描述（可选）
 * @param {number} [data.status] - 状态 0/1
 * @returns {Promise}
 */
export function addDeviceAddress(data) {
  return request({
    url: '/deviceAddress/createDeviceAddress',
    method: 'post',
    data,
  })
}

/**
 * 修改设备地址
 * @param {Object} data
 * @param {string|number} data.id - ID
 * @param {string} data.name - 名称
 * @param {string} data.label - 标签（地址名称的中文说明，如温度、电流）
 * @param {string} data.dataType - 数据类型
 * @param {string} data.rwPermission - 读写权限
 * @param {number} data.scanFrequency - 扫描频率
 * @param {string} [data.description] - 描述
 * @param {number} [data.status] - 状态
 * @returns {Promise}
 */
export function updateDeviceAddress(data) {
  return request({
    url: '/deviceAddress/updateDeviceAddress',
    method: 'post',
    data,
  })
}

/**
 * 根据 ID 获取设备地址详情
 * @param {string|number} id - 设备地址 ID
 * @returns {Promise<Object>}
 */
export function getDeviceAddressById(id) {
  return request({
    url: `/deviceAddress/getDeviceAddressById/${id}`,
    method: 'get',
  })
}

/**
 * 删除设备地址
 * @param {string|number} id - 设备地址 ID
 * @returns {Promise}
 */
export function deleteDeviceAddress(id) {
  return request({
    url: `/deviceAddress/deleteDeviceAddress/${id}`,
    method: 'get',
  })
}

/**
 * 监控设备地址标签数据（实时读取）
 * @param {Object} params
 * @param {number} params.objectId - 设备对象 ID
 * @param {string} params.address - 设备地址
 * @returns {Promise<Object>}
 */
export function monitorTagData(params = {}) {
  return request({
    url: '/deviceObject/monitorTagData',
    method: 'post',
    data: params,
  })
}
