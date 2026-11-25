// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 物模型分页列表
 * @param {Object} params
 * @param {number} params.page
 * @param {number} params.size
 * @param {string} [params.keyword] 同时模糊匹配 code 与 name
 * @param {string} [params.category] sensor/meter/actuator/robot/plc/other
 * @returns {Promise<{page:number,size:number,total:number,records:Array}>}
 */
export function getThingModelList(params = {}) {
  return request({
    url: '/thingModel/pageThingModel',
    method: 'get',
    params: {
      page: params.page,
      size: params.size,
      keyword: params.keyword || undefined,
      category: params.category || undefined,
    },
  })
}

/**
 * 全部物模型（设备绑定下拉用）
 * @returns {Promise<Array<{id:string,code:string,name:string,status:number}>>}
 */
export function getThingModels() {
  return request({
    url: '/thingModel/listThingModels',
    method: 'get',
  })
}

/**
 * 列出绑定到某属性的设备点位（属性列表「点位数」抽屉）
 * @param {string} propertyId
 * @returns {Promise<Array<{addressId:string,deviceId:string,deviceCode:string,deviceName:string,name:string,label:string,commonDataType:string,dataType:string,rwPermission:string,status:number}>>}
 */
export function listThingModelPropertyPoints(propertyId) {
  return request({
    url: '/thingModel/listThingModelPropertyPoints',
    method: 'get',
    params: { propertyId },
  })
}

/**
 * 属性表单的点位候选：全部设备的点位（属性与设备无绑定关系）
 * @returns {Promise<Array<{addressId:string,deviceId:string,deviceCode:string,deviceName:string,name:string,label:string,kind:string,rwPermission:string,propertyId:string,propertyIdentifier:string,propertyName:string,status:number}>>}
 */
export function listThingModelPointOptions() {
  return request({
    url: '/thingModel/listThingModelPointOptions',
    method: 'get',
  })
}

/**
 * 属性绑定点位的第一级：设备候选（分页 + 关键词）
 * @param {Object} params
 * @param {number} params.page
 * @param {number} params.size
 * @param {string} [params.keyword] 同时模糊匹配设备名称与编码
 * @returns {Promise<{page:number,size:number,total:number,records:Array<{id:string,code:string,name:string,status:number,pointCount:number}>}>}
 */
export function pageThingModelPointDevices(params = {}) {
  return request({
    url: '/thingModel/pageThingModelPointDevices',
    method: 'get',
    params: {
      page: params.page,
      size: params.size,
      keyword: params.keyword || undefined,
    },
  })
}

/**
 * 属性绑定点位的第二级：某设备的点位候选（分页 + 关键词）
 * @param {Object} params
 * @param {string} params.deviceId
 * @param {number} params.page
 * @param {number} params.size
 * @param {string} [params.keyword] 同时模糊匹配地址名与说明
 * @returns {Promise<{page:number,size:number,total:number,records:Array}>} 记录字段与 listThingModelPointOptions 一致
 */
export function pageThingModelPointOptions(params = {}) {
  return request({
    url: '/thingModel/pageThingModelPointOptions',
    method: 'get',
    params: {
      deviceId: params.deviceId,
      page: params.page,
      size: params.size,
      keyword: params.keyword || undefined,
    },
  })
}

/**
 * 跨模型的属性下拉（设备点位绑定属性用）
 * @returns {Promise<Array<{id:string,modelId:string,modelCode:string,modelName:string,identifier:string,name:string,kind:string,accessMode:string,unit:string,status:number}>>}
 */
export function listAllThingModelProperties() {
  return request({
    url: '/thingModel/listAllThingModelProperties',
    method: 'get',
  })
}

/**
 * 新增物模型
 * @param {Object} data
 * @param {string} data.code 模型标识（字母开头，创建后不可改）
 * @param {string} data.name 显示名
 * @param {string} [data.category] 分类 code
 * @param {string} [data.version] 版本号，默认 1.0.0
 * @param {string} [data.description]
 */
export function createThingModel(data) {
  return request({
    url: '/thingModel/createThingModel',
    method: 'post',
    data,
  })
}

/**
 * 复制物模型为一条新记录（只复制基础信息，不复制属性）
 * 标识与名称自动加 -copy 后缀并查重（-copy2、-copy3…）
 * @param {string} id 被复制的物模型 ID
 * @returns {Promise<{id:string, code:string, name:string}>}
 */
export function copyThingModel(id) {
  return request({
    url: '/thingModel/copyThingModel',
    method: 'post',
    data: { id },
  })
}

/**
 * 修改物模型（code 创建后不可改）
 * @param {Object} data
 * @param {string} data.id
 * @param {string} [data.code] 省略或与原值一致
 * @param {string} data.name
 * @param {string} [data.category]
 * @param {string} [data.version]
 * @param {string} [data.description]
 * @param {number} [data.status] 0/1，省略 = 不改
 */
