import { useEffect, useState } from 'preact/hooks'
import { api, type RuntimeConfig, type VoiceOption } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { ErrorBanner } from '../components/ErrorBanner'
import { toast } from '../toast'

type AudioProviderID = 'local' | 'openai' | 'gemini' | 'elevenlabs'

const AUDIO_PROVIDERS: { id: AudioProviderID; label: string; sublabel: string }[] = [
  { id: 'local',       label: 'Local',       sublabel: 'Whisper + Kokoro, runs on-device' },
  { id: 'openai',      label: 'OpenAI',      sublabel: 'GPT-4o Transcribe + TTS' },
  { id: 'gemini',      label: 'Gemini',      sublabel: 'Gemini 2.5 Flash audio' },
  { id: 'elevenlabs',  label: 'ElevenLabs',  sublabel: 'Scribe STT + Turbo TTS' },
]

const STT_MODELS: Record<AudioProviderID, { id: string; label: string }[]> = {
  local: [],
  openai: [
    { id: 'gpt-4o-transcribe',      label: 'GPT-4o Transcribe' },
    { id: 'gpt-4o-mini-transcribe', label: 'GPT-4o Mini Transcribe (default)' },
    { id: 'whisper-1',              label: 'Whisper-1' },
  ],
  gemini: [
    { id: 'gemini-2.0-flash', label: 'Gemini 2.0 Flash (default)' },
    { id: 'gemini-1.5-flash', label: 'Gemini 1.5 Flash' },
  ],
  elevenlabs: [
    { id: 'scribe_v1', label: 'Scribe v1 (default)' },
  ],
}

const TTS_MODELS: Record<AudioProviderID, { id: string; label: string }[]> = {
  local: [],
  openai: [
    { id: 'tts-1',           label: 'TTS-1 (default)' },
    { id: 'tts-1-hd',        label: 'TTS-1 HD — Higher quality' },
    { id: 'gpt-4o-mini-tts', label: 'GPT-4o Mini TTS — Expressiveness control' },
  ],
  gemini: [
    { id: 'gemini-2.5-flash-preview-tts', label: 'Gemini 2.5 Flash TTS (default)' },
    { id: 'gemini-2.5-pro-preview-tts',   label: 'Gemini 2.5 Pro TTS' },
  ],
  elevenlabs: [
    { id: 'eleven_turbo_v2_5', label: 'Turbo v2.5 (default)' },
    { id: 'eleven_multilingual_v2', label: 'Multilingual v2 — Higher quality' },
    { id: 'eleven_flash_v2_5',      label: 'Flash v2.5 — Lowest latency' },
  ],
}

