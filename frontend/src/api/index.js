import axios from 'axios'
import { ElMessage } from 'element-plus'
import router from '@/router'
import { useUserStore } from '@/store'

// 统一处理登录失效：清空本地登录态后跳转登录页。
// 注意必须调用 store.logout() 而不是只删 localStorage —— 路由守卫依据
// userStore.isLoggedIn 判断登录态，只删 token 会被守卫重定向回首页。
function handleAuthError(msg) {
  useUserStore().logout()
  if (router.currentRoute.value.name !== 'Login') {
    ElMessage.error(msg)
    router.push('/login')
  }
}

// 创建 axios 实例
// 注意：不在此处设置全局 Content-Type，axios 会自动为对象请求设置
// application/json；若全局固定为 json，FormData 上传会被序列化成 JSON 导致丢失文件
const request = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '/api',
  timeout: 15000,
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
    const config = response.config

    // 二进制响应（文件下载）直接返回 Blob，不按 JSON 解析
    if (config?.responseType === 'blob') {
      return response.data
    }

    const res = response.data

    // 登录失效优先处理：即使请求配置了 silent 也要登出跳转，
    // silent 只用于屏蔽普通业务错误提示
    if (res.code === "20004" || res.code === "20005" || res.code === "20006") {
      handleAuthError(res.msg || '登录已过期，请重新登录')
      return Promise.reject(new Error(res.msg || '登录已过期，请重新登录'))
    }

    // 如果请求配置了 silent，跳过错误提示
    if (config?.silent) return res

    // 如果后端返回的业务状态码不是 0，按错误处理
    if (res.code && res.code !== "0") {
      ElMessage.error(res.msg || '请求失败')
      return Promise.reject(new Error(res.msg || '请求失败'))
    }
    return res.data
  },
  (error) => {
    // 401 无论是否 silent 都要登出跳转；silent 只屏蔽其余错误提示
    if (error.response?.status === 401) {
      handleAuthError('登录已过期，请重新登录')
      return Promise.reject(error)
    }

    // 静默模式跳过错误提示
    if (error.config?.silent) {
      return Promise.reject(error)
    }

    if (error.response) {
      switch (error.response.status) {
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
