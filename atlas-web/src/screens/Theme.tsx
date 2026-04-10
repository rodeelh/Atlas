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
  DEFAULT_ACCENT,
  THEME_PRESETS,
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
  { color: '#6BA8A4', label: 'Sage Teal' },
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

const fonts: { id: ChatFont; label: string; sublabel: string; sample: string }[] = [
  { id: 'mono',    label: 'Terminal', sublabel: 'JetBrains Mono',  sample: 'Aa' },
  { id: 'default', label: 'Default',  sublabel: 'Inter',           sample: 'Aa' },
  { id: 'serif',   label: 'Serif',    sublabel: 'Iowan',           sample: 'Aa' },
]

const avatarStyles: { id: ChatAvatarStyle; label: string }[] = [
  { id: 'glyph',   label: 'Glyph'   },
  { id: 'initial', label: 'Initial' },
  { id: 'minimal', label: 'Minimal' },
]

const bubbleStyles: { id: ChatBubbleStyle; label: string }[] = [
  { id: 'bubbles', label: 'Bubbles' },
  { id: 'ghost',   label: 'Default' },
  { id: 'flat',    label: 'Flat'    },
]

const widths: { id: ChatWidth; label: string }[] = [
  { id: 'narrow',  label: 'Narrow'  },
  { id: 'default', label: 'Default' },
  { id: 'wide',    label: 'Wide'    },
]

const FONT_FAMILIES: Record<ChatFont, string> = {
  default: "'Inter', -apple-system, sans-serif",
  mono: "'JetBrains Mono', 'SF Mono', 'Menlo', monospace",
  serif: "'Iowan Old Style', 'Palatino Linotype', 'Book Antiqua', Palatino, Georgia, serif",
}

const FONT_SIZE_PX: Record<ChatFontSize, string> = {
  small: '13px',
  default: '15px',
  large: '17px',
}

const RADIUS_PX: Record<ChatRadius, string> = {
  sharp: '6px',
  default: '12px',
  rounded: '18px',
}

const DENSITY_GAP: Record<DensityMode, string> = {
  compact:     '6px',
  comfortable: '10px',
  spacious:    '16px',
}

const PREVIEW_BUBBLE_PADDING: Record<DensityMode, [string, string]> = {
  compact:     ['8px',  '13px'],
  comfortable: ['12px', '18px'],
  spacious:    ['16px', '24px'],
}

const DENSITY_LINE_SCALE: Record<DensityMode, number[]> = {
  compact: [16, 24, 12],
  comfortable: [18, 28, 15],
  spacious: [20, 30, 18],
}

function SectionTitle({ children }: { children: preact.ComponentChild }) {
  return <div class="appearance-section-title">{children}</div>
}

function SegmentedRow({
  label,
  options,
  columns,
  activeID,
  onChange,
}: {
  label: string
  options: { id: string; label: string; style?: JSX.CSSProperties }[]
  columns?: number
  activeID: string
  onChange: (id: string) => void
}) {
  const cols = columns ?? options.length
  return (
    <div class="segment-row">
      <span class="segment-row-label">{label}</span>
      <div
        class="segment-group"
        role="listbox"
        aria-label={label}
        style={`grid-template-columns: repeat(${cols}, 1fr)`}
      >
        {options.map((opt) => (
          <button
            key={opt.id}
            class={`segment-btn${activeID === opt.id ? ' is-active' : ''}`}
            onClick={() => onChange(opt.id)}
            role="option"
            aria-selected={activeID === opt.id}
            style={opt.style}
          >
            {opt.label}
          </button>
        ))}
      </div>
    </div>
  )
}

