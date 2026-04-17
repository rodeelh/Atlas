/** @type {import('tailwindcss').Config} */
export default {
  content: ['./src/**/*.{astro,html,js,jsx,md,mdx,svelte,ts,tsx,vue}'],
  theme: {
    extend: {
      colors: {
        bg:        '#060612',
        surface:   '#0A0A1A',
        'surface-2': '#151525',
        'surface-3': '#1C1C30',
        border:    'rgba(0,255,153,0.12)',
        'border-2':'rgba(0,255,153,0.22)',
        text:      '#E0E0FF',
        'text-2':  '#8080A8',
        'text-3':  '#4A4A6A',
        accent:    '#00FF99',
        'accent-dim': 'rgba(0,255,153,0.6)',
      },
      fontFamily: {
        mono: ['"Geist Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
        sans: ['"Geist Sans"', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
      boxShadow: {
        glow:    '0 0 24px rgba(0,255,153,0.18)',
        'glow-soft': '0 0 14px rgba(0,255,153,0.10)',
      },
    },
  },
  plugins: [],
}
