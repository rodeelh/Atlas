import { useState, useEffect, useRef } from 'preact/hooks'
import { api, RuntimeConfig, ModelSelectorInfo, AIModelRecord, APIKeyStatus, CloudModelHealth } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import type { RuntimeConfigUpdateResponse } from '../api/client'
import { formatAtlasModelName } from '../modelName'

const ACTION_SAFETY_OPTIONS = ['always_ask_before_actions', 'ask_only_for_risky_actions', 'more_autonomous']
const SAFETY_LABELS: Record<string, string> = {
  always_ask_before_actions: 'Ask every time',
  ask_only_for_risky_actions: 'Ask for risky actions',
  more_autonomous:            'Auto-approve actions',
}

const CLOUD_PROVIDERS = [
  { id: 'openai',    label: 'OpenAI' },
  { id: 'anthropic', label: 'Claude (Anthropic)' },
  { id: 'gemini',    label: 'Gemini (Google)' },
  { id: 'openrouter', label: 'OpenRouter' },
] as const

const LOCAL_BACKENDS = [
  { id: 'atlas_engine', label: 'Atlas Engine' },
  { id: 'ollama', label: 'Ollama' },
  { id: 'lm_studio', label: 'LM Studio' },
] as const

type CloudProviderID = typeof CLOUD_PROVIDERS[number]['id']
type LocalBackendID = typeof LOCAL_BACKENDS[number]['id']
const OPENROUTER_SHOW_MORE_VALUE = '__openrouter_show_more__'

const CLOUD_PROVIDER_IDS = new Set<string>(CLOUD_PROVIDERS.map(p => p.id))
const LOCAL_PROVIDER_IDS = new Set<string>(LOCAL_BACKENDS.map(p => p.id))
const BADGE_STYLE = { fontSize: '11px', padding: '2px 8px' } as const