function CheckIcon({ className }: { className?: string }) {
  return (
    <svg class={className} width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.3" stroke-linecap="round" stroke-linejoin="round">
      <path d="M3 8l3.5 3.5L13 4.5" />
    </svg>
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
}: Props) {
  const colorInputRef = useRef<HTMLInputElement>(null)
  const currentPreset = THEME_PRESETS.find((preset) => preset.id === activePreset) ?? THEME_PRESETS[0]
  const resolvedMode: 'light' | 'dark' =
    activeTheme === 'system'
      ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : activeTheme

  const previewStyle = {
    '--appearance-accent': activeAccent,
    '--appearance-preview-font': FONT_FAMILIES[activeChatFont],
    '--appearance-preview-font-size': FONT_SIZE_PX[activeChatFontSize],
    '--appearance-preview-radius': RADIUS_PX[activeChatRadius],
    '--appearance-preview-gap': DENSITY_GAP[activeDensity],
    '--appearance-preview-pad-y': PREVIEW_BUBBLE_PADDING[activeDensity][0],
    '--appearance-preview-pad-x': PREVIEW_BUBBLE_PADDING[activeDensity][1],
  } as JSX.CSSProperties

  return (
    <div class="screen">
      <PageHeader title="Appearance" subtitle="Themes, accent colour, and chat display" />

      <div class="appearance-screen">
        <div class="appearance-layout">
          <div class="appearance-column">
            <div class="card appearance-card">
              <div class="card-body appearance-card-body">
                <SectionTitle>Theme</SectionTitle>
                <div class="segment-list">
                  <SegmentedRow
                    label="Preset"
                    options={THEME_PRESETS.map((p) => ({ id: p.id, label: p.label }))}
                    activeID={activePreset}
                    onChange={(id) => onPresetChange(id as ThemePreset)}
                  />
                  <SegmentedRow
                    label="Mode"
                    options={modes}
                    activeID={activeTheme}
                    onChange={(id) => onThemeChange(id as ThemeMode)}
                  />
                  <div class="segment-row">
                    <span class="segment-row-label">Accent</span>
                    <div class="segment-row-swatches">
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
            </div>


            <div class="card appearance-card">
              <div class="card-body appearance-card-body">
                <SectionTitle>Chat Display</SectionTitle>
                <div class="segment-list">
                  <SegmentedRow
                    label="Avatar"
                    options={avatarStyles}
                    activeID={activeChatAvatarStyle}
                    onChange={(id) => onChatAvatarStyleChange(id as ChatAvatarStyle)}
                  />
                  <SegmentedRow
                    label="Density"
                    options={densities}
                    activeID={activeDensity}
                    onChange={(id) => onDensityChange(id as DensityMode)}
                  />
                  <SegmentedRow
                    label="Font Size"
                    options={fontSizes}
                    activeID={activeChatFontSize}
                    onChange={(id) => onChatFontSizeChange(id as ChatFontSize)}
                  />
                  <SegmentedRow
                    label="Font"
                    options={fonts.map((f) => ({
                      id: f.id,
                      label: f.label,
                      style: { fontFamily: FONT_FAMILIES[f.id as ChatFont] },
                    }))}
                    activeID={activeChatFont}
                    onChange={(id) => onChatFontChange(id as ChatFont)}
                  />
                  <SegmentedRow
                    label="Corners"
                    options={radii}
                    activeID={activeChatRadius}
                    onChange={(id) => onChatRadiusChange(id as ChatRadius)}
                  />
                  <SegmentedRow
                    label="Bubble"
                    options={bubbleStyles}
                    activeID={activeChatBubbleStyle}
                    onChange={(id) => onChatBubbleStyleChange(id as ChatBubbleStyle)}
                  />
                  <SegmentedRow
                    label="Width"
                    options={widths}
                    activeID={activeChatWidth}
                    onChange={(id) => onChatWidthChange(id as ChatWidth)}
                  />
                </div>
              </div>
            </div>
          </div>

          <div class="appearance-column">
            <div class="card appearance-card appearance-preview-card">
              <div class="card-body appearance-card-body">
                <SectionTitle>Preview</SectionTitle>
                <div class={`appearance-preview-frame appearance-preview-avatar-style-${activeChatAvatarStyle}`} data-preview-bubble-style={activeChatBubbleStyle} style={previewStyle}>
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
                        <span class="appearance-preview-avatar-minimal"><span /></span>
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
                        <span class="appearance-preview-avatar-minimal"><span /></span>
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
                        <span class="appearance-preview-avatar-minimal"><span /></span>
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
      </div>
    </div>
  )
}
