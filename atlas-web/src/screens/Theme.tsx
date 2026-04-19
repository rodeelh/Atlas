import { type JSX } from 'preact'
import { useRef } from 'preact/hooks'
import { PageHeader } from '../components/PageHeader'
import {
  type ThemePreset,
  type ThemeMode,
  type DensityMode,
  type ChatFontSize,
  type ChatRadius,
  type ChatFont,
  type ChatAvatarStyle,
  type ChatBubbleStyle,
  type ChatWidth,
  type UIRadius,
  type UIBlur,
  type UIFont,
  DEFAULT_ACCENT,
  THEME_PRESETS,
  PRESET_TOKENS,
} from '../theme'

interface Props {
  activePreset: ThemePreset
  onPresetChange: (preset: ThemePreset) => void
  activeTheme: ThemeMode
  onThemeChange: (mode: ThemeMode) => void
  activeAccent: string
  onAccentChange: (accent: string) => void
  activeDensity: DensityMode
  onDensityChange: (d: DensityMode) => void
  activeChatFontSize: ChatFontSize
  onChatFontSizeChange: (s: ChatFontSize) => void
  activeChatRadius: ChatRadius
  onChatRadiusChange: (r: ChatRadius) => void
  activeChatFont: ChatFont
  onChatFontChange: (f: ChatFont) => void
  activeChatAvatarStyle: ChatAvatarStyle
  onChatAvatarStyleChange: (style: ChatAvatarStyle) => void
  activeChatBubbleStyle: ChatBubbleStyle
  onChatBubbleStyleChange: (s: ChatBubbleStyle) => void
  activeChatWidth: ChatWidth
  onChatWidthChange: (w: ChatWidth) => void
  activeUIRadius: UIRadius
  onUIRadiusChange: (r: UIRadius) => void
  activeUIBlur: UIBlur
  onUIBlurChange: (b: UIBlur) => void
  activeUIFont: UIFont
  onUIFontChange: (f: UIFont) => void
}

const modes: { id: ThemeMode; label: string }[] = [
  { id: 'system', label: 'System' },
  { id: 'light',  label: 'Light'  },
  { id: 'dark',   label: 'Dark'   },
]

const ACCENT_PRESETS = [
  { color: '#7E7E7E', label: 'Neutral' },
  { color: '#7B8BA8', label: 'Slate' },
  { color: DEFAULT_ACCENT, label: 'Atlas Blue' },
  { color: '#6B8EC8', label: 'Cornflower' },
  { color: '#8B82C8', label: 'Lavender' },
  { color: '#A87BAA', label: 'Plum' },
  { color: '#8BA882', label: 'Sage Green' },
  { color: '#C8A87A', label: 'Warm Sand' },
  { color: '#C89070', label: 'Terracotta' },
  { color: '#C88B82', label: 'Dusty Rose' },
]

const densities: { id: DensityMode; label: string }[] = [
  { id: 'compact',     label: 'Compact'     },
  { id: 'comfortable', label: 'Comfortable' },
  { id: 'spacious',    label: 'Spacious'    },
]

const fontSizes: { id: ChatFontSize; label: string }[] = [
  { id: 'small',   label: 'Small'   },
  { id: 'default', label: 'Default' },
  { id: 'large',   label: 'Large'   },
]

const radii: { id: ChatRadius; label: string }[] = [
  { id: 'sharp',   label: 'Sharp'   },
  { id: 'default', label: 'Default' },
  { id: 'rounded', label: 'Rounded' },
]

const fonts: { id: ChatFont; label: string; family: string }[] = [
  { id: 'mono',    label: 'Mono',    family: "'JetBrains Mono', 'SF Mono', monospace"    },
  { id: 'default', label: 'Default', family: "'Inter', -apple-system, sans-serif"        },
  { id: 'geist',   label: 'Geist',   family: "'Geist', -apple-system, sans-serif"        },
]

const avatarStyles: { id: ChatAvatarStyle; label: string }[] = [
  { id: 'glyph',   label: 'Glyph'   },
  { id: 'initial', label: 'Initial' },
  { id: 'minimal', label: 'Minimal' },
]

const bubbleStyles: { id: ChatBubbleStyle; label: string }[] = [
  { id: 'bubbles', label: 'Filled'  },
  { id: 'ghost',   label: 'Flat'    },
  { id: 'flat',    label: 'Outline' },
]

const widths: { id: ChatWidth; label: string }[] = [
  { id: 'narrow',  label: 'Narrow'  },
  { id: 'default', label: 'Default' },
  { id: 'wide',    label: 'Wide'    },
]