export function AIProviders() {
  const [config, setConfig]       = useState<RuntimeConfig | null>(null)
  const [draft, setDraft]         = useState<RuntimeConfig | null>(null)
  const [loading, setLoading]     = useState(true)
  const [saving, setSaving]       = useState(false)
  const [error, setError]         = useState<string | null>(null)
  const [saved, setSaved]               = useState(false)
  const [restartRequired, setRestartRequired] = useState(false)
  const [cloudModels, setCloudModels]   = useState<ModelSelectorInfo | null>(null)
  const [localModels, setLocalModels]   = useState<ModelSelectorInfo | null>(null)
  const [keyStatus, setKeyStatus]       = useState<APIKeyStatus | null>(null)
  const [cloudModelHealth, setCloudModelHealth] = useState<CloudModelHealth | null>(null)
  const [checkingCloudModelHealth, setCheckingCloudModelHealth] = useState(false)
  const [openRouterLimit, setOpenRouterLimit] = useState(25)
  const [cloudProvider, setCloudProvider] = useState<CloudProviderID>('openai')
  const [localBackend, setLocalBackend] = useState<LocalBackendID>('atlas_engine')

  useEffect(() => {
    const init = async () => {
      try {
        const [c, k] = await Promise.all([api.config(), api.apiKeys().catch(() => null)])
        setConfig(c); setDraft(c)
        if (k) setKeyStatus(k)
        const active = c.activeAIProvider ?? 'openai'
        const initialCloud: CloudProviderID = CLOUD_PROVIDER_IDS.has(active) ? (active as CloudProviderID) : 'openai'
        const initialLocal: LocalBackendID = LOCAL_PROVIDER_IDS.has(active) ? (active as LocalBackendID) : 'atlas_engine'
        setCloudProvider(initialCloud)
        setLocalBackend(initialLocal)
        fetchCloudModels(initialCloud, true)
        fetchLocalModels(initialLocal, true)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load config.')
      } finally {
        setLoading(false)
      }
    }
    init()
  }, [])

  useEffect(() => {
    if (cloudProvider !== 'openrouter') return
    void fetchCloudModels('openrouter', true)
  }, [openRouterLimit])

  const fetchCloudModels = async (provider: CloudProviderID, refresh: boolean) => {
    setCloudModels(null)
    try {
      const m = provider === 'openrouter'
        ? await api.openRouterModels(refresh, openRouterLimit)
        : await api.modelsForProvider(provider, refresh)
      setCloudModels(m)
    } catch {
      setCloudModels({ primaryModel: '', fastModel: '', availableModels: [], lastRefreshedAt: '' })
    }
  }

  const fetchLocalModels = async (backend: LocalBackendID, _refresh: boolean) => {
    setLocalModels(null)
    try {
      const m = await api.modelsForProvider(backend, _refresh)
      setLocalModels(m)
    } catch {
      setLocalModels({ primaryModel: '', fastModel: '', availableModels: [], lastRefreshedAt: '' })
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
      fetchCloudModels(cloudProvider, true)
      fetchLocalModels(localBackend, true)
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

  const activeProvider = draft?.activeAIProvider ?? 'openai'
  const supportiveMode = CLOUD_PROVIDER_IDS.has(activeProvider)
  const localOnline = (localModels?.availableModels?.length ?? 0) > 0

  const cloudKeyConfigured = (() => {
    switch (cloudProvider) {
      case 'openai': return keyStatus?.openAIKeySet ?? false
      case 'anthropic': return keyStatus?.anthropicKeySet ?? false
      case 'gemini': return keyStatus?.geminiKeySet ?? false
      case 'openrouter': return keyStatus?.openRouterKeySet ?? false
      default: return false
    }
  })()

  const goToCredentials = () => {
    window.location.hash = 'api-keys'
  }

  const cloudPrimaryValue = (() => {
    switch (cloudProvider) {
      case 'anthropic': return draft?.selectedAnthropicModel ?? ''
      case 'gemini': return draft?.selectedGeminiModel ?? ''
      case 'openrouter': return draft?.selectedOpenRouterModel ?? ''
      default: return draft?.selectedOpenAIPrimaryModel ?? ''
    }
  })()

  const cloudAutoLabel = (() => {
    if (cloudProvider === 'openrouter') {
      const resolved = cloudModels?.primaryModel || draft?.selectedOpenRouterModel || 'openrouter/auto:free'
      if (resolved === 'openrouter/auto:free') return 'Auto — Free Models Router'
      if (resolved === 'openrouter/auto') return 'Auto — OpenRouter Auto Router'
      return `Auto — ${resolved}`
    }
    return cloudModels?.primaryModel ? `Auto — ${cloudModels.primaryModel}` : 'Auto (not yet resolved)'
  })()

  const setCloudPrimaryValue = (v: string) => {
    switch (cloudProvider) {
      case 'anthropic': update('selectedAnthropicModel', v); break
      case 'gemini': update('selectedGeminiModel', v); break
      case 'openrouter': update('selectedOpenRouterModel', v); break
      default: update('selectedOpenAIPrimaryModel', v); break
    }
  }

  useEffect(() => {
    const modelToCheck = (cloudPrimaryValue || cloudModels?.primaryModel || (cloudProvider === 'openrouter' ? 'openrouter/auto:free' : '')).trim()
    if (!modelToCheck) {
      setCloudModelHealth(null)
      setCheckingCloudModelHealth(false)
      return
    }
    let cancelled = false
    setCheckingCloudModelHealth(true)
    api.cloudModelHealth(cloudProvider, modelToCheck)
      .then((health) => { if (!cancelled) setCloudModelHealth(health) })
      .catch(() => {
        if (!cancelled) {
          setCloudModelHealth({
            status: 'unavailable',
            message: 'Could not check model availability.',
            checkedAt: new Date().toISOString(),
          })
        }
      })
      .finally(() => { if (!cancelled) setCheckingCloudModelHealth(false) })
    return () => { cancelled = true }
  }, [cloudProvider, cloudPrimaryValue, cloudModels?.primaryModel])

  const cloudModelStatusBadge = (() => {
    if (checkingCloudModelHealth) {
      return <span class="badge" style={BADGE_STYLE} title="Running model health check">Checking…</span>
    }
    if (!cloudModelHealth) return null
    const status = cloudModelHealth.status
    const title = cloudModelHealth.message || `Status: ${status}`
    if (status === 'ok') {
      return <span class="badge badge-green" style={BADGE_STYLE} title={title}>Available</span>
    }
    if (status === 'rate_limited') {
      return <span class="badge badge-yellow" style={BADGE_STYLE} title={title}>Rate limited</span>
    }
    if (status === 'missing_key') {
      return <span class="badge badge-red" style={BADGE_STYLE} title={title}>Missing API key</span>
    }
    if (status === 'warning') {
      return <span class="badge badge-yellow" style={BADGE_STYLE} title={title}>Warning</span>
    }
    if (status === 'unavailable') {
      return <span class="badge badge-red" style={BADGE_STYLE} title={title}>Unavailable</span>
    }
    if (status === 'unknown') {
      return <span class="badge" style={BADGE_STYLE} title={title}>Unknown</span>
    }
    return <span class="badge" style={BADGE_STYLE} title={title}>{status}</span>
  })()

  const localModelValue = (() => {
    switch (localBackend) {
      case 'ollama': return draft?.selectedOllamaModel ?? ''
      case 'lm_studio': return draft?.selectedLMStudioModel ?? ''
      default: return basename(draft?.selectedAtlasEngineModel ?? '')
    }
  })()

  const setLocalModelValue = (v: string) => {
    switch (localBackend) {
      case 'ollama': update('selectedOllamaModel', v); break
      case 'lm_studio': update('selectedLMStudioModel', v); break
      default: update('selectedAtlasEngineModel', v); break
    }
  }

  if (loading) {
    return (
      <div class="screen ai-providers-screen">
        <PageHeader title="AI Providers" subtitle="Configure cloud and local models" />
        <div style={{ display: 'flex', justifyContent: 'center', padding: '48px' }}><span class="spinner" /></div>
      </div>
    )
  }

  if (!draft) {
    return (
      <div class="screen ai-providers-screen">
        <PageHeader title="AI Providers" subtitle="Configure cloud and local models" />
        <ErrorBanner error={error} />
      </div>
    )
  }

  return (
    <div class="screen ai-providers-screen">
      <PageHeader
        title="AI Providers"
        subtitle="Configure cloud and local model behavior"
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
          <strong>Restart required</strong> — Some changes require restarting the Atlas daemon to take effect.
        </div>
      )}

      <SettingsGroup title="Cloud">
        <SettingsRow label="Provider" sublabel="OpenAI, Anthropic, Gemini, or OpenRouter">
          <select class="input" value={cloudProvider}
            onChange={async (e) => {
              const provider = (e.target as HTMLSelectElement).value as CloudProviderID
              setCloudProvider(provider)
              if (provider !== 'openrouter') setOpenRouterLimit(25)
              await fetchCloudModels(provider, true)
              if (supportiveMode) update('activeAIProvider', provider)
            }}>
            {CLOUD_PROVIDERS.map(p => <option key={p.id} value={p.id}>{p.label}</option>)}
          </select>
        </SettingsRow>
        <SettingsRow
          label="Model"
          sublabel="Primary model for main chat turns"
          status={cloudModelStatusBadge}
        >
          <div style={{ display: 'flex', gap: '8px', width: '100%', justifyContent: 'flex-end' }}>
            <select class="input" value={cloudPrimaryValue}
              onChange={(e) => {
                const next = (e.target as HTMLSelectElement).value
                if (cloudProvider === 'openrouter' && next === OPENROUTER_SHOW_MORE_VALUE) {
                  setOpenRouterLimit((prev) => prev + 25)
                  return
                }
                setCloudPrimaryValue(next)
              }}>
              <option value="">{cloudAutoLabel}</option>
              {(cloudModels?.availableModels ?? []).map(m => (
                <option key={m.id} value={m.id}>{m.displayName}</option>
              ))}
              {cloudProvider === 'openrouter' && (
                <option value={OPENROUTER_SHOW_MORE_VALUE}>Show 25 more</option>
              )}
            </select>
          </div>
        </SettingsRow>
        <SettingsRow
          label="API Key"
          sublabel="Stored in Keychain"
          mobileSplit
          status={
            <span class={`badge ${cloudKeyConfigured ? 'badge-green' : 'badge-red'}`} style={BADGE_STYLE}>
              {cloudKeyConfigured ? 'Configured' : 'Not configured'}
            </span>
          }
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <button class="btn btn-sm" onClick={goToCredentials}>
              {cloudKeyConfigured ? 'Change' : 'Configure'}
            </button>
          </div>
        </SettingsRow>
      </SettingsGroup>

      <SettingsGroup title="Local">
        <SettingsRow
          label="Routing Mode"
          sublabel="Choose how cloud and local models are used"
          hint={"Cloud primary: cloud handles main chat turns, local supports routing/background tasks.\nLocal only: local backend handles all turns."}
        >
          <select
            class="input"
            value={supportiveMode ? 'cloud_primary' : 'local_only'}
            onChange={(e) => {
              const mode = (e.target as HTMLSelectElement).value
              update('activeAIProvider', mode === 'cloud_primary' ? cloudProvider : localBackend)
            }}
          >
            <option value="cloud_primary">Cloud (recommended)</option>
            <option value="local_only">Local only</option>
          </select>
        </SettingsRow>
        <SettingsRow label="Backend" sublabel="Atlas Engine, Ollama, or LM Studio">
          <select class="input" value={localBackend}
            onChange={async (e) => {
              const backend = (e.target as HTMLSelectElement).value as LocalBackendID
              setLocalBackend(backend)
              await fetchLocalModels(backend, true)
              if (!supportiveMode) update('activeAIProvider', backend)
            }}>
            {LOCAL_BACKENDS.map(p => <option key={p.id} value={p.id}>{p.label}</option>)}
            <option value="" disabled>Custom (coming soon)</option>
          </select>
        </SettingsRow>
        <SettingsRow label="Base URL" sublabel="Local backend endpoint">
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', width: '100%' }}>
            <div class="base-url-field-wrap" style={{ position: 'relative' }}>
              {localBackend === 'atlas_engine' ? (
                <input class="input" readOnly value={`http://127.0.0.1:${draft.atlasEnginePort ?? 11985}/v1`} style={{ width: '100%', paddingRight: '74px' }} />
              ) : localBackend === 'ollama' ? (
                <input class="input" value={draft.ollamaBaseURL ?? ''} onInput={(e) => update('ollamaBaseURL', (e.target as HTMLInputElement).value)} style={{ width: '100%', paddingRight: '74px' }} />
              ) : (
                <input class="input" value={draft.lmStudioBaseURL ?? ''} onInput={(e) => update('lmStudioBaseURL', (e.target as HTMLInputElement).value)} style={{ width: '100%', paddingRight: '74px' }} />
              )}
              <span
                class={`badge ${localOnline ? 'badge-green' : 'badge-red'}`}
                style={{ ...BADGE_STYLE, position: 'absolute', right: '10px', top: '50%', transform: 'translateY(-50%)', pointerEvents: 'none' }}
              >
                {localOnline ? 'Online' : 'Offline'}
              </span>
            </div>
          </div>
        </SettingsRow>
        <SettingsRow label="Model" sublabel="Model loaded by the selected local backend">
          <div style={{ display: 'flex', gap: '8px', width: '100%' }}>
            <select class="input" value={localModelValue} onChange={(e) => setLocalModelValue((e.target as HTMLSelectElement).value)}>
              <option value="">{localModels?.primaryModel ? `Auto — ${localModels.primaryModel}` : 'Auto (not yet resolved)'}</option>
              {(localModels?.availableModels ?? []).map(m => (
                <option key={m.id} value={m.id}>
                  {localBackend === 'atlas_engine' ? formatAtlasModelName(m.displayName) : m.displayName}
                </option>
              ))}
            </select>
          </div>
        </SettingsRow>
        <SettingsRow label="Supportive Scope" sublabel="Include heavy background tasks in local routing when cloud is primary" mobileSplit>
          <ToggleField
            checked={draft.atlasEngineRouterForAll ?? false}
            disabled={!supportiveMode}
            onChange={v => update('atlasEngineRouterForAll', v)}
          />
        </SettingsRow>
      </SettingsGroup>

      <SettingsGroup title="Advanced">
        <div style={{ padding: '12px 20px 6px', fontSize: '12px', fontWeight: 600, letterSpacing: '0.02em', color: 'var(--text-3)', textTransform: 'uppercase' }}>
          Safety
        </div>
        <SettingsRow label="Action Safety" sublabel="When Atlas asks before taking action">
          <select class="input input" value={draft.actionSafetyMode}
            onChange={(e) => update('actionSafetyMode', (e.target as HTMLSelectElement).value)}>
            {ACTION_SAFETY_OPTIONS.map(o => (
              <option key={o} value={o}>{SAFETY_LABELS[o]}</option>
            ))}
          </select>
        </SettingsRow>

        <div style={{ padding: '12px 20px 6px', fontSize: '12px', fontWeight: 600, letterSpacing: '0.02em', color: 'var(--text-3)', textTransform: 'uppercase' }}>
          Inference & Tools
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
        {!supportiveMode ? (
          <>
            <SettingsRow
              label="Max Iterations"
              sublabel="Agent loop iterations per turn"
              hint="Keep at 2 for local models — each iteration is slow on local hardware. Cloud providers can handle 3–5 comfortably."
            >
              {localBackend === 'lm_studio' ? (
                <input class="input input-sm" type="number" min={1} max={20}
                  value={draft.lmStudioMaxAgentIterations ?? 2}
                  onInput={(e) => update('lmStudioMaxAgentIterations', Number((e.target as HTMLInputElement).value))} />
              ) : localBackend === 'atlas_engine' ? (
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
              {localBackend === 'lm_studio' ? (
                <input class="input input-sm" type="number" min={0} max={100}
                  value={draft.lmStudioContextWindowLimit ?? 10}
                  onInput={(e) => update('lmStudioContextWindowLimit', Number((e.target as HTMLInputElement).value))} />
              ) : localBackend === 'atlas_engine' ? (
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
        <div style={{ padding: '12px 20px 6px', fontSize: '12px', fontWeight: 600, letterSpacing: '0.02em', color: 'var(--text-3)', textTransform: 'uppercase' }}>
          Memory
        </div>
        <SettingsRow label="Memory Enabled" sublabel="Extract and persist facts from conversations">
          <ToggleField checked={draft.memoryEnabled} onChange={v => update('memoryEnabled', v)} />
        </SettingsRow>
        <SettingsRow label="Max per Turn" sublabel="Memories injected as context per request">
          <input class="input input-sm" type="number" min={0} max={20}
            value={draft.maxRetrievedMemoriesPerTurn}
            onInput={(e) => update('maxRetrievedMemoriesPerTurn', Number((e.target as HTMLInputElement).value))} />
        </SettingsRow>
      </SettingsGroup>

    </div>
  )
}

function SettingsGroup({ title, children }: { title: string; children: preact.ComponentChild }) {
  return (
    <div>
      <div class="section-label">{title}</div>
      <div class="card settings-group" style={{ overflow: 'visible' }}>{children}</div>
    </div>
  )
}



function SettingsRow({
  label,
  sublabel,
  hint,
  status,
  mobileSplit,
  children,
}: {
  label: string
  sublabel?: string
  hint?: string
  status?: preact.ComponentChild
  mobileSplit?: boolean
  children: preact.ComponentChild
}) {
  return (
    <div class={`settings-row${mobileSplit ? ' settings-row-mobile-split' : ''}`}>
      <div class="settings-label-col">
        <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
          {label}
          {status}
          {hint && <InfoTip text={hint} />}
        </div>
        {sublabel && <div class="settings-sublabel">{sublabel}</div>}
      </div>
      <div class="settings-field">{children}</div>
    </div>
  )
}

function InfoTip({ text }: { text: string }) {
  const [visible, setVisible] = useState(false)
  const [side, setSide] = useState<'left' | 'right'>('right')
  const wrapRef = useRef<HTMLSpanElement>(null)
  const btnRef = useRef<HTMLButtonElement>(null)

  const positionTip = () => {
    if (!btnRef.current) return
    const rect = btnRef.current.getBoundingClientRect()
    const estimatedWidth = 280
    const spaceRight = window.innerWidth - rect.right
    setSide(spaceRight < estimatedWidth ? 'left' : 'right')
  }

  const open = () => {
    positionTip()
    setVisible(true)
  }

  useEffect(() => {
    if (!visible) return
    const onDown = (ev: MouseEvent | TouchEvent) => {
      const target = ev.target as Node | null
      if (!target || !wrapRef.current?.contains(target)) setVisible(false)
    }
    window.addEventListener('mousedown', onDown)
    window.addEventListener('touchstart', onDown)
    return () => {
      window.removeEventListener('mousedown', onDown)
      window.removeEventListener('touchstart', onDown)
    }
  }, [visible])

  return (
    <span ref={wrapRef} style={{ display: 'inline-flex', alignItems: 'center', position: 'relative' }}>
      <button
        ref={btnRef}
        type="button"
        style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: '15px', height: '15px', borderRadius: '50%', background: 'var(--text-3)', color: 'var(--bg)', fontSize: '9px', fontWeight: 700, border: 'none', cursor: 'pointer', flexShrink: 0, lineHeight: 1 }}
        onMouseEnter={open}
        onMouseLeave={() => setVisible(false)}
        onFocus={open}
        onBlur={() => setVisible(false)}
        onClick={() => { if (visible) setVisible(false); else open() }}
      >?</button>
      {visible && (
        <span style={{ position: 'absolute', top: '50%', left: side === 'right' ? 'calc(100% + 8px)' : 'auto', right: side === 'left' ? 'calc(100% + 8px)' : 'auto', transform: 'translateY(-50%)', background: 'var(--surface, var(--bg))', border: '1px solid var(--border)', borderRadius: '8px', padding: '8px 11px', fontSize: '12px', color: 'var(--text-2)', width: '260px', zIndex: 9999, lineHeight: 1.5, boxShadow: '0 4px 20px rgba(0,0,0,0.22)', pointerEvents: 'none' }}>
          {text.split('\n').map((line, i) => <span key={i} style={{ display: 'block' }}>{line}</span>)}
        </span>
      )}
    </span>
  )
}

function ToggleField({ checked, disabled, onChange }: { checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
  return (
    <label class="toggle">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange((e.target as HTMLInputElement).checked)} />
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
