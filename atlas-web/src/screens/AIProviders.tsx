import { useEffect, useRef, useState } from 'preact/hooks'
import {
  api,
  type APIKeyStatus,
  type AIModelRecord,
  type CloudModelHealth,
  type ModelSelectorInfo,
  type RuntimeConfig,
} from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import { PageSpinner } from '../components/PageSpinner'
import { toast } from '../toast'
import type { RuntimeConfigUpdateResponse } from '../api/client'
import { formatAtlasModelName } from '../modelName'

const CLOUD_PROVIDERS = [
  { id: 'openai',     label: 'OpenAI',            recommended: true  },
  { id: 'anthropic',  label: 'Claude (Anthropic)', recommended: false },
  { id: 'gemini',     label: 'Gemini (Google)',    recommended: false },
  { id: 'openrouter', label: 'OpenRouter',         recommended: false },
] as const

const OPENAI_IMAGE_MODELS = [
  { id: 'gpt-image-1.5',    label: 'GPT Image 1.5 (recommended)' },
  { id: 'gpt-image-1',      label: 'GPT Image 1' },
  { id: 'gpt-image-1-mini', label: 'GPT Image 1 Mini' },
] as const

const GEMINI_IMAGE_MODELS = [
  { id: 'gemini-2.5-flash-image',          label: 'Gemini 2.5 Flash Image (default)' },
  { id: 'gemini-3.1-flash-image-preview',  label: 'Gemini 3.1 Flash Image (preview)' },
] as const

const LOCAL_BACKENDS = [
  { id: 'atlas_engine', label: 'Llama' },
  { id: 'atlas_mlx', label: 'MLX' },
  { id: 'ollama', label: 'Ollama' },
  { id: 'lm_studio', label: 'LM Studio' },
] as const

type CloudProviderID = typeof CLOUD_PROVIDERS[number]['id']
type LocalBackendID = typeof LOCAL_BACKENDS[number]['id']
type ProviderMode = 'cloud' | 'hybrid' | 'local'

const CLOUD_PROVIDER_IDS = new Set<string>(CLOUD_PROVIDERS.map((provider) => provider.id))
const LOCAL_PROVIDER_IDS = new Set<string>(LOCAL_BACKENDS.map((provider) => provider.id))
const HYBRID_BACKEND_IDS = new Set<LocalBackendID>(['atlas_engine', 'atlas_mlx'])
const BADGE_STYLE = { fontSize: '11px', padding: '2px 8px' } as const

function emptyModelSelector(message: string): ModelSelectorInfo {
  return {
    primaryModel: '',
    fastModel: '',
    availableModels: [],
    lastRefreshedAt: new Date().toISOString(),
    providerStatus: {
      state: 'unreachable',
      label: 'Unavailable',
      tone: 'red',
      message,
      checkedAt: new Date().toISOString(),
    },
  }
}

function providerToneClass(tone?: string): string {
  switch (tone) {
    case 'green': return 'badge-green'
    case 'yellow': return 'badge-yellow'
    case 'red': return 'badge-red'
    default: return ''
  }
}

function cloudHealthTone(status?: string): string {
  switch (status) {
    case 'ok': return 'green'
    case 'rate_limited':
    case 'warning': return 'yellow'
    case 'missing_key':
    case 'unavailable': return 'red'
    default: return 'neutral'
  }
}

function cloudHealthLabel(status?: string): string {
  switch (status) {
    case 'ok': return 'Available'
    case 'rate_limited': return 'Rate limited'
    case 'missing_key': return 'Missing API key'
    case 'warning': return 'Needs review'
    case 'unavailable': return 'Unavailable'
    default: return 'Not tested'
  }
}

function localBackendSupportsFastModel(backend: LocalBackendID): boolean {
  return backend !== 'atlas_mlx'
}

function localBackendSupportsHeavyBackgroundToggle(backend: LocalBackendID): boolean {
  return backend === 'atlas_engine' || backend === 'atlas_mlx'
}

function formatLocalModelOption(backend: LocalBackendID, model: AIModelRecord): string {
  return (backend === 'atlas_engine' || backend === 'atlas_mlx')
    ? formatAtlasModelName(model.displayName)
    : model.displayName
}

function basename(path: string) {
  return path && path.includes('/') ? (path.split('/').pop() ?? path) : path
}

