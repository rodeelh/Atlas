import { useState, useEffect, useRef } from 'preact/hooks'
import { api, RuntimeConfig, ModelSelectorInfo, AIModelRecord, APIKeyStatus } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import type { RuntimeConfigUpdateResponse } from '../api/client'
import { formatAtlasModelName } from '../modelName'

const ACTION_SAFETY_OPTIONS = ['always_ask_before_actions', 'ask_only_for_risky_actions', 'more_autonomous']
const SAFETY_LABELS: Record<string, string> = {
  always_ask_before_actions: 'Ask before all actions',
  ask_only_for_risky_actions: 'Ask only for risky actions',
  more_autonomous:            'More autonomous — auto-approve all',
}

const AI_PROVIDERS = [
  { id: 'openai',    label: 'OpenAI' },
  { id: 'anthropic', label: 'Claude (Anthropic)' },
  { id: 'gemini',    label: 'Gemini (Google)' },
  { id: 'lm_studio', label: 'LM Studio (Local)' },
  { id: 'ollama',         label: 'Ollama (Local)' },
  { id: 'atlas_engine',  label: 'Engine LM' },
] as const


export function Settings() {
  const [config, setConfig]       = useState<RuntimeConfig | null>(null)
  const [draft, setDraft]         = useState<RuntimeConfig | null>(null)
  const [loading, setLoading]     = useState(true)
  const [saving, setSaving]       = useState(false)
  const [error, setError]         = useState<string | null>(null)
  const [saved, setSaved]               = useState(false)
  const [restartRequired, setRestartRequired] = useState(false)
  const [models, setModels]             = useState<ModelSelectorInfo | null>(null)
  const [keyStatus, setKeyStatus]       = useState<APIKeyStatus | null>(null)
  const [showAdvanced, setShowAdvanced] = useState(false)

  // Location
  const [location, setLocation]         = useState<{ city: string; country: string; timezone: string; source: string } | null>(null)
  const [locationEdit, setLocationEdit] = useState('')
  const [locationSaving, setLocationSaving] = useState(false)
  const [locationError, setLocationError]   = useState<string | null>(null)

  // Preferences
  const [prefs, setPrefs]               = useState<{ temperatureUnit: string; currency: string; unitSystem: string } | null>(null)
  const [prefsSaving, setPrefsSaving]   = useState(false)

  useEffect(() => {
    const init = async () => {
      try {
        const [c, k] = await Promise.all([api.config(), api.apiKeys().catch(() => null)])
        setConfig(c); setDraft(c)
        if (k) setKeyStatus(k)
        // Fetch live model list for the active provider so dropdowns are populated on load
        api.modelsForProvider(c.activeAIProvider).then(setModels).catch(() => {})
        api.location().then(loc => { setLocation(loc); setLocationEdit(loc.city ? (loc.city + (loc.country ? ', ' + loc.country : '')) : '') }).catch(() => {})
        api.preferences().then(setPrefs).catch(() => {})
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load config.')
      } finally {
        setLoading(false)
      }
    }
    init()
  }, [])

  const handleProviderChange = async (provider: string) => {
    update('activeAIProvider', provider)
    // Clear stale model list immediately, then fetch for the new provider
    setModels(null)
    try {
      const m = await api.modelsForProvider(provider)
      setModels(m)
    } catch {
      // Non-fatal — models will show on next provider selection
    }
  }

  const update = <K extends keyof RuntimeConfig>(key: K, value: RuntimeConfig[K]) => {
    setDraft(prev => prev ? { ...prev, [key]: value } : prev)
    setSaved(false)
  }

  const save = async () => {
    if (!draft) return
    setSaving(true); setError(null); setSaved(false)
    try {
      const prevPrimaryModel = config?.selectedAtlasEngineModel
      const prevProvider     = config?.activeAIProvider
      const result: RuntimeConfigUpdateResponse = await api.updateConfig(draft)
      setConfig(result.config); setDraft(result.config)
      setSaved(true)
      setRestartRequired(result.restartRequired)
      // Reload live model list — provider may have changed
      api.modelsForProvider(result.config.activeAIProvider).then(setModels).catch(() => {})
      // Auto-load engine model when: model changed OR user just switched to atlas_engine
      if (result.config.activeAIProvider === 'atlas_engine') {
        const newModel        = basename(result.config.selectedAtlasEngineModel ?? '')
        const oldModel        = basename(prevPrimaryModel ?? '')
        const switchedToEngine = prevProvider !== 'atlas_engine'
        if (newModel && (newModel !== oldModel || switchedToEngine)) {
          api.engineStart(newModel).catch(() => {}) // non-fatal — user can load manually
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save config.')
    } finally {
      setSaving(false)
    }
  }

  const isDirty = (() => {
    if (!config || !draft) return false
    const keys = Object.keys(config) as (keyof RuntimeConfig)[]
    return keys.some(k => config[k] !== draft[k]) ||
      (Object.keys(draft) as (keyof RuntimeConfig)[]).some(k => !(k in config))
  })()

  if (loading) {
    return (
      <div class="screen">
        <PageHeader title="General" subtitle="Runtime configuration for the Atlas daemon" />
        <div style={{ display: 'flex', justifyContent: 'center', padding: '48px' }}><span class="spinner" /></div>
      </div>
    )
  }

  if (!draft) {
    return (
      <div class="screen">
        <PageHeader title="General" subtitle="Runtime configuration for the Atlas daemon" />
        <ErrorBanner error={error} />
      </div>
    )
  }

  return (
    <div class="screen">
      <PageHeader
        title="General"
        subtitle="Runtime configuration for the Atlas daemon"
        actions={
          <button class="btn btn-primary btn-sm" onClick={save} disabled={saving || !isDirty}>
            {saving
              ? <><span class="spinner spinner-sm" style={{ borderTopColor: '#000', borderColor: 'rgba(0,0,0,0.2)' }} /> Saving…</>
              : 'Save changes'}
          </button>
        }
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />
      {saved && !isDirty && !restartRequired && <div class="banner banner-success">Changes saved.</div>}
      {restartRequired && (
        <div class="banner" style={{ background: 'color-mix(in srgb, var(--yellow, #f59e0b) 15%, transparent)', borderColor: 'color-mix(in srgb, var(--yellow, #f59e0b) 40%, transparent)', color: 'var(--text)' }}>
          <strong>Restart required</strong> — Port change saved. Restart the Atlas daemon for it to take effect.
        </div>
      )}

      {/* Profile */}
      <SettingsGroup title="Profile">
        <SettingsRow label="Your name" sublabel="How Atlas addresses you in conversation">
          <input class="input" placeholder="e.g. Rami" value={draft.userName ?? ''}
            onInput={(e) => update('userName', (e.target as HTMLInputElement).value)} />
        </SettingsRow>
        <SettingsRow label="Assistant name" sublabel="How Atlas identifies itself in conversation">
          <input class="input" value={draft.personaName}
            onInput={(e) => update('personaName', (e.target as HTMLInputElement).value)} />
        </SettingsRow>
        <SettingsRow label="Location" sublabel="Leave blank to auto-detect from IP">
          <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', width: '100%' }}>
            <input
              class="input"
              placeholder={locationSaving ? 'Detecting…' : 'City, Country'}
              value={locationEdit}
              disabled={locationSaving}
              onInput={(e) => setLocationEdit((e.target as HTMLInputElement).value)}
              onBlur={async (e) => {
                const val = e.currentTarget.value.trim()
                setLocationError(null)
                setLocationSaving(true)
                try {
                  if (!val) {
                    const loc = await api.detectLocation()
                    setLocation(loc)
                    setLocationEdit(loc.city ? (loc.city + (loc.country ? ', ' + loc.country : '')) : '')
                  } else {
                    const parts = val.split(',').map((s: string) => s.trim())
                    const city = parts[0] ?? ''
                    const country = parts.slice(1).join(', ').trim()
                    const loc = await api.setLocation(city, country)
                    setLocation(loc)
                  }
                } catch (e) {
                  setLocationError(e instanceof Error ? e.message : 'Failed')
                } finally { setLocationSaving(false) }
              }}
            />
            {locationError && <div style={{ fontSize: '12px', color: 'var(--theme-text-danger, #e05252)' }}>{locationError}</div>}
          </div>
        </SettingsRow>
        {prefs && (
          <SettingsRow label="Units" sublabel="Sets measurement system and temperature scale">
            <select
              class="input"
              value={prefs.unitSystem}
              onChange={async (e) => {
                const v = (e.target as HTMLSelectElement).value
                const tempUnit = v === 'imperial' ? 'fahrenheit' : 'celsius'
                setPrefs(p => p ? { ...p, unitSystem: v, temperatureUnit: tempUnit } : p)
                setPrefsSaving(true)
                try { await api.setPreferences({ unitSystem: v, temperatureUnit: tempUnit }) } finally { setPrefsSaving(false) }
              }}
            >
              <option value="metric">Metric (km, km/h, °C)</option>
              <option value="imperial">Imperial (mi, mph, °F)</option>
            </select>
          </SettingsRow>
        )}
      </SettingsGroup>

      {/* Agent */}
      <SettingsGroup title="Agent">
        <SettingsRow label="AI Provider" sublabel="Provider used for all agent conversations">
          <select class="input" value={draft.activeAIProvider ?? 'openai'}
            onChange={(e) => handleProviderChange((e.target as HTMLSelectElement).value)}>
            {AI_PROVIDERS.map(p => (
              <option key={p.id} value={p.id}>{p.label}</option>
            ))}
          </select>
        </SettingsRow>

        {/* OpenAI */}
        {(draft.activeAIProvider ?? 'openai') === 'openai' && (
          <ModelPickerRows
            available={models?.availableModels ?? []}
            primaryValue={draft.selectedOpenAIPrimaryModel ?? ''}
            fastValue={draft.selectedOpenAIFastModel ?? ''}
            resolvedPrimary={models?.primaryModel}
            resolvedFast={models?.fastModel}
            onPrimaryChange={v => update('selectedOpenAIPrimaryModel', v)}
            onFastChange={v => update('selectedOpenAIFastModel', v)}
          />
        )}

        {/* Anthropic */}
        {(draft.activeAIProvider ?? 'openai') === 'anthropic' && (
          <ModelPickerRows
            available={models?.availableModels ?? []}
            primaryValue={draft.selectedAnthropicModel ?? ''}
            fastValue={draft.selectedAnthropicFastModel ?? ''}
            resolvedPrimary={models?.primaryModel}
            resolvedFast={models?.fastModel}
            onPrimaryChange={v => update('selectedAnthropicModel', v)}
            onFastChange={v => update('selectedAnthropicFastModel', v)}
          />
        )}

        {/* Gemini */}
        {(draft.activeAIProvider ?? 'openai') === 'gemini' && (
          <ModelPickerRows
            available={models?.availableModels ?? []}
            primaryValue={draft.selectedGeminiModel ?? ''}
            fastValue={draft.selectedGeminiFastModel ?? ''}
            resolvedPrimary={models?.primaryModel}
            resolvedFast={models?.fastModel}
            onPrimaryChange={v => update('selectedGeminiModel', v)}
            onFastChange={v => update('selectedGeminiFastModel', v)}
          />
        )}

        {/* LM Studio — server URL + primary model picker */}
        {(draft.activeAIProvider ?? 'openai') === 'lm_studio' && (
          <>
            <SettingsRow label="Server URL" sublabel="Local LM Studio server address">
              <input class="input" type="text" placeholder="http://localhost:1234"
                value={draft.lmStudioBaseURL ?? ''}
                onInput={(e) => update('lmStudioBaseURL', (e.target as HTMLInputElement).value)} />
            </SettingsRow>
            <SettingsRow label="Primary model" sublabel="Model loaded in LM Studio">
              <select class="input" value={draft.selectedLMStudioModel ?? ''}
                onChange={(e) => update('selectedLMStudioModel', (e.target as HTMLSelectElement).value)}>
                <option value="">{models?.primaryModel ? `Auto — ${models.primaryModel}` : 'Auto (not yet resolved)'}</option>
                {(models?.availableModels ?? []).map(m => (
                  <option key={m.id} value={m.id}>{m.displayName}</option>
                ))}
              </select>
            </SettingsRow>
          </>
        )}

        {/* Ollama — server URL + primary model picker */}
        {(draft.activeAIProvider ?? 'openai') === 'ollama' && (
          <>
            <SettingsRow label="Server URL" sublabel="Local Ollama server address">
              <input class="input" type="text" placeholder="http://localhost:11434"
                value={draft.ollamaBaseURL ?? ''}
                onInput={(e) => update('ollamaBaseURL', (e.target as HTMLInputElement).value)} />
            </SettingsRow>
            <SettingsRow label="Primary model" sublabel="Model loaded in Ollama">
              <select class="input" value={draft.selectedOllamaModel ?? ''}
                onChange={(e) => update('selectedOllamaModel', (e.target as HTMLSelectElement).value)}>
                <option value="">{models?.primaryModel ? `Auto — ${models.primaryModel}` : 'Auto (not yet resolved)'}</option>
                {(models?.availableModels ?? []).map(m => (
                  <option key={m.id} value={m.id}>{m.displayName}</option>
                ))}
              </select>
            </SettingsRow>
          </>
        )}

        {/* Engine LM — primary/fast model pickers (server managed by Atlas) */}
        {(draft.activeAIProvider ?? 'openai') === 'atlas_engine' && (
          <ModelPickerRows
            available={(models?.availableModels ?? []).map(m => ({ ...m, displayName: formatAtlasModelName(m.displayName) }))}
            primaryValue={basename(draft.selectedAtlasEngineModel ?? '')}
            fastValue={basename(draft.selectedAtlasEngineModelFast ?? '')}
            resolvedPrimary={models?.primaryModel}
            resolvedFast={models?.fastModel}
            primaryPlaceholder="No model loaded — start Engine LM first"
            fastPlaceholder="Falls back to primary model"
            onPrimaryChange={v => update('selectedAtlasEngineModel', v)}
            onFastChange={v => update('selectedAtlasEngineModelFast', v)}
          />
        )}

        {/* Advanced subsection */}
        <div style={{ borderTop: '1px solid var(--border)' }}>
          <button
            onClick={() => setShowAdvanced(v => !v)}
            style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%', padding: '10px 20px', background: 'none', border: 'none', cursor: 'pointer', textAlign: 'left' }}
          >
            <span style={{ fontSize: '11.5px', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.6px', color: 'var(--theme-text-muted, var(--text-3))' }}>Advanced</span>
            <span style={{ fontSize: '13px', color: 'var(--text-3)', transform: showAdvanced ? 'rotate(90deg)' : 'rotate(0deg)', transition: 'transform 0.15s', display: 'inline-block', lineHeight: 1 }}>›</span>
          </button>
          {showAdvanced && (
            <div class="settings-advanced-content">
              {(draft.activeAIProvider ?? 'openai') === 'openai' && (
                <ProviderKeyRow
                  providerID="openai"
                  sublabel="API key for OpenAI models (GPT-4.1 etc.)"
                  configured={keyStatus?.openAIKeySet ?? false}
                  onSaved={setKeyStatus}
                />
              )}
              {(draft.activeAIProvider ?? 'openai') === 'anthropic' && (
                <ProviderKeyRow
                  providerID="anthropic"
                  sublabel="API key for Claude models (Sonnet, Opus, Haiku)"
                  configured={keyStatus?.anthropicKeySet ?? false}
                  onSaved={setKeyStatus}
                />
              )}
              {(draft.activeAIProvider ?? 'openai') === 'gemini' && (
                <ProviderKeyRow
                  providerID="gemini"
                  sublabel="API key for Gemini models (Flash, Pro etc.)"
                  configured={keyStatus?.geminiKeySet ?? false}
                  onSaved={setKeyStatus}
                />
              )}
              {(draft.activeAIProvider ?? 'openai') === 'lm_studio' && (
                <ProviderKeyRow
                  providerID="lm_studio"
                  sublabel="Optional Bearer token for LM Studio v0.4.8+ authentication"
                  configured={keyStatus?.lmStudioKeySet ?? false}
                  onSaved={setKeyStatus}
                />
              )}
              {(draft.activeAIProvider ?? 'openai') === 'ollama' && (
                <ProviderKeyRow
                  providerID="ollama"
                  sublabel="Optional Bearer token when Ollama is running behind a reverse proxy with auth"
                  configured={keyStatus?.ollamaKeySet ?? false}
                  onSaved={setKeyStatus}
                />
              )}
              {(draft.activeAIProvider ?? 'openai') === 'atlas_engine' && (
                <SettingsRow
                  label="Server Port"
                  sublabel="Port Engine LM listens on (managed by Atlas)"
                  hint="Default is 11985. Change only if that port is in use. Engine LM must be restarted after changing."
                >
                  <input class="input input-sm" type="number" min={1024} max={65535}
                    value={draft.atlasEnginePort ?? 11985}
                    onInput={(e) => update('atlasEnginePort', Number((e.target as HTMLInputElement).value))} />
                </SettingsRow>
              )}
              {((draft.activeAIProvider ?? 'openai') === 'lm_studio' || (draft.activeAIProvider ?? 'openai') === 'ollama' || (draft.activeAIProvider ?? 'openai') === 'atlas_engine') ? (
                <>
                  <SettingsRow
                    label="Max Iterations"
                    sublabel="Agent loop iterations per turn"
                    hint="Keep at 2 for local models — each iteration is slow on local hardware. Cloud providers can handle 3–5 comfortably."
                  >
                    {(draft.activeAIProvider ?? 'openai') === 'lm_studio' ? (
                      <input class="input input-sm" type="number" min={1} max={20}
                        value={draft.lmStudioMaxAgentIterations ?? 2}
                        onInput={(e) => update('lmStudioMaxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                    ) : (draft.activeAIProvider ?? 'openai') === 'atlas_engine' ? (
                      <input class="input input-sm" type="number" min={1} max={20}
                        value={draft.atlasEngineMaxAgentIterations ?? 2}
                        onInput={(e) => update('atlasEngineMaxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                    ) : (
                      <input class="input input-sm" type="number" min={1} max={20}
                        value={draft.ollamaMaxAgentIterations ?? 2}
                        onInput={(e) => update('ollamaMaxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                    )}
                  </SettingsRow>
                  <SettingsRow
                    label="Context Window"
                    sublabel="Messages from history sent per request (0 = unlimited)"
                    hint="Keep at 10 for local models — lower context means faster prefill. Cloud providers can handle 20–50 without a speed penalty."
                  >
                    {(draft.activeAIProvider ?? 'openai') === 'lm_studio' ? (
                      <input class="input input-sm" type="number" min={0} max={100}
                        value={draft.lmStudioContextWindowLimit ?? 10}
                        onInput={(e) => update('lmStudioContextWindowLimit', Number((e.target as HTMLInputElement).value))} />
                    ) : (draft.activeAIProvider ?? 'openai') === 'atlas_engine' ? (
                      <input class="input input-sm" type="number" min={0} max={100}
                        value={draft.atlasEngineContextWindowLimit ?? 10}
                        onInput={(e) => update('atlasEngineContextWindowLimit', Number((e.target as HTMLInputElement).value))} />
                    ) : (
                      <input class="input input-sm" type="number" min={0} max={100}
                        value={draft.ollamaContextWindowLimit ?? 10}
                        onInput={(e) => update('ollamaContextWindowLimit', Number((e.target as HTMLInputElement).value))} />
                    )}
                  </SettingsRow>
                </>
              ) : (
                <>
                  <SettingsRow
                    label="Max Iterations"
                    sublabel="Agent loop iterations per turn"
                    hint="3 works well for cloud providers. Raise to 5 for complex multi-step tasks; lower to 1–2 to reduce cost per turn."
                  >
                    <input class="input input-sm" type="number" min={1} max={20}
                      value={draft.maxAgentIterations}
                      onInput={(e) => update('maxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                  </SettingsRow>
                  <SettingsRow
                    label="Context Window"
                    sublabel="Messages from history sent per request (0 = unlimited)"
                    hint="20 is a good default for cloud providers. Lower to reduce cost; set to 0 for unlimited (may increase latency on long conversations)."
                  >
                    <input class="input input-sm" type="number" min={0} max={100}
                      value={draft.conversationWindowLimit}
                      onInput={(e) => update('conversationWindowLimit', Number((e.target as HTMLInputElement).value))} />
                  </SettingsRow>
                </>
              )}
            </div>
          )}
        </div>
        <SettingsRow
          label="Tool Selection"
          sublabel="Controls which tools Atlas makes available to the model each turn"
          hint={"Smart: fast, low tokens.\nKeywords: topic pre-matched.\nAI Router: precise, needs Engine LM.\nOff: all tools, always."}
        >
          <select class="input input"
            value={draft.toolSelectionMode ?? 'lazy'}
            onChange={(e) => update('toolSelectionMode', (e.target as HTMLSelectElement).value)}>
            <option value="lazy">Smart (default)</option>
            <option value="heuristic">Keywords</option>
            <option value="llm">AI Router</option>
            <option value="off">Off</option>
          </select>
        </SettingsRow>
        <SettingsRow
          label="Local model for all background tasks"
          sublabel="Use Engine LM router for memory extraction, reflection, and dream cycle"
          hint="Not advised — memory extraction and reflection quality may be lower with a small local model. Only enable if you want to avoid all cloud API calls."
        >
          <ToggleField
            checked={draft.atlasEngineRouterForAll ?? false}
            onChange={v => update('atlasEngineRouterForAll', v)}
          />
        </SettingsRow>
        <SettingsRow label="Memory Enabled" sublabel="Extract and persist facts from conversations">
          <ToggleField checked={draft.memoryEnabled} onChange={v => update('memoryEnabled', v)} />
        </SettingsRow>
        <SettingsRow label="Max per Turn" sublabel="Memories injected as context per request">
          <input class="input input-sm" type="number" min={0} max={20}
            value={draft.maxRetrievedMemoriesPerTurn}
            onInput={(e) => update('maxRetrievedMemoriesPerTurn', Number((e.target as HTMLInputElement).value))} />
        </SettingsRow>
        <SettingsRow label="Action Safety" sublabel="When Atlas asks before taking action">
          <select class="input input" value={draft.actionSafetyMode}
            onChange={(e) => update('actionSafetyMode', (e.target as HTMLSelectElement).value)}>
            {ACTION_SAFETY_OPTIONS.map(o => (
              <option key={o} value={o}>{SAFETY_LABELS[o]}</option>
            ))}
          </select>
        </SettingsRow>
      </SettingsGroup>

      {/* Access */}
      <SettingsGroup title="Access">
        <RemoteAccessSection
          enabled={draft.remoteAccessEnabled}
          tailscaleEnabled={draft.tailscaleEnabled}
          onToggle={async v => {
            update('remoteAccessEnabled', v)
            try {
              const result = await api.updateConfig({ ...(draft ?? config ?? {}), remoteAccessEnabled: v })
              setConfig(result.config)
              setDraft(result.config)
              setRestartRequired(result.restartRequired)
              setSaved(true)
            } catch (err) {
              update('remoteAccessEnabled', !v)
              setError(err instanceof Error ? err.message : 'Failed to update remote access.')
            }
          }}
          onTailscaleToggle={async v => {
            update('tailscaleEnabled', v)
            try {
              const result = await api.updateConfig({ ...(draft ?? config ?? {}), tailscaleEnabled: v })
              setConfig(result.config)
              setDraft(result.config)
              setRestartRequired(result.restartRequired)
              setSaved(true)
            } catch (err) {
              update('tailscaleEnabled', !v)
              setError(err instanceof Error ? err.message : 'Failed to update Tailscale setting.')
            }
          }}
        />
      </SettingsGroup>

    </div>
  )
}


function SettingsGroup({ title, children }: { title: string; children: preact.ComponentChild }) {
  return (
    <div>
      <div class="section-label">{title}</div>
      <div class="card settings-group">{children}</div>
    </div>
  )
}

const CopyIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <rect x="5" y="5" width="9" height="9" rx="1.5" />
    <path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5" />
  </svg>
)

const CheckIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M3 8l4 4 6-7" />
  </svg>
)

function RemoteAccessSection({
  enabled, tailscaleEnabled, onToggle, onTailscaleToggle,
}: {
  enabled: boolean
  tailscaleEnabled: boolean
  onToggle: (v: boolean) => void
  onTailscaleToggle: (v: boolean) => void
}) {
  const [status, setStatus] = useState<{
    lanIP: string | null; accessURL: string | null
    tailscaleIP: string | null; tailscaleURL: string | null; tailscaleConnected: boolean
  } | null>(null)
  const [accessToken, setAccessToken] = useState<string | null>(null)
  const [tokenCopied, setTokenCopied] = useState(false)
  const [tokenHovered, setTokenHovered] = useState(false)
  const [tailscaleCopied, setTailscaleCopied] = useState(false)
  const [tailscaleHovered, setTailscaleHovered] = useState(false)
  const [revoking, setRevoking] = useState(false)
  const [revoked, setRevoked] = useState(false)

  useEffect(() => {
    api.remoteAccessStatus().then(s => setStatus(s)).catch(() => {})
    if (enabled) api.remoteAccessKey().then(r => setAccessToken(r.key)).catch(() => {})
    if (!enabled && !tailscaleEnabled) return
    const interval = setInterval(() => {
      api.remoteAccessStatus().then(s => setStatus(s)).catch(() => {})
    }, 4000)
    return () => clearInterval(interval)
  }, [enabled, tailscaleEnabled])

  const revokeAll = async () => {
    setRevoking(true)
    try {
      await api.revokeRemoteSessions()
      const r = await api.remoteAccessKey()
      setAccessToken(r.key)

      setRevoked(true)
      setTimeout(() => setRevoked(false), 3000)
    } finally {
      setRevoking(false)
    }
  }

  const copyToken = async () => {
    if (!accessToken) return
    await navigator.clipboard.writeText(accessToken)
    setTokenCopied(true)
    setTimeout(() => setTokenCopied(false), 1800)
  }

  const copyTailscale = async (url: string) => {
    await navigator.clipboard.writeText(url)
    setTailscaleCopied(true)
    setTimeout(() => setTailscaleCopied(false), 1800)
  }

  const codeStyle: preact.JSX.CSSProperties = {
    fontSize: '12px', background: 'var(--bg)', padding: '6px 10px', borderRadius: '8px',
    border: '1px solid var(--border)', whiteSpace: 'nowrap', width: '240px',
    display: 'inline-block', overflow: 'hidden', textOverflow: 'ellipsis', boxSizing: 'border-box',
  }

  return (
    <>
      <SettingsRow label="LAN Access" sublabel="Allow browsers on your local network to connect.">
        <ToggleField checked={enabled} onChange={onToggle} />
      </SettingsRow>
      {enabled && (
        <SettingsRow label="Local address" sublabel="Open this URL on any device on your network">
          {status?.accessURL
            ? <code style={{ ...codeStyle, userSelect: 'all' }}>{status.accessURL}</code>
            : <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Detecting…</span>
          }
        </SettingsRow>
      )}
      {enabled && (
        <SettingsRow label="Access key" sublabel="Enter this key when connecting from another device">
          {accessToken
            ? <div
                style={{ position: 'relative', display: 'inline-flex', alignItems: 'center' }}
                onMouseEnter={() => setTokenHovered(true)}
                onMouseLeave={() => setTokenHovered(false)}
              >
                <code style={{ ...codeStyle, fontFamily: 'ui-monospace, monospace', userSelect: 'all', paddingRight: '28px' }}>
                  {accessToken}
                </code>
                <button
                  class={`chat-copy-btn${tokenCopied ? ' copied' : ''}`}
                  style={{ position: 'absolute', right: '4px', opacity: tokenHovered || tokenCopied ? 1 : 0, pointerEvents: tokenHovered || tokenCopied ? 'auto' : 'none', transition: 'opacity 0.12s' }}
                  onClick={copyToken} title="Copy" aria-label="Copy access key"
                >
                  {tokenCopied ? <CheckIcon /> : <CopyIcon />}
                </button>
              </div>
            : <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Loading…</span>
          }
        </SettingsRow>
      )}
      {enabled && (
        <SettingsRow label="Revoke sessions" sublabel="Sign out all remote devices immediately">
          <button class="btn btn-sm" onClick={revokeAll} disabled={revoking}>
            {revoked ? 'Revoked' : revoking ? 'Revoking…' : 'Revoke all'}
          </button>
        </SettingsRow>
      )}
      <SettingsRow
        label="Tailscale Access"
        sublabel="Allow devices on your Tailscale network to connect. No access key required."
        hint="Every device on a Tailscale network is cryptographically enrolled by the account owner — network membership is the authentication. Tailscale must be installed and running on both devices."
      >
        <ToggleField checked={tailscaleEnabled} onChange={onTailscaleToggle} />
      </SettingsRow>
      {tailscaleEnabled && (
        <SettingsRow label="Tailscale address" sublabel="Open directly on any device in your Tailscale network">
          {status?.tailscaleConnected && status.tailscaleURL
            ? <div
                style={{ position: 'relative', display: 'inline-flex', alignItems: 'center' }}
                onMouseEnter={() => setTailscaleHovered(true)}
                onMouseLeave={() => setTailscaleHovered(false)}
              >
                <code style={{ ...codeStyle, userSelect: 'all', paddingRight: '28px' }}>{status.tailscaleURL}</code>
                <button
                  class={`chat-copy-btn${tailscaleCopied ? ' copied' : ''}`}
                  style={{ position: 'absolute', right: '4px', opacity: tailscaleHovered || tailscaleCopied ? 1 : 0, pointerEvents: tailscaleHovered || tailscaleCopied ? 'auto' : 'none', transition: 'opacity 0.12s' }}
                  onClick={() => copyTailscale(status.tailscaleURL!)} title="Copy" aria-label="Copy Tailscale address"
                >
                  {tailscaleCopied ? <CheckIcon /> : <CopyIcon />}
                </button>
              </div>
            : status !== null
              ? <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Tailscale not detected — is it running?</span>
              : <span style={{ fontSize: '12px', color: 'var(--text-3)' }}>Detecting…</span>
          }
        </SettingsRow>
      )}
    </>
  )
}

function SettingsRow({ label, sublabel, hint, children }: { label: string; sublabel?: string; hint?: string; children: preact.ComponentChild }) {
  return (
    <div class="settings-row">
      <div class="settings-label-col">
        <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
          {label}
          {hint && <InfoTip text={hint} />}
        </div>
        {sublabel && <div class="settings-sublabel">{sublabel}</div>}
      </div>
      <div class="settings-field">{children}</div>
    </div>
  )
}

function InfoTip({ text }: { text: string }) {
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)
  const btnRef = useRef<HTMLButtonElement>(null)

  const show = () => {
    if (!btnRef.current) return
    const r = btnRef.current.getBoundingClientRect()
    setPos({ top: r.top + r.height / 2, left: r.right + 8 })
  }

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center' }}>
      <button
        ref={btnRef}
        style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: '15px', height: '15px', borderRadius: '50%', background: 'var(--text-3)', color: 'var(--bg)', fontSize: '9px', fontWeight: 700, border: 'none', cursor: 'pointer', flexShrink: 0, lineHeight: 1 }}
        onMouseEnter={show}
        onMouseLeave={() => setPos(null)}
      >?</button>
      {pos && (
        <span style={{ position: 'fixed', top: pos.top, left: pos.left, transform: 'translateY(-50%)', background: 'var(--surface, var(--bg))', border: '1px solid var(--border)', borderRadius: '8px', padding: '8px 11px', fontSize: '12px', color: 'var(--text-2)', width: '260px', zIndex: 9999, lineHeight: 1.5, boxShadow: '0 4px 20px rgba(0,0,0,0.22)', pointerEvents: 'none' }}>
          {text.split('\n').map((line, i) => <span key={i} style={{ display: 'block' }}>{line}</span>)}
        </span>
      )}
    </span>
  )
}

function ToggleField({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label class="toggle">
      <input type="checkbox" checked={checked} onChange={(e) => onChange((e.target as HTMLInputElement).checked)} />
      <span class="toggle-track" />
    </label>
  )
}

// basename strips path components from a model value — Engine LM stores
// full paths in config; we display and save only the filename.
const basename = (p: string) => (p && p.includes('/')) ? (p.split('/').pop() ?? p) : p

// Shared model picker rows used by all three cloud providers (OpenAI, Anthropic, Gemini).
// Shows two dropdowns — Primary and Fast — each with "Auto (resolved)" as the first option,
// followed by all available models fetched live from the provider API.
function ModelPickerRows({
  available, primaryValue, fastValue, resolvedPrimary, resolvedFast,
  primaryPlaceholder, fastPlaceholder,
  onPrimaryChange, onFastChange,
}: {
  available: AIModelRecord[]
  primaryValue: string
  fastValue: string
  resolvedPrimary?: string
  resolvedFast?: string
  primaryPlaceholder?: string
  fastPlaceholder?: string
  onPrimaryChange: (v: string) => void
  onFastChange: (v: string) => void
}) {
  const autoLabel = (resolved?: string, placeholder?: string) =>
    placeholder ?? (resolved ? `Auto — ${resolved}` : 'Auto (not yet resolved)')

  return (
    <>
      <SettingsRow label="Primary model" sublabel="Used for all agent turns">
        <select class="input" value={primaryValue}
          onChange={(e) => onPrimaryChange((e.target as HTMLSelectElement).value)}>
          <option value="">{autoLabel(resolvedPrimary, primaryPlaceholder)}</option>
          {available.filter(m => !m.isFast).map(m => (
            <option key={m.id} value={m.id}>{m.displayName}</option>
          ))}
          {available.filter(m => m.isFast).length > 0 && (
            <>
              <option disabled>── Fast models ──</option>
              {available.filter(m => m.isFast).map(m => (
                <option key={m.id} value={m.id}>{m.displayName}</option>
              ))}
            </>
          )}
        </select>
      </SettingsRow>
      <SettingsRow label="Fast model" sublabel="Used for background tasks like reflection">
        <select class="input" value={fastValue}
          onChange={(e) => onFastChange((e.target as HTMLSelectElement).value)}>
          <option value="">{autoLabel(resolvedFast, fastPlaceholder)}</option>
          {available.filter(m => m.isFast).map(m => (
            <option key={m.id} value={m.id}>{m.displayName}</option>
          ))}
          {available.filter(m => !m.isFast).length > 0 && (
            <>
              <option disabled>── Primary models ──</option>
              {available.filter(m => !m.isFast).map(m => (
                <option key={m.id} value={m.id}>{m.displayName}</option>
              ))}
            </>
          )}
        </select>
      </SettingsRow>
    </>
  )
}

function ProviderKeyRow({ providerID, sublabel, configured, onSaved }: {
  providerID: string
  sublabel: string
  configured: boolean
  onSaved: (u: APIKeyStatus) => void
}) {
  const [editing, setEditing] = useState(false)
  const [value, setValue]     = useState('')
  const [saving, setSaving]   = useState(false)
  const [err, setErr]         = useState<string | null>(null)

  const save = async () => {
    if (!value.trim()) return
    setSaving(true); setErr(null)
    try {
      const updated = await api.setAPIKey(providerID, value.trim())
      onSaved(updated); setValue(''); setEditing(false)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to save.')
    } finally { setSaving(false) }
  }

  return (
    <div>
      <div class="settings-row" style={{ borderBottom: 'none' }}>
        <div class="settings-label-col">
          <div class="settings-label">API Key</div>
          <div class="settings-sublabel">{sublabel}</div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: '5px', fontSize: '12.5px', fontWeight: 500, color: configured ? 'var(--green)' : 'var(--text-3)' }}>
            <span style={{ width: '7px', height: '7px', borderRadius: '50%', flexShrink: 0, backgroundColor: configured ? 'var(--green)' : 'var(--text-3)' }} />
            {configured ? 'Configured' : 'Not set'}
          </span>
          <button class="btn btn-sm" onClick={() => { setEditing(v => !v); setErr(null) }}>
            {configured ? 'Change' : 'Add'}
          </button>
        </div>
      </div>
      {editing && (
        <div style={{ padding: '0 20px 14px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
          <input
            class="input"
            type="password"
            placeholder="Paste API key…"
            value={value}
            onInput={e => setValue((e.target as HTMLInputElement).value)}
            onKeyDown={e => { if (e.key === 'Enter') save(); if (e.key === 'Escape') { setEditing(false); setValue(''); setErr(null) } }}
            autoFocus
          />
          {err && <div style={{ fontSize: '12px', color: 'var(--red)' }}>{err}</div>}
          <div style={{ display: 'flex', gap: '6px' }}>
            <button class="btn btn-sm btn-primary" onClick={save} disabled={saving || !value.trim()}>
              {saving ? <span class="spinner spinner-sm" style={{ borderTopColor: '#000', borderColor: 'rgba(0,0,0,0.2)' }} /> : 'Save'}
            </button>
            <button class="btn btn-sm" onClick={() => { setEditing(false); setValue(''); setErr(null) }} disabled={saving}>Cancel</button>
          </div>
        </div>
      )}
    </div>
  )
}