export function updateThingModel(data) {
  return request({
    url: '/thingModel/updateThingModel',
    method: 'post',
    data,
  })
}

/**
 * 删除物模型（属性仍被点位绑定时会被拒绝）
 * @param {string} id
 */
export function deleteThingModel(id) {
  return request({
    url: `/thingModel/deleteThingModel/${id}`,
    method: 'get',
  })
}

/**
 * 根据 ID 查询物模型
 * @param {string} id
 */
export function getThingModelById(id) {
  return request({
    url: `/thingModel/getThingModelById/${id}`,
    method: 'get',
  })
}

/**
 * 列出某模型下的全部属性
 * @param {string} modelId
 * @returns {Promise<Array>}
 */
export function listThingModelProperties(modelId) {
  return request({
    url: '/thingModel/listThingModelProperties',
    method: 'get',
    params: { modelId },
  })
}

/**
 * 某模型下的属性分页
 * @param {string} modelId
 * @param {Object} params
 * @param {number} params.page
 * @param {number} params.size
 * @param {string} [params.keyword] 同时模糊匹配属性标识符与名称
 * @returns {Promise<{page:number,size:number,total:number,records:Array,maxSortOrder:number}>}
 */
export function pageThingModelProperties(modelId, params = {}) {
  return request({
    url: '/thingModel/pageThingModelProperties',
    method: 'get',
    params: {
      modelId,
      page: params.page,
      size: params.size,
      keyword: params.keyword || undefined,
    },
  })
}

/**
 * 新增物模型属性
 * @param {Object} data
 * @param {string} data.modelId
 * @param {string} data.identifier 属性标识符（字母开头，模型内唯一）
 * @param {string} data.name
 * @param {string} data.kind bool/int/uint/float/string/time
 * @param {string} data.accessMode R/W/RW
 * @param {string} [data.unit]
 * @param {string} [data.description]
 * @param {number} [data.sortOrder]
 * @param {string[]} [data.addressIds] 创建时一并绑定的点位
 */
export function createThingModelProperty(data) {
  return request({
    url: '/thingModel/createThingModelProperty',
    method: 'post',
    data,
  })
}

/**
 * 修改物模型属性（identifier 可改名，模型内仍须唯一）
 * @param {Object} data
 * @param {string} data.id
 * @param {string} [data.identifier] 省略或与原值一致
 * @param {string} data.name
 * @param {string} data.kind
 * @param {string} data.accessMode
 * @param {string} [data.unit]
 * @param {string} [data.description]
 * @param {number} [data.sortOrder]
 * @param {number} [data.status] 0/1，省略 = 不改
 * @param {string[]} [data.addressIds] 省略 = 不改绑定；[] = 全部解绑；非空 = 以此为准
 */
export function updateThingModelProperty(data) {
  return request({
    url: '/thingModel/updateThingModelProperty',
    method: 'post',
    data,
  })
}

/**
 * 删除物模型属性（被点位绑定时会被拒绝）
 * @param {string} id
 */
export function deleteThingModelProperty(id) {
  return request({
    url: `/thingModel/deleteThingModelProperty/${id}`,
    method: 'get',
  })
}

/**
 * 批量导入物模型（Excel .xlsx）
 * @param {File} file - 选择的 Excel 文件
 * @returns {Promise<{total:number, success:number, failed:number, errors:Array<{row:number, name:string, reason:string}>}>}
 */
export function importThingModels(file) {
  const formData = new FormData()
  formData.append('file', file)
  return request({
    url: '/thingModel/importThingModel',
    method: 'post',
    data: formData,
  })
}

/**
 * 下载物模型导入模板（.xlsx，五列：模型标识/名称/分类/版本/描述）
 * @returns {Promise<Blob>}
 */
export function downloadThingModelTemplate() {
  return request({
    url: '/thingModel/importThingModelTemplate',
    method: 'get',
    responseType: 'blob',
  })
}

/**
 * 批量导入物模型属性（Excel .xlsx）
 * @param {File} file - 选择的 Excel 文件
 * @param {string} modelId - 所属物模型 ID
 * @returns {Promise<{total:number, success:number, failed:number, errors:Array<{row:number, name:string, reason:string}>}>}
 */
export function importThingModelProperties(file, modelId) {
  const formData = new FormData()
  formData.append('file', file)
  formData.append('modelId', modelId)
  return request({
    url: '/thingModel/importThingModelProperty',
    method: 'post',
    data: formData,
  })
}

/**
 * 下载物模型属性导入模板（.xlsx，七列：属性标识/名称/数据类型/读写模式/单位/排序/描述）
 * @returns {Promise<Blob>}
 */
export function downloadThingModelPropertyTemplate() {
  return request({
    url: '/thingModel/importThingModelPropertyTemplate',
    method: 'get',
    responseType: 'blob',
  })
}
