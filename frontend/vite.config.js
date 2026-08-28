import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'path'

export default defineConfig({
  base: '/admin/',
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 1500,
    rolldownOptions: {
      // 关闭对无效 /* #__PURE__ */ 注解位置的报错（@vueuse/core 的遗留写法）
      checks: {
        invalidAnnotation: false,
      },
      output: {
        // 自动代码分割：将 node_modules 拆为独立 chunk
        codeSplitting: {
          groups: [
            {
              name: 'vendor',
              test: /node_modules/,
              minSize: 0,
            },
          ],
        },
      },
    },
  },

  server: {
    port: 4000,
    proxy: {
      '/api': {
        target: process.env.VITE_API_BASE_URL || 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
