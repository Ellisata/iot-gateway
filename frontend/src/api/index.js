import axios from 'axios'
import { ElMessage } from 'element-plus'
import router from '@/router'

// 创建 axios 实例
const request = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '/api',
  timeout: 15000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// 请求拦截器
request.interceptors.request.use(
  (config) => {
    // 注入 Token（后续接入认证后启用）
    const token = localStorage.getItem('token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => {
    return Promise.reject(error)
  }
)

// 响应拦截器
request.interceptors.response.use(
  (response) => {
    const res = response.data
    const config = response.config

    // 如果请求配置了 silent，跳过错误提示
    if (config?.silent) return res

    if (res.code === "20004" || res.code === "20005" || res.code === "20006") {
      localStorage.removeItem('token')
      router.push('/login')
      return Promise.reject(new Error(res.msg || '登录已过期，请重新登录'))
    }

    // 如果后端返回的业务状态码不是 0，按错误处理
    if (res.code && res.code !== "0") {
      ElMessage.error(res.msg || '请求失败')
      return Promise.reject(new Error(res.msg || '请求失败'))
    }
    return res.data
  },
  (error) => {
    // 静默模式跳过错误提示
    if (error.config?.silent) {
      return Promise.reject(error)
    }

    if (error.response) {
      switch (error.response.status) {
        case 401:
          ElMessage.error('登录已过期，请重新登录')
          localStorage.removeItem('token')
          router.push('/login')
          break
        case 403:
          ElMessage.error('没有权限访问')
          break
        case 404:
          ElMessage.error('请求的资源不存在')
          break
        case 500:
          ElMessage.error('服务器错误')
          break
        default:
          ElMessage.error(error.message || '网络错误')
      }
    } else {
      ElMessage.error('网络连接异常')
    }
    return Promise.reject(error)
  }
)

export default request
