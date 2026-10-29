// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 查询扫描页初始化所需的全部信息（协议 / 网段 / 开关）。
 *
 * 三个信息合成一个响应，是因为进页面时都要拿到 —— 分开就是三次鉴权加三次往返。
 *
 * @returns {Promise<{enabled:boolean, maxTargets:number,
 *   protocols:Array<{name:string, protocolId:string, title:string, description:string,
 *     defaultPort:number, targetMode:string, scanFormJson:object}>,
 *   subnets:Array<{iface:string, cidr:string, localIp:string, broadcast:string, hostCount:number}>>}
 */
export function getScanCapabilities() {
  return request({
    url: '/deviceScan/capabilities',
    method: 'get',
    // 默认 15s 足够：这一层不做任何网络探测，只读本机网卡与已加载的插件。
  })
}

/**
 * 发起一次扫描。
 *
 * **登记任务即返回**，不等扫描结束 —— 一个 /24 网段的扫描是十几秒到几十秒的量级。
 * 拿到 jobId 后用 getScanStatus 轮询。
 *
 * @param {{protocol:string, options?:object, subnets?:string[], port?:number}} data
 * subnets 只能取自 getScanCapabilities 返回的网段（后端会拒绝其它 CIDR）。
 * @returns {Promise<object>} 任务快照（同 getScanStatus）。目标数超过单批上限时
 * `truncated=true`，剩下的地址要调 continueScan 接着扫。
 */
export function startScan(data) {
  return request({
    url: '/deviceScan/start',
    method: 'post',
    data,
  })
}

/**
 * 续扫当前任务的下一批目标。
 *
 * 目标数超过单批上限（deviceScan.maxTargets）时，startScan 只扫前一批，快照里
 * `truncated=true`、`remainingTargets>0`。点「继续扫描」调这里：**任务 id 不变**，
 * 已发现的结果保留，服务端把目标换成下一批后继续跑。
 *
 * 只传 jobId —— 协议/选项/网段/端口必须沿用第一批（服务端持有），重传会让
 * 两批的目标列表错位。上一批的在飞探测还没收尾时返回"已有扫描任务在运行"。
 *
 * @param {string} jobId
 * @returns {Promise<object>} 任务快照（同 getScanStatus）
 */
export function continueScan(jobId) {
  return request({
    url: '/deviceScan/continue',
    method: 'post',
    data: { jobId },
  })
}

/**
 * 查询扫描进度与结果（前端轮询）。
 *
 * **轮询传 withDevices:false 拿轻量快照**：结果清单只增不减，一份扫到几百台的
 * 清单每拍全量回传没意义（绝大多数拍子并没有新设备）。轻量快照里 `devices` 与
 * `errors` 是 **null**，完整快照里一定是数组 —— 调用方据"是不是数组"决定保留
 * 还是清空自己持有的那份清单，并拿 `found`（两份快照同源）判断该不该补一次
 * withDevices:true 的请求。省略 = 完整快照。
 *
 * @param {string} jobId
 * @param {{withDevices?: boolean}} [options]
 * @returns {Promise<object>} 任务快照。
 * 任务过期（只在内存中保留，网关重启或超过保留期即失效）时后端返回
 * "扫描任务不存在或已过期"的业务错误码，调用方应停止轮询并提示重新扫描。
 */
export function getScanStatus(jobId, { withDevices } = {}) {
  return request({
    url: '/deviceScan/status',
    method: 'get',
    params: withDevices === undefined ? { jobId } : { jobId, withDevices },
    // 轮询失败不该弹错误提示（拦截器会按 silent 跳过），
    // 由轮询逻辑统一处理"任务已过期"。
    silent: true,
  })
}

/**
 * 取消扫描。已结束的任务取消是幂等的成功。
 * @param {string} jobId
 */
export function cancelScan(jobId) {
  return request({
    url: '/deviceScan/cancel',
    method: 'post',
    data: { jobId },
  })
}

/**
 * 把勾选的扫描结果批量建成设备。
 *
 * **不回传 protocolJson**：连接配置由服务端从它自己持有的任务快照里取，
 * 避免客户端篡改决定网关去连哪个地址的配置。index 是结果数组下标。
 *
 * **地址重复是两段式的**：扫描地址（协议+主机+端口+站号）与库内某台设备相同时，
 * 后端默认**不建**这一行，只回一条 `code: 'addressExists'` 的明细 —— 调用方确认
 * 之后再带 `confirmDuplicate: true` 重提，只重提被判重复的那几行。同一地址上多台
 * 设备是合法场景（Modbus TCP 串口服务器后面挂多个从站、多通道串口服务器的不同
 * 端口），所以既不能默默建、也不能拦死。判据用 code 而不是 reason 文案：
 * 文案随 i18n 改，code 是契约。
 *
 * @param {{jobId:string, items:Array<{index:number, name:string, description:string}>,
 *   confirmDuplicate?:boolean}} data
 * @returns {Promise<{total:number, success:number, failed:number,
 *   errors:Array<{row:number, name:string, reason:string, code?:string}>}>}
 * 逐条成败明细，部分成功是预期结果（HTTP 始终 200）。
 * code 可空，只用于分辨需要特殊处理的行，展示一律走 reason。
 */
export function createDevicesFromScan(data) {
  return request({
    url: '/deviceScan/createDevices',
    method: 'post',
    data,
  })
}

// ==================== 点位范围扫描（设备地址页的「自动发现点位」） ====================
//
// 与上面那组共用 deviceScan.enabled 总开关，但**同步**：一次扫描覆盖整段地址，
// 插件侧没有中途取结果的入口，做不成可轮询的任务。因此这里的超时必须放宽 ——
// 网关侧把插件预算钳在 9s 以内，加上排队与前端收尾，留 30s 是安全的余量。

/**
 * 查询某个协议的点位扫描能力（设备地址页进页面时问一次）。
 *
 * **不支持是正常答案（200 + supported=false），不是错误**：调用方据
 * enabled/supported 的组合决定按钮是可用、置灰还是给出理由。
 * 千万不要用 try/catch 把它当成失败 —— 那会把"这个协议不支持"显示成网络错误。
 *
 * @param {string} protocolName 协议名（= 路由上的 protocolName）
 * @returns {Promise<{enabled:boolean, supported:boolean, protocol:string,
 *   spec?:{title:string, description:string, pointFormJson:object,
 *     defaultCommonDataType:string, maxBudgetMs:number, maxPoints:number}}>}
 */
export function getPointCapability(protocolName) {
  return request({
    url: '/deviceScan/pointCapability',
    method: 'get',
    params: { protocolName },
  })
}

/**
 * 对一台已存在的设备发起一次点位范围扫描。
 *
 * **同步接口，可能等满 9 秒**：这里显式覆盖 axios 默认的 15s 超时，免得将来
 * 有人在全局把它调小，症状是"扫描到一半就报网络错误"而插件其实还在跑。
 *
 * **不回传 protocolJson**：连接配置由服务端从 device 行读取后透传给插件。
 * 客户端传的那个决定网关去连哪台设备，属于跨信任边界的数据。
 *
 * @param {{deviceId:string, options?:object}} data
 * @returns {Promise<{protocol:string, points:Array<{name:string, value?:string,
 *   label?:string, note?:string}>, assumedCommonDataType:string,
 *   scanned:number, readable:number, unreadable:number,
 *   truncated:boolean, incomplete:boolean, elapsedMs:number}>}
 * truncated（结果条数到上限）与 incomplete（预算耗尽、范围没扫完）是两个
 * 不同的标志，界面上不能合并成一句话。
 */
export function discoverPoints(data) {
  return request({
    url: '/deviceScan/discoverPoints',
    method: 'post',
    data,
    timeout: 30000,
  })
}

/**
 * 把勾选的点位批量建成设备地址。
 *
 * `name` 是设备地址写法（如 %MW100），由扫描结果原样带过来 —— 它决定采集能否
 * 解析，写错的后果是**这个点位永远没有值**（只报「地址无效」），所以界面上该列只读。
 * `label` 留空时服务端兜底为 name（扫描结果没有标签来源，这是有意的）。
 *
 * @param {{deviceId:string,
 *   items:Array<{name:string, label:string, commonDataType:string,
 *     rwPermission:string, scanFrequency:number, description:string}>}} data
 * @returns {Promise<{total:number, success:number, failed:number,
 *   errors:Array<{row:number, name:string, reason:string}>}>}
 * 逐条成败明细，部分成功是预期结果（HTTP 始终 200）。
 */
export function createAddressesFromScan(data) {
  return request({
    url: '/deviceScan/createAddresses',
    method: 'post',
    data,
    timeout: 30000,
  })
}
