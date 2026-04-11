/**
 * Atlas Theming Engine — V1.1
 *
 * Manages dark / light / system mode + accent colour.
 * ThemeConfig is the extensibility seam for V2:
 * add borderRadius, fontScale, etc. here and wire through applyTheme().
 */

export type ThemeMode = 'system' | 'light' | 'dark'
export type ThemePreset = 'atlas' | 'studio' | 'terminal'

export type DensityMode     = 'compact' | 'comfortable' | 'spacious'
export type ChatFontSize    = 'small' | 'default' | 'large'
export type ChatRadius      = 'sharp' | 'default' | 'rounded'
export type ChatFont        = 'default' | 'mono' | 'serif'
export type ChatAvatarStyle = 'glyph' | 'initial' | 'minimal'
export type ChatBubbleStyle = 'bubbles' | 'ghost' | 'flat'
export type ChatWidth       = 'narrow' | 'default' | 'wide' | 'full'

export interface ThemeConfig {
  preset:          ThemePreset
  mode:            ThemeMode
  accent:          string
  density:         DensityMode
  chatFontSize:    ChatFontSize
  chatRadius:      ChatRadius
  chatFont:        ChatFont
  chatAvatarStyle: ChatAvatarStyle
  chatBubbleStyle: ChatBubbleStyle
  chatWidth:       ChatWidth
}

const STORAGE_KEY = 'atlas.theme'

export const DEFAULT_ACCENT = '#4D86C8'

type PresetModeTokens = Record<string, string>

export type ThemePresetOption = {
  id: ThemePreset
  label: string
  description: string
  preview: {
    light: {
      surface: string
      surfaceAlt: string
      accent: string
    }
    dark: {
      surface: string
      surfaceAlt: string
      accent: string
    }
  }
}

export const THEME_PRESETS: ThemePresetOption[] = [
  {
    id: 'atlas',
    label: 'Atlas',
    description: 'Quiet',
    preview: {
      light: {
        surface: '#f4f4f2',
        surfaceAlt: '#ffffff',
        accent: DEFAULT_ACCENT,
      },
      dark: {
        surface: '#1a1a1a',
        surfaceAlt: '#262626',
        accent: DEFAULT_ACCENT,
      },
    },
  },
  {
    id: 'studio',
    label: 'Studio',
    description: 'Professional',
    preview: {
      light: {
        surface: '#eceae5',
        surfaceAlt: '#f7f5f2',
        accent: '#bf5530',
      },
      dark: {
        surface: '#141210',
        surfaceAlt: '#1c1a18',
        accent: '#d4682a',
      },
    },
  },
  {
    id: 'terminal',
    label: 'Terminal',
    description: 'Phosphor',
    preview: {
      light: {
        surface: '#bce4b4',
        surfaceAlt: '#d8f4d0',
        accent: '#00e536',
      },
      dark: {
        surface: '#040a04',
        surfaceAlt: '#081408',
        accent: '#22c55e',
      },
    },
  },
]

