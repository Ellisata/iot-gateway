import request from '@/api'

/**
 * 分页查询日志文件列表
 * @param {Object} params - 查询参数
 * @param {number} params.page - 页码（从 1 开始，缺省 1）
 * @param {number} params.size - 每页条数（缺省 20，上限 100）
 * @returns {Promise<{page: number, size: number, total: number, records: Array}>} 分页数据
 */
export function getLogFilePage(params = {}) {
  return request({
    url: '/log/listFiles',
    method: 'get',
    params: {
      page: params.page,
      size: params.size,
    },
  })
}

/**
 * 读取日志文件末尾 N 行
 * @param {Object} params - 查询参数
 * @param {string} params.fileName - 日志文件名（必填）
 * @param {number} [params.tail] - 末尾行数（缺省 200，上限 5000）
 * @returns {Promise<{fileName: string, totalLines: number, lines: Array<string>}>}
 */
export function readLogFile(params = {}) {
  return request({
    url: '/log/readFile',
    method: 'get',
    params: {
      fileName: params.fileName,
      tail: params.tail,
    },
  })
}
