import preact from '@preact/preset-vite'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [preact(), tailwindcss()],
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