export function AIProviders() {
  const [config, setConfig] = useState<RuntimeConfig | null>(null)
  const [draft, setDraft] = useState<RuntimeConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [restartRequired, setRestartRequired] = useState(false)
  const [cloudModels, setCloudModels] = useState<ModelSelectorInfo | null>(null)
  const [localModels, setLocalModels] = useState<ModelSelectorInfo | null>(null)
  const [keyStatus, setKeyStatus] = useState<APIKeyStatus | null>(null)
  const [cloudModelHealth, setCloudModelHealth] = useState<CloudModelHealth | null>(null)
  const [checkingCloudModelHealth, setCheckingCloudModelHealth] = useState(false)
  const [openRouterLimit, setOpenRouterLimit] = useState(25)
  const [cloudProvider, setCloudProvider] = useState<CloudProviderID>('openai')
  const [localBackend, setLocalBackend] = useState<LocalBackendID>('atlas_engine')

  // Finding 40: request sequence counter to discard stale out-of-order fetch results
  const cloudFetchSeq = useRef(0)
  const localFetchSeq = useRef(0)

  const fetchCloudModels = async (provider: CloudProviderID, refresh: boolean) => {
    const seq = ++cloudFetchSeq.current
    setCloudModels(null)
    try {
      const next = provider === 'openrouter'
        ? await api.openRouterModels(refresh, openRouterLimit)
        : await api.modelsForProvider(provider, refresh)
      if (seq !== cloudFetchSeq.current) return
      setCloudModels(next)
    } catch (err) {
      if (seq !== cloudFetchSeq.current) return
      // Finding 41: distinguish connection errors from empty model lists
      const msg = err instanceof Error ? err.message : ''
      const isNetworkError = msg.includes('fetch') || msg.includes('network') ||
        msg.includes('ECONNREFUSED') || msg.includes('Failed to fetch') || msg.includes('NetworkError')
      setCloudModels(emptyModelSelector(isNetworkError ? 'Local backend unreachable.' : (msg || 'Could not load cloud models.')))
    }
  }

  const fetchLocalModels = async (backend: LocalBackendID, refresh: boolean) => {
    const seq = ++localFetchSeq.current
    setLocalModels(null)
    try {
      const next = await api.modelsForProvider(backend, refresh)
      if (seq !== localFetchSeq.current) return
      // Finding 41: distinguish empty list from connection error
      if (!next.availableModels || next.availableModels.length === 0) {
        setLocalModels(emptyModelSelector('No models found.'))
        return
      }
      setLocalModels(next)
    } catch (err) {
      if (seq !== localFetchSeq.current) return
      // Finding 41: distinguish connection errors from empty model lists
      const msg = err instanceof Error ? err.message : ''
      const isNetworkError = msg.includes('fetch') || msg.includes('network') ||
        msg.includes('ECONNREFUSED') || msg.includes('Failed to fetch') || msg.includes('NetworkError')
      setLocalModels(emptyModelSelector(isNetworkError ? 'Local backend unreachable.' : (msg || 'Could not reach the local backend.')))
    }
  }

  useEffect(() => {
    const init = async () => {
      try {
        const [nextConfig, nextKeys] = await Promise.all([api.config(), api.apiKeys().catch(() => null)])
        setConfig(nextConfig)
        setDraft(nextConfig)
        if (nextKeys) setKeyStatus(nextKeys)

        const active = nextConfig.activeAIProvider ?? 'openai'
        const selectedLocal = nextConfig.selectedLocalEngine ?? ''
        const initialCloud: CloudProviderID = CLOUD_PROVIDER_IDS.has(active) ? (active as CloudProviderID) : 'openai'
        const initialLocal: LocalBackendID = LOCAL_PROVIDER_IDS.has(selectedLocal)
          ? (selectedLocal as LocalBackendID)
          : (LOCAL_PROVIDER_IDS.has(active) ? (active as LocalBackendID) : 'atlas_engine')

        setCloudProvider(initialCloud)
        setLocalBackend(initialLocal)
        await Promise.all([
          fetchCloudModels(initialCloud, true),
          fetchLocalModels(initialLocal, true),
        ])
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load config.')
      } finally {
        setLoading(false)
      }
    }
    void init()
  }, [])

  useEffect(() => {
    if (cloudProvider !== 'openrouter') return
    void fetchCloudModels('openrouter', true)
  }, [openRouterLimit])

  const update = <K extends keyof RuntimeConfig>(key: K, value: RuntimeConfig[K]) => {
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev))
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    setError(null)
    try {
      const prevPrimaryModel = config?.selectedAtlasEngineModel
      const prevProvider = config?.activeAIProvider
      const result: RuntimeConfigUpdateResponse = await api.updateConfig(draft)
      setConfig(result.config)
      setDraft(result.config)
      toast.success('Changes saved.')
      setRestartRequired(result.restartRequired)
      void fetchCloudModels(cloudProvider, true)
      void fetchLocalModels(localBackend, true)
      if (result.config.activeAIProvider === 'atlas_engine') {
        const newModel = basename(result.config.selectedAtlasEngineModel ?? '')
        const oldModel = basename(prevPrimaryModel ?? '')
        const switchedToEngine = prevProvider !== 'atlas_engine'
        if (newModel && (newModel !== oldModel || switchedToEngine)) {
          api.engineStart(newModel).catch(() => {})
        }
      }
      if (result.config.activeAIProvider === 'atlas_mlx') {
        const newModel = basename(result.config.selectedAtlasMLXModel ?? '')
        if (newModel && prevProvider !== 'atlas_mlx') {
          api.mlxStart(newModel).catch(() => {})
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
    return keys.some((key) => config[key] !== draft[key]) ||
      (Object.keys(draft) as (keyof RuntimeConfig)[]).some((key) => !(key in config))
  })()

  if (loading) {
    return (
      <div class="screen ai-providers-screen">
        <PageHeader title="AI Providers" subtitle="Set up how Atlas chooses between cloud and local models" />
        <PageSpinner />
      </div>
    )
  }

  if (!draft) {
    return (
      <div class="screen ai-providers-screen">
        <PageHeader title="AI Providers" subtitle="Set up how Atlas chooses between cloud and local models" />
        <ErrorBanner error={error} />
      </div>
    )
  }

  const activeProviderType = draft.activeAIProvider ?? 'openai'
  const selectedSupportiveBackend = draft.selectedLocalEngine ?? ''
  const hybridEnabled = CLOUD_PROVIDER_IDS.has(activeProviderType) && HYBRID_BACKEND_IDS.has(selectedSupportiveBackend as LocalBackendID)
  const mode: ProviderMode = CLOUD_PROVIDER_IDS.has(activeProviderType)
    ? (hybridEnabled ? 'hybrid' : 'cloud')
    : 'local'

  const cloudProviderStatus = cloudModels?.providerStatus ?? null
  const localProviderStatus = localModels?.providerStatus ?? null
  const hybridBackend: LocalBackendID = HYBRID_BACKEND_IDS.has(localBackend) ? localBackend : 'atlas_engine'

  const cloudKeyConfigured = (() => {
    switch (cloudProvider) {
      case 'openai': return keyStatus?.openAIKeySet ?? false
      case 'anthropic': return keyStatus?.anthropicKeySet ?? false
      case 'gemini': return keyStatus?.geminiKeySet ?? false
      case 'openrouter': return keyStatus?.openRouterKeySet ?? false
      default: return false
    }
  })()

  const cloudPrimaryValue = (() => {
    switch (cloudProvider) {
      case 'anthropic': return draft.selectedAnthropicModel ?? ''
      case 'gemini': return draft.selectedGeminiModel ?? ''
      case 'openrouter': return draft.selectedOpenRouterModel ?? ''
      default: return draft.selectedOpenAIPrimaryModel ?? ''
    }
  })()

  const cloudFastValue = (() => {
    switch (cloudProvider) {
      case 'anthropic': return draft.selectedAnthropicFastModel ?? ''
      case 'gemini': return draft.selectedGeminiFastModel ?? ''
      case 'openrouter': return draft.selectedOpenRouterFastModel ?? ''
      default: return draft.selectedOpenAIFastModel ?? ''
    }
  })()

  const cloudAutoLabel = (() => {
    if (cloudProvider === 'openrouter') {
      const resolved = cloudModels?.primaryModel || draft.selectedOpenRouterModel || 'openrouter/auto:free'
      if (resolved === 'openrouter/auto:free') return 'Auto — Free Models Router'
      if (resolved === 'openrouter/auto') return 'Auto — OpenRouter Auto Router'
      return `Auto — ${resolved}`
    }
    return cloudModels?.primaryModel ? `Auto — ${cloudModels.primaryModel}` : 'Auto (recommended default)'
  })()

  const cloudFastAutoLabel = cloudModels?.fastModel
    ? `Auto — ${cloudModels.fastModel}`
    : 'Auto (recommended fast model)'

  const localModelValue = (() => {
    switch (localBackend) {
      case 'ollama': return draft.selectedOllamaModel ?? ''
      case 'lm_studio': return draft.selectedLMStudioModel ?? ''
      case 'atlas_mlx': return basename(draft.selectedAtlasMLXModel ?? '')
      default: return basename(draft.selectedAtlasEngineModel ?? '')
    }
  })()

  const localFastValue = (() => {
    switch (localBackend) {
      case 'ollama': return draft.selectedOllamaModelFast ?? ''
      case 'lm_studio': return draft.selectedLMStudioModelFast ?? ''
      default: return basename(draft.selectedAtlasEngineModelFast ?? '')
    }
  })()

  const localAutoLabel = localModels?.primaryModel
    ? `Auto — ${basename(localModels.primaryModel)}`
    : 'Auto (recommended default)'
  const localFastAutoLabel = localModels?.fastModel
    ? `Auto — ${basename(localModels.fastModel)}`
    : 'Auto (recommended fast model)'

  const setCloudPrimaryValue = (value: string) => {
    switch (cloudProvider) {
      case 'anthropic': update('selectedAnthropicModel', value); break
      case 'gemini': update('selectedGeminiModel', value); break
      case 'openrouter': update('selectedOpenRouterModel', value); break
      default: update('selectedOpenAIPrimaryModel', value); break
    }
  }

  const setCloudFastValue = (value: string) => {
    switch (cloudProvider) {
      case 'anthropic': update('selectedAnthropicFastModel', value); break
      case 'gemini': update('selectedGeminiFastModel', value); break
      case 'openrouter': update('selectedOpenRouterFastModel', value); break
      default: update('selectedOpenAIFastModel', value); break
    }
  }

  const cloudImageValue = (() => {
    switch (cloudProvider) {
      case 'gemini': return draft.selectedGeminiImageModel ?? ''
      case 'openai': return draft.selectedOpenAIImageModel ?? ''
      default: return ''
    }
  })()

  const setCloudImageValue = (value: string) => {
    switch (cloudProvider) {
      case 'gemini': update('selectedGeminiImageModel', value); break
      case 'openai': update('selectedOpenAIImageModel', value); break
    }
  }

  const cloudImageModels = cloudProvider === 'gemini' ? GEMINI_IMAGE_MODELS : OPENAI_IMAGE_MODELS
  const supportsImageGen = cloudProvider === 'openai' || cloudProvider === 'gemini'

  const setLocalPrimaryValue = (value: string) => {
    switch (localBackend) {
      case 'ollama': update('selectedOllamaModel', value); break
      case 'lm_studio': update('selectedLMStudioModel', value); break
      case 'atlas_mlx': update('selectedAtlasMLXModel', value); break
      default: update('selectedAtlasEngineModel', value); break
    }
  }

  const setLocalFastValue = (value: string) => {
    switch (localBackend) {
      case 'ollama': update('selectedOllamaModelFast', value); break
      case 'lm_studio': update('selectedLMStudioModelFast', value); break
      default: update('selectedAtlasEngineModelFast', value); break
    }
  }

  const setMode = async (nextMode: ProviderMode) => {
    if (nextMode === 'cloud') {
      update('activeAIProvider', cloudProvider)
      update('selectedLocalEngine', '')
      return
    }
    if (nextMode === 'hybrid') {
      const nextBackend: LocalBackendID = HYBRID_BACKEND_IDS.has(localBackend) ? localBackend : 'atlas_engine'
      setLocalBackend(nextBackend)
      update('activeAIProvider', cloudProvider)
      update('selectedLocalEngine', nextBackend)
      await fetchLocalModels(nextBackend, true)
      return
    }
    update('selectedLocalEngine', localBackend)
    update('activeAIProvider', localBackend)
    await fetchLocalModels(localBackend, true)
  }

  const testCloudConnection = async () => {
    const modelToCheck = (cloudPrimaryValue || cloudModels?.primaryModel || (cloudProvider === 'openrouter' ? 'openrouter/auto:free' : '')).trim()
    if (!modelToCheck) {
      setCloudModelHealth({
        status: 'unknown',
        message: 'Choose a model before testing this provider.',
        checkedAt: new Date().toISOString(),
      })
      return
    }
    setCheckingCloudModelHealth(true)
    try {
      const health = await api.cloudModelHealth(cloudProvider, modelToCheck)
      setCloudModelHealth(health)
    } catch (err) {
      setCloudModelHealth({
        status: 'unavailable',
        message: err instanceof Error ? err.message : 'Could not check model availability.',
        checkedAt: new Date().toISOString(),
      })
    } finally {
      setCheckingCloudModelHealth(false)
    }
  }

  const goToCredentials = () => {
    window.location.hash = 'api-keys'
  }

  const cloudConnectionBadge = cloudModelHealth
    ? (
      <span class={`badge ${providerToneClass(cloudHealthTone(cloudModelHealth.status))}`} style={BADGE_STYLE} title={cloudModelHealth.message}>
        {cloudHealthLabel(cloudModelHealth.status)}
      </span>
    )
    : cloudProviderStatus
      ? (
        <span class={`badge ${providerToneClass(cloudProviderStatus.tone)}`} style={BADGE_STYLE} title={cloudProviderStatus.message}>
          {cloudProviderStatus.label}
        </span>
      )
      : null

  const localConnectionBadge = localProviderStatus
    ? (
      <span class={`badge ${providerToneClass(localProviderStatus.tone)}`} style={BADGE_STYLE} title={localProviderStatus.message}>
        {localProviderStatus.label}
      </span>
    )
    : null

  const localBaseURL = localBackend === 'atlas_engine'
    ? `http://127.0.0.1:${draft.atlasEnginePort ?? 11985}/v1`
    : localBackend === 'atlas_mlx'
      ? `http://127.0.0.1:${draft.atlasMLXPort ?? 11990}/v1`
      : localBackend === 'ollama'
        ? (draft.ollamaBaseURL ?? '')
        : (draft.lmStudioBaseURL ?? '')

  const modeSummary = (() => {
    if (mode === 'cloud') {
      return {
        title: `${CLOUD_PROVIDERS.find((provider) => provider.id === cloudProvider)?.label ?? 'Cloud'} handles everything`,
        copy: 'Atlas keeps your main chat, background work, memory, and reflection inside the selected cloud provider.',
      }
    }
    if (mode === 'hybrid') {
      return {
        title: `${CLOUD_PROVIDERS.find((provider) => provider.id === cloudProvider)?.label ?? 'Cloud'} leads, ${LOCAL_BACKENDS.find((provider) => provider.id === hybridBackend)?.label ?? 'Local'} assists`,
        copy: 'Cloud handles the main conversation. Atlas can use the local backend for supportive routing and, if you opt in, heavier background work too.',
      }
    }
    return {
      title: `${LOCAL_BACKENDS.find((provider) => provider.id === localBackend)?.label ?? 'Local'} handles everything`,
      copy: 'Atlas stays fully local in this mode. Your cloud setup stays saved for later, but it is not used until you switch back.',
    }
  })()

  return (
    <div class="screen ai-providers-screen">
      <PageHeader
        title="AI Providers"
        subtitle="Choose the setup style you want first, then configure only the providers that matter for that mode."
        actions={
          <button class="btn btn-primary btn-sm" onClick={save} disabled={saving || !isDirty}>
            {saving
              ? <><span class="spinner spinner-sm" style={{ borderTopColor: '#000', borderColor: 'rgba(0,0,0,0.2)' }} /> Saving…</>
              : 'Save changes'}
          </button>
        }
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />
      {restartRequired && (
        <div class="banner banner-info" style={{ marginBottom: 16 }}>
          <span class="banner-message">Restart Atlas to apply all provider changes.</span>
          <button class="banner-dismiss" onClick={() => setRestartRequired(false)} title="Dismiss">x</button>
        </div>
      )}

      <div>
        <div class="section-label">Setup</div>
        <div class="card ai-provider-setup-card">
          <div class="ai-mode-grid">
            <ModeCard
              title="Cloud"
              description="Use one cloud provider for everything."
              selected={mode === 'cloud'}
              onClick={() => void setMode('cloud')}
            />
            <ModeCard
              title="Hybrid"
              description="Use cloud for main chat and local for background work."
              selected={mode === 'hybrid'}
              onClick={() => void setMode('hybrid')}
            />
            <ModeCard
              title="Local"
              description="Run Atlas entirely through a local backend."
              selected={mode === 'local'}
              onClick={() => void setMode('local')}
            />
          </div>
        </div>
        <p class="ai-provider-summary-text" style={{ marginTop: '10px' }}>
          <span class="ai-provider-summary-label">Current setup:</span>{' '}
          {modeSummary.title}. {modeSummary.copy}
        </p>
      </div>

      {mode === 'cloud' && (
        <SettingsGroup title="Cloud setup">
          <SettingsRow label="Provider" sublabel="Choose the cloud provider Atlas should use" fieldId="ai-provider-cloud-provider">
            <select
              id="ai-provider-cloud-provider"
              class="input"
              value={cloudProvider}
              onChange={async (e) => {
                const provider = (e.target as HTMLSelectElement).value as CloudProviderID
                setCloudProvider(provider)
                setCloudModelHealth(null)
                if (provider !== 'openrouter') setOpenRouterLimit(25)
                update('activeAIProvider', provider)
                await fetchCloudModels(provider, true)
              }}
            >
              {CLOUD_PROVIDERS.map((provider) => <option key={provider.id} value={provider.id}>{provider.label}{provider.recommended ? ' — Recommended' : ''}</option>)}
            </select>
          </SettingsRow>
          <ModelSelectRow
            label="Main model"
            fieldId="ai-provider-cloud-primary-model"
            sublabel="Used for your main chat turns"
            selectValue={cloudPrimaryValue}
            onSelect={setCloudPrimaryValue}
            autoLabel={cloudAutoLabel}
            options={cloudModels?.availableModels ?? []}
            optionFormatter={(model) => model.displayName}
            status={cloudConnectionBadge}
            itemCountLabel={cloudProvider === 'openrouter' && cloudModels?.totalAvailable
              ? `${cloudModels.availableModels?.length ?? 0} of ${cloudModels.totalAvailable} shown`
              : undefined}
          />
          <SettingsRow
            label="Model availability"
            sublabel="Check whether the selected cloud model can answer right now"
            mobileSplit
          >
            <button class="btn btn-sm" onClick={testCloudConnection} disabled={checkingCloudModelHealth}>
              {checkingCloudModelHealth ? 'Checking...' : 'Test'}
            </button>
          </SettingsRow>
          <details class="ai-provider-advanced-panel">
            <summary>Advanced cloud options</summary>
            <div class="ai-provider-advanced-panel-body">
              <ModelSelectRow
                label="Speed-optimized model"
                fieldId="ai-provider-cloud-fast-model"
                sublabel="Used for lighter cloud-side background work"
                selectValue={cloudFastValue}
                onSelect={setCloudFastValue}
                autoLabel={cloudFastAutoLabel}
                options={cloudModels?.availableModels ?? []}
                optionFormatter={(model) => model.displayName}
              />
              {supportsImageGen && (
                <SettingsRow
                  label="Image generation model"
                  sublabel="Model used by the image.generate skill"
                  fieldId="ai-provider-cloud-image-model"
                >
                  <select
                    id="ai-provider-cloud-image-model"
                    class="input"
                    value={cloudImageValue}
                    onChange={(e) => setCloudImageValue((e.target as HTMLSelectElement).value)}
                  >
                    {cloudImageModels.map((m) => (
                      <option key={m.id} value={m.id}>{m.label}</option>
                    ))}
                  </select>
                </SettingsRow>
              )}
              <SettingsRow
                label="API key"
                sublabel="Stored in Keychain and used for live requests"
                mobileSplit
                status={<span class={`badge ${cloudKeyConfigured ? 'badge-green' : 'badge-red'}`} style={BADGE_STYLE}>{cloudKeyConfigured ? 'Configured' : 'Not configured'}</span>}
              >
                <button class="btn btn-sm" onClick={goToCredentials}>{cloudKeyConfigured ? 'Change key' : 'Configure key'}</button>
              </SettingsRow>
            </div>
          </details>
        </SettingsGroup>
      )}

      {mode === 'hybrid' && (
        <SettingsGroup title="Hybrid setup">
          <div class="ai-provider-mini-section-label">Cloud</div>
          <SettingsRow label="Provider" sublabel="Choose the cloud provider Atlas should use" fieldId="ai-provider-hybrid-cloud-provider">
            <select
              id="ai-provider-hybrid-cloud-provider"
              class="input"
              value={cloudProvider}
              onChange={async (e) => {
                const provider = (e.target as HTMLSelectElement).value as CloudProviderID
                setCloudProvider(provider)
                setCloudModelHealth(null)
                if (provider !== 'openrouter') setOpenRouterLimit(25)
                update('activeAIProvider', provider)
                await fetchCloudModels(provider, true)
              }}
            >
              {CLOUD_PROVIDERS.map((provider) => <option key={provider.id} value={provider.id}>{provider.label}{provider.recommended ? ' — Recommended' : ''}</option>)}
            </select>
          </SettingsRow>
          <ModelSelectRow
            label="Main model"
            fieldId="ai-provider-hybrid-cloud-primary-model"
            sublabel="Used for your main chat turns"
            selectValue={cloudPrimaryValue}
            onSelect={setCloudPrimaryValue}
            autoLabel={cloudAutoLabel}
            options={cloudModels?.availableModels ?? []}
            optionFormatter={(model) => model.displayName}
            status={cloudConnectionBadge}
            itemCountLabel={cloudProvider === 'openrouter' && cloudModels?.totalAvailable
              ? `${cloudModels.availableModels?.length ?? 0} of ${cloudModels.totalAvailable} shown`
              : undefined}
          />
          <SettingsRow
            label="Model availability"
            sublabel="Check whether the selected cloud model can answer right now"
            mobileSplit
          >
            <button class="btn btn-sm" onClick={testCloudConnection} disabled={checkingCloudModelHealth}>
              {checkingCloudModelHealth ? 'Checking...' : 'Test'}
            </button>
          </SettingsRow>
          <div class="ai-provider-mini-section-label">Background</div>
          <SettingsRow
            label="Backend"
            sublabel="Choose the local backend Atlas should use for supportive routing"
            fieldId="ai-provider-hybrid-backend"
          >
            <select
              id="ai-provider-hybrid-backend"
              class="input"
              value={hybridBackend}
              onChange={async (e) => {
                const backend = (e.target as HTMLSelectElement).value as LocalBackendID
                setLocalBackend(backend)
                update('selectedLocalEngine', backend)
                await fetchLocalModels(backend, true)
              }}
            >
              {LOCAL_BACKENDS.filter((provider) => HYBRID_BACKEND_IDS.has(provider.id)).map((provider) => (
                <option key={provider.id} value={provider.id}>{provider.label}</option>
              ))}
            </select>
          </SettingsRow>
          <ModelSelectRow
            label="Background model"
            fieldId="ai-provider-hybrid-local-model"
            sublabel="Model used for supportive background routing"
            selectValue={localModelValue}
            onSelect={setLocalPrimaryValue}
            autoLabel={localAutoLabel}
            options={localModels?.availableModels ?? []}
            optionFormatter={(model) => formatLocalModelOption(hybridBackend, model)}
            status={localConnectionBadge}
          />

          <details class="ai-provider-advanced-panel">
            <summary>Advanced hybrid options</summary>
            <div class="ai-provider-advanced-panel-body">
              {supportsImageGen && (
                <SettingsRow
                  label="Image generation model"
                  sublabel="Model used by the image.generate skill"
                  fieldId="ai-provider-hybrid-image-model"
                >
                  <select
                    id="ai-provider-hybrid-image-model"
                    class="input"
                    value={cloudImageValue}
                    onChange={(e) => setCloudImageValue((e.target as HTMLSelectElement).value)}
                  >
                    {cloudImageModels.map((m) => (
                      <option key={m.id} value={m.id}>{m.label}</option>
                    ))}
                  </select>
                </SettingsRow>
              )}
              {localBackendSupportsHeavyBackgroundToggle(hybridBackend) && (
                <SettingsRow
                  label="Use local for memory and reflection"
                  sublabel="Let the local router take heavier background tasks too"
                  mobileSplit
                >
                  <ToggleField
                    checked={hybridBackend === 'atlas_mlx'
                      ? (draft.atlasMLXRouterForAll ?? false)
                      : (draft.atlasEngineRouterForAll ?? false)}
                    onChange={(value) => hybridBackend === 'atlas_mlx'
                      ? update('atlasMLXRouterForAll', value)
                      : update('atlasEngineRouterForAll', value)}
                  />
                </SettingsRow>
              )}
              <SettingsRow
                label="API key"
                sublabel="Stored in Keychain and used for live requests"
                mobileSplit
                status={<span class={`badge ${cloudKeyConfigured ? 'badge-green' : 'badge-red'}`} style={BADGE_STYLE}>{cloudKeyConfigured ? 'Configured' : 'Not configured'}</span>}
              >
                <button class="btn btn-sm" onClick={goToCredentials}>{cloudKeyConfigured ? 'Change key' : 'Configure key'}</button>
              </SettingsRow>
            </div>
          </details>
        </SettingsGroup>
      )}

      {mode === 'local' && (
        <SettingsGroup title="Local setup">
          <SettingsRow
            label="Backend"
            sublabel="Choose the local backend Atlas should use for all turns"
            fieldId="ai-provider-local-backend"
          >
            <select
              id="ai-provider-local-backend"
              class="input"
              value={localBackend}
              onChange={async (e) => {
                const backend = (e.target as HTMLSelectElement).value as LocalBackendID
                setLocalBackend(backend)
                update('selectedLocalEngine', backend)
                update('activeAIProvider', backend)
                await fetchLocalModels(backend, true)
              }}
            >
              {LOCAL_BACKENDS.map((provider) => <option key={provider.id} value={provider.id}>{provider.label}</option>)}
            </select>
          </SettingsRow>
          <ModelSelectRow
            label="Main model"
            fieldId="ai-provider-local-primary-model"
            sublabel="Used for every turn while Atlas is in local mode"
            selectValue={localModelValue}
            onSelect={setLocalPrimaryValue}
            autoLabel={localAutoLabel}
            options={localModels?.availableModels ?? []}
            optionFormatter={(model) => formatLocalModelOption(localBackend, model)}
            status={localConnectionBadge}
          />
          <details class="ai-provider-advanced-panel">
            <summary>Advanced local options</summary>
            <div class="ai-provider-advanced-panel-body">
              {localBackendSupportsFastModel(localBackend) && (
                <ModelSelectRow
                  label="Speed-optimized model"
                  fieldId="ai-provider-local-fast-model"
                  sublabel="Optional faster model for lightweight local tasks"
                  selectValue={localFastValue}
                  onSelect={setLocalFastValue}
                  autoLabel={localFastAutoLabel}
                  options={localModels?.availableModels ?? []}
                  optionFormatter={(model) => formatLocalModelOption(localBackend, model)}
                />
              )}
              <SettingsRow
                label="Base URL"
                sublabel={localBackend === 'atlas_engine' || localBackend === 'atlas_mlx'
                  ? 'Managed by Atlas locally'
                  : 'Atlas uses this endpoint to discover and call local models'}
                fieldId="ai-provider-local-base-url"
              >
                {localBackend === 'atlas_engine' || localBackend === 'atlas_mlx' ? (
                  <input id="ai-provider-local-base-url" class="input" readOnly value={localBaseURL} />
                ) : localBackend === 'ollama' ? (
                  <input
                    id="ai-provider-local-base-url"
                    class="input"
                    value={localBaseURL}
                    onInput={(e) => update('ollamaBaseURL', (e.target as HTMLInputElement).value)}
                  />
                ) : (
                  <input
                    id="ai-provider-local-base-url"
                    class="input"
                    value={localBaseURL}
                    onInput={(e) => update('lmStudioBaseURL', (e.target as HTMLInputElement).value)}
                  />
                )}
              </SettingsRow>
            </div>
          </details>
        </SettingsGroup>
      )}

      <SettingsGroup title="Advanced">
        <details class="ai-provider-advanced-panel">
          <summary>Inference & tools</summary>
          <div class="ai-provider-advanced-panel-body">
            <div class="ai-provider-mini-section-label">Inference & Tools</div>
            <SettingsRow
              label="Tool selection"
              sublabel="Controls how much tool context Atlas loads before each turn"
              hint={"Smart: preloads a compact AI-routed tool set and keeps request_tools as an escape hatch.\nKeywords: topic pre-matched.\nAI Router: compact AI-routed set only.\nOff: all tools, always."}
              fieldId="ai-provider-tool-selection"
            >
              <select
                id="ai-provider-tool-selection"
                class="input"
                value={draft.toolSelectionMode ?? 'lazy'}
                onChange={(e) => update('toolSelectionMode', (e.target as HTMLSelectElement).value)}
              >
                <option value="lazy">Smart (default)</option>
                <option value="heuristic">Keywords</option>
                <option value="llm">AI Router</option>
                <option value="off">Off</option>
              </select>
            </SettingsRow>

            {mode === 'local' ? (
              <>
                <SettingsRow
                  label="Max iterations"
                  sublabel="Agent loop iterations per turn"
                  hint="Keep this low for local models. More iterations improve planning quality but increase latency."
                  fieldId="ai-provider-max-iterations-local"
                >
                  {localBackend === 'lm_studio' ? (
                    <input id="ai-provider-max-iterations-local" class="input input-sm" type="number" min={1} max={20} value={draft.lmStudioMaxAgentIterations ?? 2} onInput={(e) => update('lmStudioMaxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                  ) : localBackend === 'atlas_engine' ? (
                    <input id="ai-provider-max-iterations-local" class="input input-sm" type="number" min={1} max={20} value={draft.atlasEngineMaxAgentIterations ?? 2} onInput={(e) => update('atlasEngineMaxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                  ) : localBackend === 'atlas_mlx' ? (
                    <input id="ai-provider-max-iterations-local" class="input input-sm" type="number" min={1} max={20} value={draft.maxAgentIterations ?? 3} onInput={(e) => update('maxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                  ) : (
                    <input id="ai-provider-max-iterations-local" class="input input-sm" type="number" min={1} max={20} value={draft.ollamaMaxAgentIterations ?? 2} onInput={(e) => update('ollamaMaxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                  )}
                </SettingsRow>
                <SettingsRow
                  label="Context window"
                  sublabel="Messages from history sent per request (0 = unlimited)"
                  hint="Smaller context windows help local backends respond faster."
                  fieldId="ai-provider-context-window-local"
                >
                  {localBackend === 'lm_studio' ? (
                    <input id="ai-provider-context-window-local" class="input input-sm" type="number" min={0} max={100} value={draft.lmStudioContextWindowLimit ?? 10} onInput={(e) => update('lmStudioContextWindowLimit', Number((e.target as HTMLInputElement).value))} />
                  ) : localBackend === 'atlas_engine' ? (
                    <input id="ai-provider-context-window-local" class="input input-sm" type="number" min={0} max={100} value={draft.atlasEngineContextWindowLimit ?? 10} onInput={(e) => update('atlasEngineContextWindowLimit', Number((e.target as HTMLInputElement).value))} />
                  ) : localBackend === 'atlas_mlx' ? (
                    <input id="ai-provider-context-window-local" class="input input-sm" type="number" min={0} max={100} value={draft.conversationWindowLimit ?? 10} onInput={(e) => update('conversationWindowLimit', Number((e.target as HTMLInputElement).value))} />
                  ) : (
                    <input id="ai-provider-context-window-local" class="input input-sm" type="number" min={0} max={100} value={draft.ollamaContextWindowLimit ?? 10} onInput={(e) => update('ollamaContextWindowLimit', Number((e.target as HTMLInputElement).value))} />
                  )}
                </SettingsRow>
              </>
            ) : (
              <>
                <SettingsRow
                  label="Max iterations"
                  sublabel="Agent loop iterations per turn"
                  hint="Cloud providers usually handle 3 well. Raise this for complex workflows; lower it to reduce cost."
                  fieldId="ai-provider-max-iterations-cloud"
                >
                  <input id="ai-provider-max-iterations-cloud" class="input input-sm" type="number" min={1} max={20} value={draft.maxAgentIterations} onInput={(e) => update('maxAgentIterations', Number((e.target as HTMLInputElement).value))} />
                </SettingsRow>
                <SettingsRow
                  label="Context window"
                  sublabel="Messages from history sent per request (0 = unlimited)"
                  hint="Lower this to reduce cost on long-running cloud conversations."
                  fieldId="ai-provider-context-window-cloud"
                >
                  <input id="ai-provider-context-window-cloud" class="input input-sm" type="number" min={0} max={100} value={draft.conversationWindowLimit} onInput={(e) => update('conversationWindowLimit', Number((e.target as HTMLInputElement).value))} />
                </SettingsRow>
              </>
            )}

          </div>
        </details>
      </SettingsGroup>
    </div>
  )
}

function SettingsGroup({ title, children }: { title: string; children: preact.ComponentChild }) {
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
  hint,
  status,
  mobileSplit,
  fieldId,
  children,
}: {
  label: string
  sublabel?: string
  hint?: string
  status?: preact.ComponentChild
  mobileSplit?: boolean
  fieldId?: string
  children: preact.ComponentChild
}) {
  const sublabelId = fieldId && sublabel ? `${fieldId}-description` : undefined
  return (
    <div class={`settings-row${mobileSplit ? ' settings-row-mobile-split' : ''}`}>
      <div class="settings-label-col">
        <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
          {fieldId ? <label htmlFor={fieldId}>{label}</label> : label}
          {status}
          {hint && <InfoTip text={hint} />}
        </div>
        {sublabel && <div class="settings-sublabel" id={sublabelId}>{sublabel}</div>}
      </div>
      <div class="settings-field">{children}</div>
    </div>
  )
}

function ModeCard({
  title,
  description,
  selected,
  onClick,
}: {
  title: string
  description: string
  selected: boolean
  onClick: () => void
}) {
  return (
    <button type="button" class={`card ai-mode-card${selected ? ' ai-mode-card-selected' : ''}`} onClick={onClick}>
      <span class="ai-mode-card-title">{title}</span>
      <span class="ai-mode-card-copy">{description}</span>
    </button>
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
    const onDown = (event: MouseEvent | TouchEvent) => {
      const target = event.target as Node | null
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
        aria-label="More information"
        style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: '15px', height: '15px', borderRadius: '50%', background: 'var(--text-3)', color: 'var(--bg)', fontSize: '9px', fontWeight: 700, border: 'none', cursor: 'pointer', flexShrink: 0, lineHeight: 1 }}
        onMouseEnter={open}
        onMouseLeave={() => setVisible(false)}
        onFocus={open}
        onBlur={() => setVisible(false)}
        onClick={() => { if (visible) setVisible(false); else open() }}
      >?</button>
      {visible && (
        <span style={{ position: 'absolute', top: '50%', left: side === 'right' ? 'calc(100% + 8px)' : 'auto', right: side === 'left' ? 'calc(100% + 8px)' : 'auto', transform: 'translateY(-50%)', background: 'var(--surface, var(--bg))', border: '1px solid var(--border)', borderRadius: 'var(--ui-radius)', padding: '8px 11px', fontSize: '12px', fontFamily: 'var(--ui-font)', color: 'var(--text-2)', width: '260px', zIndex: 9999, lineHeight: 1.5, boxShadow: '0 4px 20px rgba(0,0,0,0.22)', pointerEvents: 'none' }}>
          {text.split('\n').map((line, index) => <span key={index} style={{ display: 'block' }}>{line}</span>)}
        </span>
      )}
    </span>
  )
}

function ToggleField({ checked, disabled, onChange }: { checked: boolean; disabled?: boolean; onChange: (value: boolean) => void }) {
  return (
    <label class="toggle">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange((e.target as HTMLInputElement).checked)} />
      <span class="toggle-track" />
    </label>
  )
}

function ModelSelectRow({
  label,
  fieldId,
  sublabel,
  selectValue,
  onSelect,
  autoLabel,
  options,
  optionFormatter,
  itemCountLabel,
  status,
}: {
  label: string
  fieldId: string
  sublabel: string
  selectValue: string
  onSelect: (value: string) => void
  autoLabel: string
  options: AIModelRecord[]
  optionFormatter: (model: AIModelRecord) => string
  itemCountLabel?: string
  status?: preact.ComponentChild
}) {
  return (
    <SettingsRow label={label} sublabel={sublabel} fieldId={fieldId} status={status}>
      <div class="ai-provider-model-stack">
        <select id={fieldId} class="input" value={selectValue} onChange={(e) => onSelect((e.target as HTMLSelectElement).value)}>
          <option value="">{autoLabel}</option>
          {options.map((model) => (
            <option key={model.id} value={model.id}>{optionFormatter(model)}</option>
          ))}
        </select>
        {itemCountLabel && (
          <div class="ai-provider-model-meta">
            <span>{itemCountLabel}</span>
          </div>
        )}
      </div>
    </SettingsRow>
  )
}