const uiRadii: { id: UIRadius; label: string }[] = [
  { id: 'sharp',   label: 'Sharp'   },
  { id: 'default', label: 'Default' },
  { id: 'rounded', label: 'Rounded' },
]

const uiBlurs: { id: UIBlur; label: string }[] = [
  { id: 'none',   label: 'None'   },
  { id: 'glass',  label: 'Glass'  },
  { id: 'subtle', label: 'Subtle' },
]

const uiFonts: { id: UIFont; label: string; family: string }[] = [
  { id: 'mono',   label: 'Mono',   family: "'JetBrains Mono', 'SF Mono', monospace"    },
  { id: 'system', label: 'System', family: "-apple-system, BlinkMacSystemFont, sans-serif" },
  { id: 'geist',  label: 'Geist',  family: "'Geist', -apple-system, sans-serif"         },
]

const FONT_FAMILIES: Record<ChatFont, string> = {
  default: "'Inter', -apple-system, sans-serif",
  mono:    "'JetBrains Mono', 'SF Mono', 'Menlo', monospace",
  geist:   "'Geist', -apple-system, sans-serif",
}

const FONT_SIZE_PX: Record<ChatFontSize, string> = {
  small:   '13px',
  default: '15px',
  large:   '17px',
}

const RADIUS_PX: Record<ChatRadius, string> = {
  sharp:   '6px',
  default: '10px',  // matches RADIUS_TOKENS in theme.ts
  rounded: '18px',
}

const UI_RADIUS_PX: Record<UIRadius, string> = {
  sharp:   '0px',
  default: '10px',
  rounded: '20px',
}

const DENSITY_GAP: Record<DensityMode, string> = {
  compact:     '2px',   // matches --chat-msg-gap in DENSITY_TOKENS (theme.ts)
  comfortable: '6px',
  spacious:    '14px',
}

const PREVIEW_BUBBLE_PADDING: Record<DensityMode, [string, string]> = {
  compact:     ['8px',  '13px'],
  comfortable: ['12px', '18px'],
  spacious:    ['16px', '24px'],
}

// ── Shared option-row control ───────────────────────────────────────────────

function OptionRow({
  label,
  sublabel,
  options,
  activeID,
  onChange,
}: {
  label: string
  sublabel?: string
  options: { id: string; label: string; style?: JSX.CSSProperties }[]
  activeID: string
  onChange: (id: string) => void
}) {
  return (
    <div class="settings-row">
      <div class="settings-label-col">
        <div class="settings-label">{label}</div>
        {sublabel && <div class="settings-sublabel">{sublabel}</div>}
      </div>
      <div class="ap-option-group">
        {options.map((opt) => (
          <button
            key={opt.id}
            class={`ap-option-btn${activeID === opt.id ? ' is-active' : ''}`}
            onClick={() => onChange(opt.id)}
            style={opt.style}
          >
            {opt.label}
          </button>
        ))}
      </div>
    </div>
  )
}

