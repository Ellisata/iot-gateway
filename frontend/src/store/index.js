import { defineStore } from 'pinia'

const USER_INFO_KEY = 'userInfo'

/** 从 localStorage 恢复用户信息（登录态刷新后不丢失） */
function loadUserInfo() {
  try {
    return JSON.parse(localStorage.getItem(USER_INFO_KEY) || 'null')
  } catch {
    return null
  }
}

/**
 * 用户状态管理
 */
export const useUserStore = defineStore('user', {
  state: () => ({
    token: localStorage.getItem('token') || '',
    userInfo: loadUserInfo(),
  }),

  getters: {
    isLoggedIn: (state) => !!state.token,
    userName: (state) => state.userInfo?.name || '',
    /** 是否为管理员（当前按用户名 admin 判定） */
    isAdmin: (state) => state.userInfo?.role === 'admin',
  },

  actions: {
    setToken(token) {
      this.token = token
      localStorage.setItem('token', token)
    },

    setUserInfo(info) {
      this.userInfo = info
      localStorage.setItem(USER_INFO_KEY, JSON.stringify(info))
    },

    logout() {
      this.token = ''
      this.userInfo = null
      localStorage.removeItem('token')
      localStorage.removeItem(USER_INFO_KEY)
    },
  },
})
