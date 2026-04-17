import { readFileSync } from 'node:fs'
import { defineConfig, type Plugin } from 'vite'
import preact from '@preact/preset-vite'

const { version } = JSON.parse(readFileSync('./package.json', 'utf-8')) as { version: string }

// Remove crossorigin attributes from generated HTML.
// When the web UI is served from a LAN IP over plain HTTP, crossorigin="anonymous"
// on <script type="module"> causes browsers to apply CORS restrictions on same-origin
// requests, which breaks the page. Atlas doesn't use SRI so crossorigin isn't needed.
function removeCrossorigin(): Plugin {
  return {
    name: 'remove-crossorigin',
    transformIndexHtml(html) {
      return html.replace(/ crossorigin/g, '')
    },
  }
}

export default defineConfig({
  plugins: [preact(), removeCrossorigin()],
  define: {
    __APP_VERSION__: JSON.stringify(version),
  },
  test: {
    exclude: ['e2e/**', 'node_modules/**', 'dist/**'],
  },
  base: '/web/',
  build: {
    // Output to dist/ — served by the Go binary via -web-dir flag.
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('chart.js')) return 'vendor-chart'
            if (id.includes('highlight.js')) return 'vendor-highlight'
            if (id.includes('marked') || id.includes('dompurify')) return 'vendor-markdown'
            if (id.includes('preact')) return 'vendor-preact'
            return 'vendor'
          }

          if (id.includes('/src/screens/Chat') || id.includes('/src/screens/chatStream')) {
            return 'screen-chat'
          }
          if (id.includes('/src/screens/Automations') || id.includes('/src/screens/Workflows') || id.includes('/src/screens/Team')) {
            return 'screen-operations'
          }
          if (id.includes('/src/screens/Settings') || id.includes('/src/screens/AIProviders') || id.includes('/src/screens/APIKeys') || id.includes('/src/screens/Theme') || id.includes('/src/screens/LocalLM')) {
            return 'screen-settings'
          }
          if (id.includes('/src/screens/Skills') || id.includes('/src/screens/Forge') || id.includes('/src/screens/Docs') || id.includes('/src/screens/Usage')) {
            return 'screen-capabilities'
          }
          if (id.includes('/src/screens/Activity') || id.includes('/src/screens/Mind') || id.includes('/src/screens/Communications') || id.includes('/src/screens/Approvals') || id.includes('/src/screens/Dashboards') || id.includes('/src/screens/Onboarding')) {
            return 'screen-support'
          }
          return undefined
        },
      },
    },
  },
})
