import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// Vite 配置 —— 与后端联调说明：
//   开发态：npm run dev，/api 代理到本地 Go 后端 http://127.0.0.1:8080
//   生产态：npm run build，产物在 frontend/dist，由 Go 以 -web-dir 目录托管
const dir = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(dir, './src')
    }
  },
  server: {
    host: '127.0.0.1',
    port: 8443,
    strictPort: false,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true
      }
    }
  },
  preview: {
    host: '127.0.0.1',
    port: 8443
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true
  }
})