export const PRESET_TOKENS: Record<ThemePreset, { light: PresetModeTokens; dark: PresetModeTokens }> = {
  atlas: {
    light: {},
    dark: {},
  },
  studio: {
    // Design studio — warm stone light, deep charcoal dark, terracotta accent
    light: {
      '--bg': '#d8d4ce',
      '--surface': '#eceae5',
      '--surface-2': '#f7f5f2',
      '--surface-3': '#c4c0b8',
      '--hover': 'rgba(22,18,14,0.06)',
      '--active-bg': 'rgba(22,18,14,0.10)',
      '--border': 'rgba(22,18,14,0.12)',
      '--border-2': 'rgba(22,18,14,0.22)',
      '--text': '#161210',
      '--text-2': '#6a6058',
      '--text-3': '#9c928a',
      '--shadow-bubble-ai': '0 10px 24px rgba(22,18,14,0.08), 0 2px 6px rgba(22,18,14,0.04)',
      '--shadow-bubble-user': '0 12px 28px color-mix(in srgb, var(--accent) 22%, transparent), 0 3px 8px rgba(22,18,14,0.08)',
      '--shadow-avatar': '0 8px 18px rgba(22,18,14,0.09), 0 1px 4px rgba(22,18,14,0.05)',
      '--theme-shadow-card': '0 20px 44px rgba(22,18,14,0.09), 0 4px 14px rgba(22,18,14,0.05)',
      '--theme-shadow-soft': '0 10px 22px rgba(22,18,14,0.05)',
      '--theme-shadow-pop': '0 26px 50px rgba(22,18,14,0.11)',
    },
    dark: {
      '--bg': '#0c0a08',
      '--surface': '#141210',
      '--surface-2': '#1c1a18',
      '--surface-3': '#262420',
      '--hover': 'rgba(240,235,228,0.05)',
      '--active-bg': 'rgba(240,235,228,0.08)',
      '--border': 'rgba(240,235,228,0.09)',
      '--border-2': 'rgba(240,235,228,0.16)',
      '--text': '#f2ede8',
      '--text-2': '#a09088',
      '--text-3': '#625850',
      '--shadow-bubble-ai': '0 0 20px rgba(212,104,42,0.07)',
      '--shadow-bubble-user': '0 0 26px color-mix(in srgb, var(--accent) 34%, transparent)',
      '--shadow-avatar': '0 0 14px rgba(212,104,42,0.08)',
      '--theme-shadow-card': '0 16px 40px rgba(0,0,0,0.42)',
      '--theme-shadow-soft': '0 4px 14px rgba(0,0,0,0.10)',
      '--theme-shadow-pop': '0 24px 48px rgba(0,0,0,0.20)',
    },
  },
  terminal: {
    // Phosphor CRT — Matrix light: printed code paper + neon green accent; near-black dark
    light: {
      '--bg': '#a8d4a0',
      '--surface': '#bce4b4',
      '--surface-2': '#d8f4d0',
      '--surface-3': '#8cba84',
      '--hover': 'rgba(0,60,0,0.09)',
      '--active-bg': 'rgba(0,180,40,0.14)',
      '--border': 'rgba(0,60,0,0.20)',
      '--border-2': 'rgba(0,60,0,0.36)',
      '--text': '#001400',
      '--text-2': '#004800',
      '--text-3': '#1a6c1a',
      '--shadow-bubble-ai': '0 8px 18px rgba(0,60,0,0.14), 0 2px 5px rgba(0,60,0,0.08)',
      '--shadow-bubble-user': '0 10px 22px color-mix(in srgb, var(--accent) 32%, transparent), 0 2px 6px rgba(0,60,0,0.10)',
      '--shadow-avatar': '0 5px 14px rgba(0,60,0,0.16), 0 1px 3px rgba(0,60,0,0.08)',
      '--theme-shadow-card': '0 14px 32px rgba(0,60,0,0.14), 0 2px 8px rgba(0,60,0,0.08)',
      '--theme-shadow-soft': '0 6px 16px rgba(0,60,0,0.08)',
      '--theme-shadow-pop': '0 20px 38px rgba(0,60,0,0.16)',
    },
    dark: {
      '--bg': '#010301',
      '--surface': '#040a04',
      '--surface-2': '#081408',
      '--surface-3': '#0e1e0e',
      '--hover': 'rgba(34,197,94,0.07)',
      '--active-bg': 'rgba(34,197,94,0.12)',
      '--border': 'rgba(34,197,94,0.14)',
      '--border-2': 'rgba(34,197,94,0.26)',
      '--text': '#d4f5d4',
      '--text-2': '#4cae6a',
      '--text-3': '#285a38',
      '--shadow-bubble-ai': '0 0 14px rgba(34,197,94,0.08)',
      '--shadow-bubble-user': '0 0 20px color-mix(in srgb, var(--accent) 30%, transparent)',
      '--shadow-avatar': '0 0 10px rgba(34,197,94,0.10)',
      '--theme-shadow-card': '0 10px 24px rgba(0,0,0,0.50)',
      '--theme-shadow-soft': '0 2px 10px rgba(0,0,0,0.08)',
      '--theme-shadow-pop': '0 18px 36px rgba(0,0,0,0.20)',
    },
  },
}

