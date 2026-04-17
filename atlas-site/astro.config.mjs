import { defineConfig } from 'astro/config'
import tailwind from '@astrojs/tailwind'

export default defineConfig({
  site: 'https://atlas.ai',
  integrations: [tailwind({ applyBaseStyles: false })],
})
