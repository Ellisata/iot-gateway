import request from '@/api'

/**
 * 获取设备对象列表（分页）
 * @param {Object} params - 查询参数
 * @param {string} params.iotGatewayId - 网关 ID
 * @param {string} [params.name] - 按名称搜索（可选）
 * @param {number} params.page - 页码（从 1 开始）
 * @param {number} params.size - 每页条数
 * @returns {Promise}
 */
export function getDeviceObjectList(params = {}) {
  return request({
    url: '/device/pageDevice',
    method: 'get',
    params: {
      iotGatewayId: params.iotGatewayId,
      page: params.page,
      size: params.size,
      name: params.name || undefined,
    },
  })
}

/**
 * 获取设备在线情况统计（首页概览）
 * @returns {Promise<{total:number, enabled:number, disabled:number, collected:number, online:number, offline:number, unCollected:number, onlineRate:number}>}
 * total=全部设备, enabled=启用, disabled=禁用, collected=已接入采集,
 * online=在线, offline=离线, unCollected=启用但未接入采集, onlineRate=在线率(0~1)
 */
export function getDeviceOverview() {
  return request({
    url: '/device/overview',
    method: 'get',
  })
}

/**
 * 新增设备对象
 * @param {Object} data
 * @param {string} data.iotGatewayId - 所属网关 ID
 * @param {string} data.name - 名称（必填，1-100 字符）
 * @param {string} data.protocol - 协议（必填）
 * @param {string} [data.description] - 描述（可选）
 * @param {number} [data.status] - 状态 0/1（可选）
 * @returns {Promise}
 */
export function addDeviceObject(data) {
  return request({
    url: '/device/createDevice',
    method: 'post',
    data,
  })
}

/**
 * 修改设备对象
 * @param {Object} data
 * @param {string|number} data.id - ID（必填）
 * @param {string} data.name - 名称（必填）
 * @param {string} data.protocol - 协议（必填）
 * @param {string} [data.description] - 描述
 * @param {number} [data.status] - 状态
 * @returns {Promise}
 */
export function updateDeviceObject(data) {
  return request({
    url: '/device/updateDevice',
    method: 'post',
    data,
  })
}

/**
 * 根据 ID 获取设备对象详情
 * @param {string|number} id - 设备对象 ID
 * @returns {Promise<Object>} 设备对象详情，含 protocolConfig 等完整字段
 */
export function getDeviceObjectById(id) {
  return request({
    url: `/device/getDeviceById/${id}`,
    method: 'get',
  })
}

/**
 * 批量下发设备对象
 * @param {number[]} ids - 设备对象 ID 数组
 * @returns {Promise}
 */
export function issueDeviceObject(ids) {
  return request({
    url: '/deviceObject/issue',
    method: 'post',
    data: { ids },
  })
}

/**
 * 获取未绑定设备列表（分页）
 * @param {Object} params - 查询参数
 * @param {number} params.page - 页码（从 1 开始）
 * @param {number} params.size - 每页条数
 * @param {string} [params.name] - 按名称模糊搜索（可选）
 * @returns {Promise}
 */
export function getUnboundDeviceList(params = {}) {
  return request({
    url: '/deviceObject/listUnbound',
    method: 'get',
    params: {
      page: params.page,
      size: params.size,
      name: params.name || undefined,
    },
  })
}

/**
 * 发布设备对象到指定网关
 * @param {Object} data
 * @param {string} data.iotGatewayId - 目标网关 ID
 * @param {number[]} data.deviceIds - 设备对象 ID 数组（至少一个）
 * @returns {Promise}
 */
export function issueDevicesToGateway(data) {
  return request({
    url: '/deviceObject/issue',
    method: 'post',
    data,
  })
}

/**
 * 删除设备对象
 * @param {string|number} id - 设备对象 ID
 * @returns {Promise}
 */
export function deleteDeviceObject(id) {
  return request({
    url: `/device/deleteDevice/${id}`,
    method: 'get',
  })
}

/**
 * 全量发布所有设备对象
 * @returns {Promise}
 */
export function publishAll() {
  return request({
    url: '/deviceObject/publishAll',
    method: 'post',
  })
}

/**
 * 监测设备对象连接状态
 * @param {Object} data - 连接测试参数
 * @param {string} data.protocolName - 协议名称（如 ModBus.TCP）
 * @param {Object} data.protocolJson - 协议配置 JSON（host/port/batch 等）
 * @returns {Promise<{connected: boolean}>}
 */
export function testDeviceConnection(data) {
  return request({
    url: '/device/testDeviceConnection',
    method: 'post',
    data,
  })
}

/**
 * 监控设备对象数据（分页）
 * @param {Object} data
 * @param {number} data.objectId - 设备对象 ID
 * @param {string|number} data.page - 页码
 * @param {string|number} data.size - 每页条数
 * @returns {Promise}
 */
export function monitorDeviceData(data) {
  return request({
    url: '/deviceObject/monitorDeviceData',
    method: 'post',
    data,
  })
}