export function Theme({
  activePreset,
  onPresetChange,
  activeTheme,
  onThemeChange,
  activeAccent,
  onAccentChange,
  activeDensity,
  onDensityChange,
  activeChatFontSize,
  onChatFontSizeChange,
  activeChatRadius,
  onChatRadiusChange,
  activeChatFont,
  onChatFontChange,
  activeChatAvatarStyle,
  onChatAvatarStyleChange,
  activeChatBubbleStyle,
  onChatBubbleStyleChange,
  activeChatWidth,
  onChatWidthChange,
  activeUIRadius,
  onUIRadiusChange,
  activeUIBlur,
  onUIBlurChange,
  activeUIFont,
  onUIFontChange,
}: Props) {
  const colorInputRef = useRef<HTMLInputElement>(null)
  const currentPreset = THEME_PRESETS.find((p) => p.id === activePreset) ?? THEME_PRESETS[0]
  const resolvedMode: 'light' | 'dark' =
    activeTheme === 'system'
      ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : activeTheme

  const presetColorTokens = PRESET_TOKENS[activePreset][resolvedMode]

  // Use the preset's '--accent' override (if any) as the preview accent so the
  // preview faithfully reflects the effective colour — e.g. Terminal light
  // overrides the bright electric green to a dark forest green for contrast.
  // Exclude '--accent' from the spread so it doesn't bleed into CSS rules that
  // reference var(--accent) directly, which would fight var(--appearance-accent).
  const { '--accent': presetAccentOverride, ...presetColorTokensWithoutAccent } = presetColorTokens
  const effectivePreviewAccent = presetAccentOverride ?? activeAccent

  const previewStyle = {
    ...presetColorTokensWithoutAccent,
    '--appearance-accent': effectivePreviewAccent,
    '--appearance-preview-font': FONT_FAMILIES[activeChatFont],
    '--appearance-preview-font-size': FONT_SIZE_PX[activeChatFontSize],
    '--appearance-preview-radius': RADIUS_PX[activeChatRadius],
    '--appearance-preview-gap': DENSITY_GAP[activeDensity],
    '--appearance-preview-pad-y': PREVIEW_BUBBLE_PADDING[activeDensity][0],
    '--appearance-preview-pad-x': PREVIEW_BUBBLE_PADDING[activeDensity][1],
    '--appearance-preview-ui-radius': UI_RADIUS_PX[activeUIRadius],
  } as JSX.CSSProperties

  return (
    <div class="screen">
      <PageHeader title="Appearance" subtitle="Themes, accent colour, and chat display" />

      <div class="ap-layout">

        {/* Theme card — row 1, col 1 */}
        <div class="card">
            <div class="card-header">
              <span class="card-title">Theme</span>
            </div>
            <div class="settings-group">
              <div class="settings-row">
                <div class="settings-label-col">
                  <div class="settings-label">Preset</div>
                </div>
                <select
                  class="input"
                  style={{ width: '160px' }}
                  value={activePreset}
                  onChange={(e) => onPresetChange((e.target as HTMLSelectElement).value as ThemePreset)}
                >
                  {THEME_PRESETS.map((p) => (
                    <option key={p.id} value={p.id}>{p.label}</option>
                  ))}
                </select>
              </div>
              <OptionRow
                label="Mode"
                options={modes}
                activeID={activeTheme}
                onChange={(id) => onThemeChange(id as ThemeMode)}
              />
              {/* Accent row */}
              <div class="settings-row">
                <div class="settings-label-col">
                  <div class="settings-label">Accent</div>
                </div>
                <div class="ap-swatch-strip">
                  {ACCENT_PRESETS.map((preset) => {
                    const active = activeAccent.toLowerCase() === preset.color.toLowerCase()
                    return (
                      <button
                        key={preset.color}
                        class={`appearance-swatch${active ? ' is-active' : ''}`}
                        title={preset.label}
                        aria-label={preset.label}
                        onClick={() => onAccentChange(preset.color)}
                        style={{ background: preset.color }}
                      />
                    )
                  })}
                  <div class="appearance-accent-divider" />
                  <button
                    class="appearance-swatch appearance-swatch-custom"
                    title="Custom colour"
                    aria-label="Custom colour"
                    onClick={() => colorInputRef.current?.click()}
                  >
                    <input
                      ref={colorInputRef}
                      type="color"
                      value={activeAccent}
                      onInput={(e) => onAccentChange((e.target as HTMLInputElement).value)}
                      class="appearance-hidden-color-input"
                    />
                  </button>
                </div>
              </div>
            </div>
          </div>

        {/* Interface card — row 1, col 2 */}
        <div class="card">
          <div class="card-header">
            <span class="card-title">Interface</span>
          </div>
          <div class="settings-group">
            <OptionRow
              label="Corners"
              options={uiRadii}
              activeID={activeUIRadius}
              onChange={(id) => onUIRadiusChange(id as UIRadius)}
            />
            <OptionRow
              label="Blur"
              options={uiBlurs}
              activeID={activeUIBlur}
              onChange={(id) => onUIBlurChange(id as UIBlur)}
            />
            <OptionRow
              label="Font"
              options={uiFonts.map((f) => ({
                id: f.id,
                label: f.label,
                style: { fontFamily: f.family },
              }))}
              activeID={activeUIFont}
              onChange={(id) => onUIFontChange(id as UIFont)}
            />
          </div>
        </div>

        {/* Chat Display card — row 2, col 1 */}
        <div class="card">
          <div class="card-header">
            <span class="card-title">Chat Display</span>
          </div>
          <div class="settings-group">
            <OptionRow
              label="Avatar"
              options={avatarStyles}
              activeID={activeChatAvatarStyle}
              onChange={(id) => onChatAvatarStyleChange(id as ChatAvatarStyle)}
            />
            <OptionRow
              label="Bubble"
              options={bubbleStyles}
              activeID={activeChatBubbleStyle}
              onChange={(id) => onChatBubbleStyleChange(id as ChatBubbleStyle)}
            />
            <OptionRow
              label="Font"
              options={fonts.map((f) => ({
                id: f.id,
                label: f.label,
                style: { fontFamily: f.family },
              }))}
              activeID={activeChatFont}
              onChange={(id) => onChatFontChange(id as ChatFont)}
            />
            <OptionRow
              label="Size"
              options={fontSizes}
              activeID={activeChatFontSize}
              onChange={(id) => onChatFontSizeChange(id as ChatFontSize)}
            />
            <OptionRow
              label="Corners"
              options={radii}
              activeID={activeChatRadius}
              onChange={(id) => onChatRadiusChange(id as ChatRadius)}
            />
            <OptionRow
              label="Density"
              options={densities}
              activeID={activeDensity}
              onChange={(id) => onDensityChange(id as DensityMode)}
            />
            <OptionRow
              label="Width"
              options={widths}
              activeID={activeChatWidth}
              onChange={(id) => onChatWidthChange(id as ChatWidth)}
            />
          </div>
        </div>

        {/* Preview card — row 2, col 2 */}
        <div class="card ap-preview-card">
            <div class="card-header">
              <span class="card-title">Preview</span>
            </div>
            <div class="ap-preview-body">
              <div
                class={`appearance-preview-frame appearance-preview-avatar-style-${activeChatAvatarStyle}`}
                data-preview-bubble-style={activeChatBubbleStyle}
                style={previewStyle}
              >
                <div class="appearance-preview-toolbar">
                  <div class="appearance-preview-tab">{currentPreset.label} theme</div>
                  <div class="appearance-preview-status">Current</div>
                </div>

                <div class="appearance-preview-thread">
                  <div class="appearance-preview-row">
                    <div class="appearance-preview-avatar appearance-preview-avatar-ai">
                      <span class="appearance-preview-avatar-glyph">
                        <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor">
                          <circle cx="8" cy="5.5" r="3" />
                          <path d="M2.5 15c0-3 2.5-5.5 5.5-5.5S13.5 12 13.5 15" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" fill="none" />
                        </svg>
                      </span>
                      <span class="appearance-preview-avatar-initial">A</span>
                      <span class="appearance-preview-avatar-minimal">
                        <span aria-hidden="true">&gt;</span>
                      </span>
                    </div>
                    <div class="appearance-preview-bubble appearance-preview-bubble-ai">
                      Your workspace is ready. Want a clean summary or should I keep digging?
                    </div>
                  </div>

                  <div class="appearance-preview-row appearance-preview-row-user">
                    <div class="appearance-preview-avatar appearance-preview-avatar-user">
                      <span class="appearance-preview-avatar-glyph">
                        <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor">
                          <circle cx="8" cy="5.5" r="3" />
                          <path d="M2.5 15c0-3 2.5-5.5 5.5-5.5S13.5 12 13.5 15" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" fill="none" />
                        </svg>
                      </span>
                      <span class="appearance-preview-avatar-initial">Y</span>
                      <span class="appearance-preview-avatar-minimal">
                        <span aria-hidden="true">&lt;</span>
                      </span>
                    </div>
                    <div class="appearance-preview-bubble appearance-preview-bubble-user">
                      Keep digging, but make it easy to scan.
                    </div>
                  </div>

                  <div class="appearance-preview-row">
                    <div class="appearance-preview-avatar appearance-preview-avatar-ai">
                      <span class="appearance-preview-avatar-glyph">
                        <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor">
                          <circle cx="8" cy="5.5" r="3" />
                          <path d="M2.5 15c0-3 2.5-5.5 5.5-5.5S13.5 12 13.5 15" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" fill="none" />
                        </svg>
                      </span>
                      <span class="appearance-preview-avatar-initial">A</span>
                      <span class="appearance-preview-avatar-minimal">
                        <span aria-hidden="true">&gt;</span>
                      </span>
                    </div>
                    <div class="appearance-preview-bubble appearance-preview-bubble-ai">
                      Clean, focused, and Atlas-native. Looking good already.
                    </div>
                  </div>
                </div>

                <div class="appearance-preview-composer">
                  <span class="appearance-preview-composer-placeholder">Message Atlas…</span>
                  <div class="appearance-preview-composer-actions">
                    <div class="appearance-preview-composer-send">
                      <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M8 13V3M3 8l5-5 5 5" />
                      </svg>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>

      </div>
    </div>
  )
}
