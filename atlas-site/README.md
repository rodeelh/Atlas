# atlas-site

Marketing + download site for Atlas. Astro + Tailwind, dark-only Terminal theme.

## Develop

```bash
cd atlas-site
npm install
npm run dev      # http://localhost:4321
npm run build    # → dist/
npm run preview  # serve dist/
```

## Layout

```
src/
  layouts/Base.astro       — html shell, fonts, favicon
  components/
    Wordmark.astro         — █ atlas mark (animated cursor optional)
    Nav.astro              — sticky top nav
    Hero.astro             — section 00
    Features.astro         — section 01
    Architecture.astro     — section 02 (ASCII diagram)
    Capabilities.astro     — section 03
    Privacy.astro          — section 04
    Download.astro         — section 05
    Footer.astro
  pages/index.astro        — composes all sections
  styles/global.css        — tailwind + Geist fonts + cursor blink keyframe
public/
  logo.svg                 — static wordmark
  favicon.svg              — block-only favicon
```

## Brand

- **Mark:** `█ atlas` — green prompt block + lowercase Geist Mono
- **Palette:** Terminal preset (dark) — `#060612` bg, `#00FF99` accent, `#E0E0FF` text
- **Type:** Geist Mono for headings/labels/code, Geist Sans for body

## Deploy

Static output. Cloudflare Pages or GitHub Pages — both serve `dist/` as-is.
Domain (placeholder): `atlas.ai`.