const PRESET_TOKEN_KEYS = Array.from(
  new Set(
    Object.values(PRESET_TOKENS).flatMap((preset) => [
      ...Object.keys(preset.light),
      ...Object.keys(preset.dark),
    ]),
  ),
)

export const DEFAULT_THEME: ThemeConfig = {
  preset:          'atlas',
  mode:            'system',
  accent:          DEFAULT_ACCENT,
  density:         'comfortable',
  chatFontSize:    'default',
  chatRadius:      'default',
  chatFont:        'default',
  chatAvatarStyle: 'glyph',
  chatBubbleStyle: 'ghost',
  chatWidth:       'default',
}

// ── Persistence ──────────────────────────────────────────────

export function loadTheme(): ThemeConfig {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT_THEME
    return { ...DEFAULT_THEME, ...JSON.parse(raw) }
  } catch {
    return DEFAULT_THEME
  }
}

export function saveTheme(config: ThemeConfig): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(config))
}

// ── Application ──────────────────────────────────────────────

/** Resolves 'system' to the actual OS preference. */
function resolveMode(mode: ThemeMode): 'dark' | 'light' {
  if (mode !== 'system') return mode
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

// chat-msg-gap is the *extra* gap between groups on top of the 28px padding-bottom
// already reserved for the absolutely-positioned meta row.
// Total space between bubbles = 28px + gap:
//   compact → 28 + 2  = 30px
//   comfortable → 28 + 6  = 34px
//   spacious → 28 + 14 = 42px
const DENSITY_TOKENS: Record<DensityMode, Record<string, string>> = {
  compact:     { '--bubble-pad-v': '8px',  '--bubble-pad-h': '13px', '--chat-msg-gap': '2px'  },
  comfortable: { '--bubble-pad-v': '12px', '--bubble-pad-h': '18px', '--chat-msg-gap': '6px'  },
  spacious:    { '--bubble-pad-v': '16px', '--bubble-pad-h': '24px', '--chat-msg-gap': '14px' },
}

const FONT_SIZE_TOKENS: Record<ChatFontSize, Record<string, string>> = {
  small:   { '--bubble-font-size': '13px' },
  default: { '--bubble-font-size': '15px' },
  large:   { '--bubble-font-size': '17px' },
}

const RADIUS_TOKENS: Record<ChatRadius, Record<string, string>> = {
  sharp:   { '--bubble-radius': '6px',  '--bubble-radius-notch': '2px' },
  default: { '--bubble-radius': '10px', '--bubble-radius-notch': '3px' },
  rounded: { '--bubble-radius': '18px', '--bubble-radius-notch': '5px' },
}

const FONT_TOKENS: Record<ChatFont, Record<string, string>> = {
  default: { '--bubble-font': "'Inter', -apple-system, 'Helvetica Neue', sans-serif" },
  mono:    { '--bubble-font': "'JetBrains Mono', 'SF Mono', 'Menlo', 'Courier New', monospace" },
  serif:   { '--bubble-font': "'Iowan Old Style', 'Palatino Linotype', 'Book Antiqua', Palatino, Georgia, serif" },
}

const WIDTH_TOKENS: Record<ChatWidth, Record<string, string>> = {
  narrow:  { '--chat-content-max': 'min(600px,  calc(100% - 96px))' },
  default: { '--chat-content-max': 'min(900px,  calc(100% - 96px))' },
  wide:    { '--chat-content-max': 'min(1200px, calc(100% - 48px))' },
  full:    { '--chat-content-max': 'calc(100% - 32px)'              },
}

function writeTokens(tokens: Record<string, string>): void {
  Object.entries(tokens).forEach(([key, value]) => {
    document.documentElement.style.setProperty(key, value)
  })
}

function clearTokens(keys: string[]): void {
  keys.forEach((key) => {
    document.documentElement.style.removeProperty(key)
  })
}

function semanticAccentTokens(accent: string): Record<string, string> {
  return {
    '--theme-accent-fill': accent,
    '--theme-accent-fill-strong': accent,
    '--theme-accent-outline': `color-mix(in srgb, ${accent} 12%, transparent)`,
    '--theme-border-accent': `color-mix(in srgb, ${accent} 45%, var(--border-2))`,
    '--theme-focus-ring': `color-mix(in srgb, ${accent} 26%, transparent)`,
    '--theme-surface-accent': `color-mix(in srgb, ${accent} 9%, var(--surface-2))`,
    '--theme-surface-accent-strong': `color-mix(in srgb, ${accent} 12%, var(--surface-2))`,
    '--theme-shadow-accent': `color-mix(in srgb, ${accent} 24%, transparent)`,
    '--control-focus-ring': `color-mix(in srgb, ${accent} 26%, transparent)`,
    '--control-selected-bg': `color-mix(in srgb, ${accent} 9%, var(--surface-2))`,
    '--control-selected-bg-strong': `color-mix(in srgb, ${accent} 12%, var(--surface-2))`,
    '--control-selected-border': `color-mix(in srgb, ${accent} 45%, var(--border-2))`,
    '--control-selected-outline': `color-mix(in srgb, ${accent} 12%, transparent)`,
    '--theme-selection-bg': `color-mix(in srgb, ${accent} 32%, transparent)`,
  }
}

/**
 * Writes data-theme onto <html> and injects accent + density + font size + radius CSS variables.
 * V2 note: this now also writes semantic runtime aliases so the full web app can
 * consume theme values through semantic tokens instead of raw accent/chat vars.
 */
export function applyTheme(config: ThemeConfig): void {
  const resolvedMode = resolveMode(config.mode)
  document.documentElement.setAttribute('data-theme', resolvedMode)
  document.documentElement.setAttribute('data-chat-avatar-style', config.chatAvatarStyle)
  clearTokens(PRESET_TOKEN_KEYS)
  writeTokens(PRESET_TOKENS[config.preset][resolvedMode])
  document.documentElement.style.setProperty('--accent', config.accent)
  writeTokens(semanticAccentTokens(config.accent))

  const densityTokens = DENSITY_TOKENS[config.density]
  writeTokens({
    ...densityTokens,
    '--theme-chat-gap': densityTokens['--chat-msg-gap'],
    '--theme-chat-pad-y': densityTokens['--bubble-pad-v'],
    '--theme-chat-pad-x': densityTokens['--bubble-pad-h'],
  })

  const fontSizeTokens = FONT_SIZE_TOKENS[config.chatFontSize]
  writeTokens({
    ...fontSizeTokens,
    '--theme-chat-font-size': fontSizeTokens['--bubble-font-size'],
  })

  const radiusTokens = RADIUS_TOKENS[config.chatRadius]
  writeTokens({
    ...radiusTokens,
    '--theme-chat-radius': radiusTokens['--bubble-radius'],
    '--theme-chat-radius-notch': radiusTokens['--bubble-radius-notch'],
  })

  const fontTokens = FONT_TOKENS[config.chatFont]
  writeTokens({
    ...fontTokens,
    '--theme-chat-font': fontTokens['--bubble-font'],
  })

  document.documentElement.setAttribute('data-chat-bubble-style', config.chatBubbleStyle)
  writeTokens(WIDTH_TOKENS[config.chatWidth])
}

/**
 * Watches for OS-level theme changes when mode is 'system'.
 * Returns a cleanup function — call it in useEffect's return.
 */
export function watchSystemTheme(config: ThemeConfig, onChanged: () => void): () => void {
  if (config.mode !== 'system') return () => {}
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  mq.addEventListener('change', onChanged)
  return () => mq.removeEventListener('change', onChanged)
}