export function AudioProviders() {
  const [config, setConfig] = useState<RuntimeConfig | null>(null)
  const [draft, setDraft] = useState<RuntimeConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [voices, setVoices] = useState<VoiceOption[]>([])
  const [voicesLoading, setVoicesLoading] = useState(false)
  const [showAllVoices, setShowAllVoices] = useState(false)

  const [provider, setProvider] = useState<AudioProviderID>('local')

  const fetchVoices = async (p: AudioProviderID) => {
    setVoicesLoading(true)
    setShowAllVoices(false)
    try {
      setVoices(await api.voiceVoices(p))
    } catch {
      setVoices([])
    } finally {
      setVoicesLoading(false)
    }
  }

  useEffect(() => {
    const init = async () => {
      try {
        const cfg = await api.config()
        setConfig(cfg)
        setDraft(cfg)
        const active = ((cfg.activeAudioProvider || 'local') as AudioProviderID)
        setProvider(active)
        void fetchVoices(active)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load config.')
      } finally {
        setLoading(false)
      }
    }
    void init()
  }, [])

  const update = <K extends keyof RuntimeConfig>(key: K, value: RuntimeConfig[K]) => {
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev))
  }

  const switchProvider = async (p: AudioProviderID) => {
    setProvider(p)
    update('activeAudioProvider', p)
    update('audioSTTModel', '')
    update('audioTTSModel', '')
    update('audioTTSVoice', '')
    void fetchVoices(p)
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    setError(null)
    try {
      const result = await api.updateConfig(draft)
      setConfig(result.config)
      setDraft(result.config)
      toast.success('Audio settings saved.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save.')
    } finally {
      setSaving(false)
    }
  }

  const isDirty = (() => {
    if (!config || !draft) return false
    const audioKeys: (keyof RuntimeConfig)[] = [
      'activeAudioProvider', 'audioSTTModel', 'audioSTTLanguage',
      'audioTTSModel', 'audioTTSVoice', 'audioTTSSpeed', 'audioTTSStylePrompt',
      'voiceKokoroVoice', 'voiceWhisperLanguage',
    ]
    return audioKeys.some((k) => config[k] !== draft[k])
  })()

  if (loading) {
    return (
      <div class="screen ai-providers-screen">
        <PageHeader title="Audio" subtitle="Choose your speech-to-text and text-to-speech provider" />
        <PageSpinner />
      </div>
    )
  }

  if (!draft) {
    return (
      <div class="screen ai-providers-screen">
        <PageHeader title="Audio" subtitle="Choose your speech-to-text and text-to-speech provider" />
        <ErrorBanner error={error} />
      </div>
    )
  }

  const VOICE_PAGE = 5
  const displayedVoices = showAllVoices ? voices : voices.slice(0, VOICE_PAGE)
  const hasMore = voices.length > VOICE_PAGE

  const selectedVoice = provider === 'local' ? (draft.voiceKokoroVoice || '') : (draft.audioTTSVoice || '')
  const selectVoice = (id: string) => {
    if (provider === 'local') update('voiceKokoroVoice', id)
    else update('audioTTSVoice', id)
  }

  const ttsModelValue = draft.audioTTSModel ?? ''
  const showStylePrompt = provider === 'openai' && ttsModelValue === 'gpt-4o-mini-tts'

  return (
    <div class="screen ai-providers-screen">
      <PageHeader
        title="Audio"
        subtitle="Choose your speech-to-text and text-to-speech provider"
        actions={
          <button class="btn btn-primary btn-sm" onClick={save} disabled={saving || !isDirty}>
            {saving
              ? <><span class="spinner spinner-sm" style={{ borderTopColor: '#000', borderColor: 'rgba(0,0,0,0.2)' }} /> Saving…</>
              : 'Save changes'}
          </button>
        }
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* Provider picker */}
      <div>
        <div class="section-label">Provider</div>
        <div class="card ai-provider-setup-card ai-provider-setup-card--voice">
          <div class="ai-mode-grid" style={{ gridTemplateColumns: 'repeat(4, minmax(0, 1fr))' }}>
            {AUDIO_PROVIDERS.map((p) => (
              <button
                key={p.id}
                type="button"
                class={`card ai-mode-card${provider === p.id ? ' ai-mode-card-selected' : ''}`}
                onClick={() => void switchProvider(p.id)}
              >
                <span class="ai-mode-card-title">{p.label}</span>
                <span class="ai-mode-card-copy">{p.sublabel}</span>
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Voice picker — own card, always visible */}
      <div>
        <div class="section-label">Voice</div>
        <div class="card ai-provider-setup-card ai-provider-setup-card--no-anim">
          {voicesLoading ? (
            <div style={{ padding: '16px 20px' }}><span class="spinner spinner-sm" /></div>
          ) : voices.length === 0 ? (
            <div style={{ padding: '16px 20px', fontSize: '13px', color: 'var(--theme-text-secondary)' }}>No voices available</div>
          ) : (
            <div class="ai-mode-grid" style={{ gridTemplateColumns: showAllVoices ? 'repeat(auto-fill, minmax(min(130px, 100%), 1fr))' : `repeat(${displayedVoices.length + (hasMore ? 1 : 0)}, 1fr)` }}>
              {displayedVoices.map((v) => {
                const gated = !!v.modelGate && ttsModelValue !== v.modelGate
                return (
                  <button
                    key={v.id}
                    type="button"
                    class={`card ai-mode-card${selectedVoice === v.id ? ' ai-mode-card-selected' : ''}`}
                    style={{ padding: '8px 10px', minHeight: 'unset', opacity: gated ? 0.45 : 1, cursor: gated ? 'not-allowed' : 'pointer' }}
                    onClick={() => { if (!gated) selectVoice(v.id) }}
                    title={gated && v.modelGate ? `Requires model: ${v.modelGate}` : v.description}
                  >
                    <span class="ai-mode-card-title" style={{ fontSize: '12.5px' }}>
                      {v.label}
                      {gated && <span style={{ marginLeft: '4px', fontSize: '10px', opacity: 0.6 }}>locked</span>}
                    </span>
                    <span class="ai-mode-card-copy" style={{ fontSize: '11px' }}>{v.description}</span>
                  </button>
                )
              })}
              {hasMore && (
                <button
                  type="button"
                  class="card ai-mode-card"
                  style={{ padding: '8px 10px', minHeight: 'unset' }}
                  onClick={() => setShowAllVoices((s) => !s)}
                >
                  <span class="ai-mode-card-title" style={{ fontSize: '12.5px' }}>
                    {showAllVoices ? 'Show less' : `+${voices.length - VOICE_PAGE}`}
                  </span>
                  <span class="ai-mode-card-copy" style={{ fontSize: '11px' }}>
                    {showAllVoices ? '' : 'more voices'}
                  </span>
                </button>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Speech to text */}
      <SettingsGroup title="Speech to text">
        {provider === 'local' ? (
          <>
            <SettingsRow label="Engine" sublabel={`Whisper — active model: ${draft.voiceWhisperModel || 'ggml-base.en.bin'}`}>
              <button class="btn btn-sm" onClick={() => { window.location.hash = 'local-lm' }}>
                Manage models
              </button>
            </SettingsRow>
            <SettingsRow label="Language" sublabel="BCP-47 code for Whisper (e.g. en, fr). Leave empty for auto-detect." fieldId="local-whisper-lang">
              <input
                id="local-whisper-lang"
                class="input"
                placeholder="Auto-detect"
                value={draft.voiceWhisperLanguage ?? ''}
                onInput={(e) => update('voiceWhisperLanguage', (e.target as HTMLInputElement).value)}
              />
            </SettingsRow>
          </>
        ) : (
          <>
            <SettingsRow label="Model" sublabel="Model used to transcribe your voice input" fieldId="audio-stt-model">
              <select
                id="audio-stt-model"
                class="input"
                value={draft.audioSTTModel ?? ''}
                onChange={(e) => update('audioSTTModel', (e.target as HTMLSelectElement).value)}
              >
                <option value="">Auto (default)</option>
                {STT_MODELS[provider].map((m) => (
                  <option key={m.id} value={m.id}>{m.label}</option>
                ))}
              </select>
            </SettingsRow>
            <SettingsRow label="Language" sublabel="ISO-639-1 code (e.g. en, fr). Leave empty for auto-detection." fieldId="audio-stt-language">
              <input
                id="audio-stt-language"
                class="input"
                placeholder="Auto-detect"
                value={draft.audioSTTLanguage ?? ''}
                onInput={(e) => update('audioSTTLanguage', (e.target as HTMLInputElement).value)}
              />
            </SettingsRow>
          </>
        )}
      </SettingsGroup>

      {/* Text to speech */}
      <SettingsGroup title="Text to speech">
        {provider === 'local' && (
          <SettingsRow label="Engine" sublabel="Kokoro — on-device neural TTS">
            <button class="btn btn-sm" onClick={() => { window.location.hash = 'local-lm' }}>
              Manage models
            </button>
          </SettingsRow>
        )}
        {provider !== 'local' && (
          <>
            <SettingsRow label="Model" sublabel="Model used to generate speech" fieldId="audio-tts-model">
              <select
                id="audio-tts-model"
                class="input"
                value={ttsModelValue}
                onChange={(e) => update('audioTTSModel', (e.target as HTMLSelectElement).value)}
              >
                <option value="">Auto (default)</option>
                {TTS_MODELS[provider].map((m) => (
                  <option key={m.id} value={m.id}>{m.label}</option>
                ))}
              </select>
            </SettingsRow>
            <SettingsRow label="Speed" sublabel="Playback speed multiplier (0.5 – 2.0)" fieldId="audio-tts-speed">
              <input
                id="audio-tts-speed"
                class="input input-sm"
                type="number"
                min={0.5}
                max={2.0}
                step={0.1}
                value={draft.audioTTSSpeed || 1.0}
                onInput={(e) => update('audioTTSSpeed', parseFloat((e.target as HTMLInputElement).value))}
              />
            </SettingsRow>
            {showStylePrompt && (
              <SettingsRow label="Style prompt" sublabel="Describe how Atlas should sound (e.g. 'calm and clear narrator')" fieldId="audio-tts-style">
                <input
                  id="audio-tts-style"
                  class="input"
                  placeholder="Speak in a calm, helpful tone."
                  value={draft.audioTTSStylePrompt ?? ''}
                  onInput={(e) => update('audioTTSStylePrompt', (e.target as HTMLInputElement).value)}
                />
              </SettingsRow>
            )}
          </>
        )}
      </SettingsGroup>

      {/* API key — cloud providers */}
      {provider !== 'local' && (
        <SettingsGroup title="API key">
          <SettingsRow label="Credentials" sublabel="Your API key is read from the Keychain bundle">
            <button class="btn btn-sm" onClick={() => { window.location.hash = 'api-keys' }}>
              Manage API keys
            </button>
          </SettingsRow>
        </SettingsGroup>
      )}

    </div>
  )
}

function SettingsGroup({ title, children }: { title: string; children: preact.ComponentChildren }) {
  return (
    <div class="card settings-group" style={{ overflow: 'visible' }}>
      <div class="card-header"><span class="card-title">{title}</span></div>
      {children}
    </div>
  )
}

function SettingsRow({
  label,
  sublabel,
  fieldId,
  children,
}: {
  label: string
  sublabel?: string
  fieldId?: string
  children?: preact.ComponentChildren
}) {
  return (
    <div class="settings-row">
      <div class="settings-label-col">
        <div class="settings-label">
          {fieldId ? <label htmlFor={fieldId}>{label}</label> : label}
        </div>
        {sublabel && <div class="settings-sublabel">{sublabel}</div>}
      </div>
      {children && <div class="settings-field">{children}</div>}
    </div>
  )
}
