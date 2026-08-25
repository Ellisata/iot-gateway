import request from '@/api'

/**
 * 获取采集引擎状态（首页概览）
 * @returns {Promise<{running:boolean, lastPollTime:string, lastSuccessTime:string, errorCount:number, deviceCount:number, addressCount:number}>}
 * running=采集是否运行中, lastPollTime=最近轮询时间, lastSuccessTime=最近成功采集时间,
 * errorCount=累计采集错误数, deviceCount=参与采集设备数, addressCount=参与采集地址数
 */
export function getCollectionStatus() {
  return request({
    url: '/collection/status',
    method: 'get',
  })
}
