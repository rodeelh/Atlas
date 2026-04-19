import { useEffect, useState, useRef } from 'preact/hooks'
import { api, type EngineStatus, type EngineModelInfo, type RuntimeConfig } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { ErrorBanner } from '../components/ErrorBanner'
import { parseModelInfo } from '../modelName'
import { ConfirmDialog } from '../components/ConfirmDialog'

// Pinned default — must match Atlas/Makefile LLAMA_VERSION
const PINNED_VERSION = 'b8641'

// ── Curated starter models ────────────────────────────────────────────────────
const PRIMARY_MODELS = [
  {
    label: 'Gemma 4 E4B  (Q4_K_M · ~2.5 GB · 8 GB+ RAM)',
    filename: 'google_gemma-4-E4B-it-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/google_gemma-4-E4B-it-GGUF/resolve/main/google_gemma-4-E4B-it-Q4_K_M.gguf',
  },
  {
    label: 'Qwen 3 8B  (Q4_K_M · ~5.2 GB · 16 GB+ RAM)',
    filename: 'Qwen_Qwen3-8B-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/Qwen_Qwen3-8B-GGUF/resolve/main/Qwen_Qwen3-8B-Q4_K_M.gguf',
  },
  {
    label: 'Gemma 3 12B  (Q4_K_M · ~7.5 GB · 16 GB+ RAM)',
    filename: 'google_gemma-3-12b-it-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/google_gemma-3-12b-it-GGUF/resolve/main/google_gemma-3-12b-it-Q4_K_M.gguf',
  },
  {
    label: 'Gemma 4 26B MoE  (Q4_K_M · ~14 GB · 24 GB+ RAM)',
    filename: 'google_gemma-4-26B-A4B-it-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/google_gemma-4-26B-A4B-it-GGUF/resolve/main/google_gemma-4-26B-A4B-it-Q4_K_M.gguf',
  },
  {
    label: 'Qwen 3.5 27B  (Q4_K_M · ~17 GB · 32 GB+ RAM)',
    filename: 'Qwen_Qwen3.5-27B-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/Qwen_Qwen3.5-27B-GGUF/resolve/main/Qwen_Qwen3.5-27B-Q4_K_M.gguf',
  },
]

const ROUTER_MODELS = [
  {
    label: 'Qwen 3 1.7B  (Q4_K_M · ~1.3 GB · recommended)',
    filename: 'Qwen_Qwen3-1.7B-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/Qwen_Qwen3-1.7B-GGUF/resolve/main/Qwen_Qwen3-1.7B-Q4_K_M.gguf',
  },
  {
    label: 'Ministral 3B  (Q4_K_M · ~2.2 GB · agentic)',
    filename: 'mistralai_Ministral-3-3B-Instruct-2512-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/mistralai_Ministral-3-3B-Instruct-2512-GGUF/resolve/main/mistralai_Ministral-3-3B-Instruct-2512-Q4_K_M.gguf',
  },
  {
    label: 'Gemma 3 1B  (Q4_K_M · ~0.8 GB · lightest)',
    filename: 'google_gemma-3-1b-it-Q4_K_M.gguf',
    url: 'https://huggingface.co/bartowski/google_gemma-3-1b-it-GGUF/resolve/main/google_gemma-3-1b-it-Q4_K_M.gguf',
  },
]

const ALL_STARTER_MODELS = [...PRIMARY_MODELS, ...ROUTER_MODELS]

/** Extract model family for same-family filtering.
 *  "google_gemma-4-E4B-it-Q4_K_M.gguf" → "gemma"
 *  "gemma-4-E2B-it-UD-IQ2_M.gguf"      → "gemma"
 *  "Qwen_Qwen3-1.7B-Q4_K_M.gguf"       → "qwen"
 */
function modelFamily(filename: string): string {
  const base = filename.replace(/\.gguf$/i, '').toLowerCase()
  // Strip quantization suffix (e.g. "-Q4_K_M", "-IQ2_M", "-UD-IQ2_M")
  const noQuant = base.replace(/[-_](q\d|iq\d|ud).*$/i, '')
  // Split on underscore — HuggingFace org prefix is before the first underscore
  const parts = noQuant.split('_')
  // Take the part after the org prefix if it looks like "org_model" (e.g. "google_gemma")
  // Otherwise take the first part (e.g. "gemma" from "gemma-4-E2B-it")
  const modelPart = parts.length >= 2 ? parts[1] : parts[0]
  // Family is the first token before a dash/dot/number boundary
  return modelPart.split(/[-._]/)[0]
}

// Module-level TPS history — persists across SPA navigation (component unmount/remount).
// Only cleared when the model is ejected (running transitions false→true on next start).
// Fixed-slot ring buffer: MAX_TPS_SLOTS at 1s polling = 30 seconds of graph data.
const MAX_TPS_SLOTS = 30

let _tpsHistory: number[] = []
let _wasRunning           = false

type DownloadState = {
  filename: string
  downloaded: number
  total: number
  percent: number
  done: boolean
  error: string | null
}

type UpdateState = {
  version: string
  downloaded: number
  total: number
  percent: number
  done: boolean
  error: string | null
}

const CTX_SIZE_OPTIONS = [4096, 8192, 16384, 32768, 65536, 131072, 262144]
const KV_CACHE_QUANT_OPTIONS = [
  { value: 'f32', label: 'f32', detail: 'full precision' },
  { value: 'f16', label: 'f16', detail: 'half precision' },
  { value: 'bf16', label: 'bf16', detail: 'bfloat16' },
  { value: 'q8_0', label: 'q8_0', detail: '8-bit' },
  { value: 'q5_1', label: 'q5_1', detail: '5-bit high accuracy' },
  { value: 'q5_0', label: 'q5_0', detail: '5-bit balanced' },
  { value: 'q4_1', label: 'q4_1', detail: '4-bit higher accuracy' },
  { value: 'q4_0', label: 'q4_0', detail: '4-bit smallest' },
  { value: 'iq4_nl', label: 'iq4_nl', detail: '4-bit nonlinear' },
] as const

export function AtlasEngine({ hidePageHeader = false }: { hidePageHeader?: boolean } = {}) {
  const [status, setStatus]   = useState<EngineStatus | null>(null)
  const [models, setModels]   = useState<EngineModelInfo[]>([])
  const [error, setError]     = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [acting, setActing]   = useState(false)
  const [isMobile, setIsMobile] = useState(() => window.innerWidth <= 480)

  // Context size + KV cache quant — read from config, editable here
  const [ctxSize, setCtxSize]             = useState(8192)
  const [ctxSizeSaving, setCtxSizeSaving] = useState(false)
  const [serverPort, setServerPort] = useState(11985)
  const [serverPortSaving, setServerPortSaving] = useState(false)
  const [kvQuant, setKvQuant]             = useState('q4_0')
  const [kvQuantSaving, setKvQuantSaving] = useState(false)
  const [mlock, setMlock]                 = useState(true)
  const [mlockSaving, setMlockSaving]     = useState(false)
  const [draftModel, setDraftModel]       = useState('')
  const [draftModelSaving, setDraftModelSaving] = useState(false)

  // Tool Router (Phase 3)
  const [routerStatus, setRouterStatus]     = useState<EngineStatus | null>(null)
  const [routerModel, setRouterModel]       = useState('')
  const [routerModelSaving, setRouterModelSaving] = useState(false)

  // Embedding Sidecar
  const [embedStatus, setEmbedStatus]       = useState<any | null>(null)
  const [embedEnabled, setEmbedEnabled]     = useState(false)
  const [embedEnabledSaving, setEmbedEnabledSaving] = useState(false)

  const [tpsHistory, setTpsHistory]             = useState<number[]>(_tpsHistory)
  const wasRunningRef = useRef(_wasRunning)

  const [dlURL, setDlURL]           = useState('')
  const [dlFilename, setDlFilename] = useState('')
  const [dlPreset, setDlPreset]     = useState('')
  const [download, setDownload]     = useState<DownloadState | null>(null)
  const dlAbortRef = useRef<(() => void) | null>(null)

  const [update, setUpdate]         = useState<UpdateState | null>(null)
  const updateAbortRef = useRef<(() => void) | null>(null)
  const [latestVersion, setLatestVersion] = useState<string | null>(null)
  const [versionCheckFailed, setVersionCheckFailed] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)

  const load = async () => {
    try {
      const [s, m, rs] = await Promise.all([
        api.engineStatus(),
        api.engineModels(),
        api.engineRouterStatus().catch(() => null),
      ])
      setStatus(s); setModels(m); setError(null)
      if (rs) setRouterStatus(rs)
      if (s.running) {
        if (!wasRunningRef.current) {
          _tpsHistory = []
          setTpsHistory([])
        }
        wasRunningRef.current = true; _wasRunning = true
      } else {
        wasRunningRef.current = false; _wasRunning = false
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load engine status.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    // Reset module-level TPS history on every mount so stale data from a
    // previous render cycle doesn't bleed through after SPA navigation.
    _tpsHistory.splice(0)
    _wasRunning = false
    wasRunningRef.current = false
    setTpsHistory([])

    void load()
    const interval = setInterval(load, 4000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    const onResize = () => setIsMobile(window.innerWidth <= 480)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // Fast 1s TPS poll — separate from the 4s status poll to keep the graph smooth.
  useEffect(() => {
    let consecutiveFailures = 0
    const MAX_POLL_FAILURES = 3
    const tpsPoll = setInterval(async () => {
      if (!wasRunningRef.current) return
      try {
        const s = await api.engineStatus()
        consecutiveFailures = 0
        if (!s.running || (s.activeRequests ?? 0) === 0) return
        if ((s.lastTPS ?? 0) > 0) {
          setTpsHistory(h => { const n = [...h.slice(-(MAX_TPS_SLOTS - 1)), s.lastTPS!]; _tpsHistory = n; return n })
        }
      } catch {
        consecutiveFailures++
        if (consecutiveFailures >= MAX_POLL_FAILURES) {
          setError('Lost connection to engine.')
        }
      }
    }, 1000)
    return () => clearInterval(tpsPoll)
  }, [])

  // Load ctx size, KV quant + router model from config once on mount
  useEffect(() => {
    api.config().then(cfg => {
      if (cfg.atlasEnginePort && cfg.atlasEnginePort > 0) setServerPort(cfg.atlasEnginePort)
      if (cfg.atlasEngineCtxSize && cfg.atlasEngineCtxSize > 0) setCtxSize(cfg.atlasEngineCtxSize)
      if (cfg.atlasEngineKVCacheQuant) setKvQuant(cfg.atlasEngineKVCacheQuant)
      if (cfg.atlasEngineMlock !== undefined) setMlock(cfg.atlasEngineMlock)
      if (cfg.atlasEngineRouterModel) setRouterModel(cfg.atlasEngineRouterModel)
      if (cfg.atlasEngineDraftModel) setDraftModel(cfg.atlasEngineDraftModel)
      if (cfg.atlasEmbedEnabled !== undefined) setEmbedEnabled(!!cfg.atlasEmbedEnabled)
    }).catch(() => {})

    api.engineEmbedStatus().then((s: any) => setEmbedStatus(s)).catch(() => {})

    // Fetch latest llama.cpp release tag from GitHub.
    fetch('https://api.github.com/repos/ggml-org/llama.cpp/releases/latest')
      .then(r => r.json())
      .then(d => { if (d.tag_name) { setLatestVersion(d.tag_name); setVersionCheckFailed(false) } })
      .catch(() => { setVersionCheckFailed(true) })

    // Restore download state after page refresh or SPA navigation.
    // The server tracks the last known download progress even after interruption.
    api.engineDownloadStatus().then(s => {
      if (!s.filename) return
      // Only restore if download wasn't already completed (percent < 100 guard)
      if (s.percent >= 100) return
      setDownload({
        filename: s.filename,
        downloaded: s.downloaded,
        total: s.total,
        percent: s.percent,
        done: false,
        error: s.active ? null : 'paused',
      })
      // Pre-fill filename + URL so Resume works
      setDlFilename(s.filename)
      if (s.url) setDlURL(s.url)
    }).catch(() => {})
  }, [])

  const handleCtxSizeChange = async (newSize: number) => {
    if (!Number.isFinite(newSize) || newSize < 512) {
      setError('Context size must be at least 512 tokens.')
      return
    }
    setCtxSize(newSize)
    setCtxSizeSaving(true)
    try {
      await api.updateConfig({ atlasEngineCtxSize: newSize } as Partial<RuntimeConfig>)
    } catch {
      // best-effort
    } finally {
      setCtxSizeSaving(false)
    }
  }

  const handleServerPortChange = async (newPort: number) => {
    if (!Number.isFinite(newPort) || newPort < 1024 || newPort > 65535) return
    setServerPort(newPort)
    setServerPortSaving(true)
    try {
      await api.updateConfig({ atlasEnginePort: newPort } as Partial<RuntimeConfig>)
    } catch {
      // best-effort
    } finally {
      setServerPortSaving(false)
    }
  }

  const handleKvQuantChange = async (quant: string) => {
    setKvQuant(quant)
    setKvQuantSaving(true)
    try {
      await api.updateConfig({ atlasEngineKVCacheQuant: quant } as Partial<RuntimeConfig>)
    } catch {
      // best-effort
    } finally {
      setKvQuantSaving(false)
    }
  }

  const handleMlockChange = async (enabled: boolean) => {
    setMlock(enabled)
    setMlockSaving(true)
    try {
      await api.updateConfig({ atlasEngineMlock: enabled } as Partial<RuntimeConfig>)
    } catch {
      // best-effort
    } finally {
      setMlockSaving(false)
    }
  }

  const handleDraftModelChange = async (model: string) => {
    setDraftModel(model)
    setDraftModelSaving(true)
    try { await api.updateConfig({ atlasEngineDraftModel: model } as Partial<RuntimeConfig>) }
    catch { /* best-effort */ }
    finally { setDraftModelSaving(false) }
  }

  const handleRouterModelChange = async (model: string) => {
    setRouterModel(model)
    setRouterModelSaving(true)
    setError(null)
    try {
      await api.updateConfig({ atlasEngineRouterModel: model } as any)
      if (model) {
        // Start (or restart) router with the selected model
        setRouterStatus(await api.engineRouterStart(model))
      } else {
        // "Off" — stop the router
        if (routerStatus?.running) {
          setRouterStatus(await api.engineRouterStop())
        }
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Router operation failed.')
    } finally {
      setRouterModelSaving(false)
    }
  }

  const handleEmbedEnabledChange = async (enabled: boolean) => {
    setEmbedEnabled(enabled)
    setEmbedEnabledSaving(true)
    try {
      await api.updateConfig({ atlasEmbedEnabled: enabled } as any)
      if (enabled) {
        await api.engineEmbedStart()
        setEmbedStatus(await api.engineEmbedStatus())
      } else {
        await api.engineEmbedStop().catch(() => {})
        setEmbedStatus(await api.engineEmbedStatus())
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Embed sidecar operation failed.')
    } finally { setEmbedEnabledSaving(false) }
  }

  const handleStart = async (modelName: string) => {
    setActing(true); setError(null)
    try { setStatus(await api.engineStart(modelName, undefined, ctxSize)) }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to load model.') }
    finally { setActing(false) }
  }

  const handleStop = async () => {
    setActing(true); setError(null)
    try { setStatus(await api.engineStop()) }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to eject model.') }
    finally { setActing(false) }
  }

  const handleDelete = (name: string) => {
    setPendingDelete(name)
  }

  const confirmDelete = async () => {
    if (!pendingDelete) return
    const name = pendingDelete
    setPendingDelete(null)
    setError(null)
    try { setModels(await api.engineDeleteModel(name)) }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to delete model.') }
  }

  const handlePresetChange = (preset: string) => {
    setDlPreset(preset)
    const found = ALL_STARTER_MODELS.find(m => m.filename === preset)
    if (found) { setDlURL(found.url); setDlFilename(found.filename) }
  }

  const handleDownload = async () => {
    if (!dlURL || !dlFilename) return
    setDownload({ filename: dlFilename, downloaded: 0, total: 0, percent: 0, done: false, error: null })
    setError(null)

    const controller = new AbortController()
    dlAbortRef.current = () => controller.abort()

    try {
      const resp = await fetch(`${api.engineDownloadBaseURL()}/engine/models/download`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: dlURL, filename: dlFilename }),
        signal: controller.signal,
      })
      if (!resp.ok || !resp.body) throw new Error(`Server returned ${resp.status}`)

      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const parts = buffer.split('\n\n')
        buffer = parts.pop() ?? ''
        for (const part of parts) {
          const eventLine = part.split('\n').find(l => l.startsWith('event:'))
          const dataLine  = part.split('\n').find(l => l.startsWith('data:'))
          if (!eventLine || !dataLine) continue
          const event = eventLine.slice(7).trim()
          const data  = JSON.parse(dataLine.slice(5).trim())
          if (event === 'progress') {
            setDownload(prev => prev ? { ...prev, ...data } : null)
          } else if (event === 'done') {
            setDownload(prev => prev ? { ...prev, done: true, percent: 100 } : null)
            if (data.models) setModels(data.models)
            setDlURL(''); setDlFilename(''); setDlPreset('')
            dlAbortRef.current = null
          } else if (event === 'error') {
            setDownload(prev => prev ? { ...prev, error: data.message } : null)
            dlAbortRef.current = null
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        setDownload(prev => prev ? { ...prev, error: e instanceof Error ? e.message : 'Download failed' } : null)
      }
      dlAbortRef.current = null
    }
  }

  const handleCancelDownload = () => {
    dlAbortRef.current?.()
    dlAbortRef.current = null
    setDownload(prev => prev ? { ...prev, error: 'paused' } : null)
  }

  const handleDismissDownload = () => {
    setDownload(null)
    api.engineDismissDownload().catch(() => {})
  }

  const handleUpdate = async (version: string) => {
    setUpdate({ version, downloaded: 0, total: 0, percent: 0, done: false, error: null })
    setError(null)

    const controller = new AbortController()
    updateAbortRef.current = () => controller.abort()

    try {
      const resp = await fetch(`${api.engineUpdateBaseURL()}/engine/update`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version }),
        signal: controller.signal,
      })
      if (!resp.ok || !resp.body) throw new Error(`Server returned ${resp.status}`)

      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const parts = buffer.split('\n\n')
        buffer = parts.pop() ?? ''
        for (const part of parts) {
          const eventLine = part.split('\n').find(l => l.startsWith('event:'))
          const dataLine  = part.split('\n').find(l => l.startsWith('data:'))
          if (!eventLine || !dataLine) continue
          const event = eventLine.slice(7).trim()
          const data  = JSON.parse(dataLine.slice(5).trim())
          if (event === 'progress') {
            setUpdate(prev => prev ? { ...prev, ...data } : null)
          } else if (event === 'done') {
            setUpdate(prev => prev ? { ...prev, done: true, percent: 100 } : null)
            if (data.status) setStatus(data.status)
            updateAbortRef.current = null
          } else if (event === 'error') {
            setUpdate(prev => prev ? { ...prev, error: data.message } : null)
            updateAbortRef.current = null
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        setUpdate(prev => prev ? { ...prev, error: e instanceof Error ? e.message : 'Update failed' } : null)
      }
      updateAbortRef.current = null
    }
  }

  const handleCancelUpdate = () => {
    updateAbortRef.current?.()
    setUpdate(null)
  }

  const isRunning       = status?.running ?? false
  const isLoading       = status?.loading ?? false
  const binaryReady     = status?.binaryReady ?? false
  const buildVersion    = status?.buildVersion ?? ''
  const isDownloading   = !!download && !download.done && !download.error
  const isUpdating      = !!update && !update.done && !update.error
  const targetVersion   = latestVersion ?? PINNED_VERSION
  const isUpToDate      = buildVersion === targetVersion

  if (loading) {
    return (
      <div class="screen">
        {!hidePageHeader && <PageHeader title="Llama" subtitle="Built-in local inference — no external tools required." />}
        <PageSpinner />
      </div>
    )
  }

  return (
    <div class="screen">
      {!hidePageHeader && <PageHeader title="Llama" subtitle="Built-in local inference — no external tools required." />}

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* ── Performance Status Board ────────────────────────────────────────── */}
      <div style={{ display: 'flex', flexDirection: isMobile ? 'column' : 'row', gap: '12px', alignItems: 'stretch' }}>

        {/* Stat cells */}
        <div class="card" style={{ flex: 1 }}>
          <div class="card-header">
            <span class="card-title">Performance</span>
            {isRunning && isLoading && <span class="badge badge-yellow" style={{ fontSize: 11 }}>Loading</span>}
            {isRunning && !isLoading && <span class="badge badge-green" style={{ fontSize: 11 }}>Live</span>}
          </div>
          <div class="stat-grid">
            <div class="stat-cell">
              <div class="stat-label">Decode Speed</div>
              <div class="stat-value">
                {isRunning && (status?.lastTPS ?? 0) > 0
                  ? `${status!.lastTPS!.toFixed(1)}`
                  : '—'}
              </div>
              <div class="stat-note">{isRunning && (status?.lastTPS ?? 0) > 0 ? 'tok / sec' : isLoading ? 'loading model…' : 'not running'}</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Prompt Speed</div>
              <div class="stat-value">
                {isRunning && (status?.promptTPS ?? 0) > 0
                  ? `${status!.promptTPS!.toFixed(1)}`
                  : '—'}
              </div>
              <div class="stat-note">{isRunning && (status?.promptTPS ?? 0) > 0 ? 'tok / sec' : 'prompt eval'}</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Active Slots</div>
              <div class="stat-value">
                {isRunning ? String(status?.activeRequests ?? 0) : '—'}
              </div>
              <div class="stat-note">in-flight</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Context Tokens</div>
              <div class="stat-value">
                {isRunning && (status?.contextTokens ?? 0) > 0
                  ? String(status!.contextTokens)
                  : '—'}
              </div>
              <div class="stat-note">tokens in context</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Generation Time</div>
              <div class="stat-value">
                {isRunning && (status?.genTimeSec ?? 0) > 0
                  ? status!.genTimeSec! >= 60
                    ? `${(status!.genTimeSec! / 60).toFixed(1)}m`
                    : `${status!.genTimeSec!.toFixed(1)}s`
                  : '—'}
              </div>
              <div class="stat-note">{isRunning && (status?.genTimeSec ?? 0) > 0 ? 'total' : ''}</div>
            </div>
          </div>
        </div>

        {/* TPS line graph */}
        <div class="card" style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
          <div class="card-header">
            <span class="card-title">Decode TPS</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              {tpsHistory.length > 0 && (
                <span class="stat-note" style={{ marginBottom: 0 }}>
                  peak {Math.max(...tpsHistory).toFixed(1)} · avg {(tpsHistory.reduce((a, b) => a + b, 0) / tpsHistory.length).toFixed(1)}
                </span>
              )}
              {isRunning && isLoading && <span class="badge badge-yellow" style={{ fontSize: 11 }}>Loading</span>}
              {isRunning && !isLoading && <span class="badge badge-green" style={{ fontSize: 11 }}>Live</span>}
            </div>
          </div>
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', padding: '12px 20px 16px' }}>
            {(() => {
              const yLabelW  = 38   // left margin for Y axis labels
              const topPad   = 10  // prevent top label clip
              const botPad   = 6   // prevent bottom "0" label clip
              const rightPad = 16  // prevent right-edge clipping
              const chartW   = 500
              const chartH   = 63
              const totalW   = yLabelW + chartW + rightPad
              const totalH   = topPad + chartH + botPad

              if (!isRunning && tpsHistory.length === 0) {
                return (
                  <div class="empty-state" style={{ padding: '24px 0', minHeight: 'unset' }}>
                    <p style={{ margin: 0, fontSize: 13 }}>Start a model to see live speed</p>
                  </div>
                )
              }

              // Y scale
              const rawMax  = tpsHistory.length > 0 ? Math.max(...tpsHistory) : 10
              const niceMax = Math.ceil(rawMax / 5) * 5 || 10
              const yTicks  = [0, Math.round(niceMax * 0.33), Math.round(niceMax * 0.66), niceMax]

              const toY = (v: number) => topPad + chartH - (v / niceMax) * chartH

              // X axis — fixed-slot strip chart.
              // Data grows left-to-right. Once the buffer is full (MAX_TPS_SLOTS),
              // oldest falls off the left, newest is always at the right edge.
              const toSlotX = (i: number, total: number) => {
                if (total <= 1) return yLabelW
                // If buffer is not full, spread across the proportional width
                const span = total >= MAX_TPS_SLOTS ? chartW : (total - 1) / (MAX_TPS_SLOTS - 1) * chartW
                return yLabelW + (i / (total - 1)) * span
              }

              // Helper: build polyline points for a series by slot index
              const buildPts = (series: number[]) => {
                if (series.length < 2) return ''
                return series.map((v, i) => `${toSlotX(i, series.length).toFixed(1)},${toY(v).toFixed(1)}`).join(' ')
              }

              const decodeN     = tpsHistory.length
              const decodePts   = buildPts(tpsHistory)
              const decodeLastX = decodeN > 0 ? toSlotX(decodeN - 1, decodeN) : yLabelW
              const decodeLastY = decodeN > 0 ? toY(tpsHistory[decodeN - 1]) : topPad + chartH
              const decodeFill  = decodeN >= 2
                ? `${toSlotX(0, decodeN).toFixed(1)},${topPad + chartH} ${decodePts} ${decodeLastX.toFixed(1)},${topPad + chartH}`
                : ''

              const stopped = !isRunning

              return (
                <svg
                  width="100%"
                  viewBox={`0 0 ${totalW} ${totalH}`}
                  preserveAspectRatio="none"
                  style={{ display: 'block', width: '100%', height: `${totalH}px` }}
                >
                  <defs>
                    <linearGradient id="tps-fill" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stop-color="var(--accent)" stop-opacity="0.18" />
                      <stop offset="100%" stop-color="var(--accent)" stop-opacity="0.01" />
                    </linearGradient>
                  </defs>

                  {/* Y gridlines + labels */}
                  {yTicks.map(v => {
                    const y = toY(v)
                    return (
                      <g key={v}>
                        <line x1={yLabelW} y1={y} x2={yLabelW + chartW} y2={y}
                          stroke="var(--theme-border-subtle, #e5e7eb)" stroke-width="1"
                          stroke-dasharray={v === 0 ? 'none' : '3,3'} />
                        <text x={yLabelW - 5} y={y} text-anchor="end"
                          font-size="9" fill="var(--theme-text-muted, #9ca3af)" dominant-baseline="middle">
                          {v}
                        </text>
                      </g>
                    )
                  })}

                  {/* Y axis line */}
                  <line x1={yLabelW} y1={topPad} x2={yLabelW} y2={topPad + chartH}
                    stroke="var(--theme-border-subtle, #e5e7eb)" stroke-width="1" />

                  {/* Decode TPS — fill + line */}
                  {decodeN >= 2 && (
                    <>
                      <polygon points={decodeFill} fill="url(#tps-fill)" />
                      <polyline points={decodePts} fill="none"
                        stroke={stopped ? 'var(--theme-text-muted, #9ca3af)' : 'var(--accent)'}
                        stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round" />
                      {!stopped && <circle cx={decodeLastX} cy={decodeLastY} r="3" fill="var(--accent)" />}
                    </>
                  )}
                  {decodeN === 1 && (
                    <circle cx={decodeLastX} cy={decodeLastY} r="3" fill="var(--accent)" />
                  )}

                  {/* Stopped overlay */}
                  {stopped && tpsHistory.length > 0 && (
                    <text x={yLabelW + chartW / 2} y={topPad + chartH / 2}
                      text-anchor="middle" font-size="11" dominant-baseline="middle"
                      fill="var(--theme-text-muted, #9ca3af)">
                      Model stopped
                    </text>
                  )}
                </svg>
              )
            })()}
          </div>
        </div>

      </div>

      {/* ── Models ─────────────────────────────────────────────────────────── */}
      <div>
        <div class="section-label" style={{ marginBottom: 10 }}>Models</div>

        {!binaryReady && (
          <div class="banner banner-warn" style={{ marginBottom: 12, borderRadius: '6px' }}>
            <span class="banner-message">
              llama-server binary not found. Run <code>make install</code> or <code>make download-engine</code>.
            </span>
          </div>
        )}

        {/* Unified models card */}
        <div class="card">
          {models.length === 0 ? (
            <div style={{ padding: '40px 20px', textAlign: 'center', color: 'var(--theme-text-muted)', fontSize: 13 }}>
              No models downloaded yet — use the section below to get started.
            </div>
          ) : (
            [...models].sort((a, b) => a.sizeBytes - b.sizeBytes).map(m => {
              const isActive = isRunning && status?.loadedModel === m.name
              const { display, quant } = parseModelInfo(m.name)
              return (
                <div key={m.name} class="settings-row engine-model-row">
                  {/* Model info */}
                  <div class="settings-label-col engine-model-summary">
                    <div class="engine-model-heading">
                      <span class="settings-label engine-model-title" style={{ fontFamily: 'var(--font-mono)', fontSize: 13 }}>
                        {display}
                      </span>
                    </div>
                    <div class="settings-sublabel engine-model-meta">
                      {quant && (
                        <span class="badge badge-gray" style={{ fontSize: 11, padding: '1px 6px' }}>{quant}</span>
                      )}
                      {quant && <span class="engine-model-meta-separator">-</span>}
                      <span>{m.sizeHuman}</span>
                      {isActive && isLoading && (
                        <span class="badge badge-yellow" style={{ fontSize: 11, padding: '1px 6px' }}>Loading</span>
                      )}
                      {isActive && !isLoading && (
                        <span class="badge badge-green" style={{ fontSize: 11, padding: '1px 6px' }}>Active</span>
                      )}
                      {isActive && status?.port && (
                        <span class="badge badge-blue" style={{ fontSize: 11, padding: '1px 6px' }}>port {status.port}</span>
                      )}
                    </div>
                    {isActive && status?.lastError && (
                      <div class="engine-model-error" style={{ fontSize: 11.5, color: 'var(--theme-text-danger, #e05252)', marginTop: 3 }}>
                        {status.lastError}
                      </div>
                    )}
                  </div>

                  {/* Actions */}
                  <div class="engine-model-controls">
                    <div class="settings-field engine-model-actions" style={{ gap: 6, flexShrink: 0 }}>
                      {isActive ? (
                        <button class="btn btn-sm engine-model-action-btn" onClick={handleStop} disabled={acting}>
                          {acting ? '…' : 'Eject'}
                        </button>
                      ) : (
                        <button
                          class="btn btn-sm btn-primary engine-model-action-btn"
                          onClick={() => handleStart(m.name)}
                          disabled={acting || !binaryReady}
                        >
                          {acting ? '…' : 'Load'}
                        </button>
                      )}
                      <button
                        class="btn btn-sm btn-danger engine-model-action-btn"
                        onClick={() => handleDelete(m.name)}
                        disabled={acting || isActive}
                        title={isActive ? 'Eject the model before deleting' : `Delete ${m.name}`}
                      >
                        Delete
                      </button>
                    </div>
                  </div>
                </div>
              )
            })
          )}
        </div>
      </div>

      {/* ── Tool Router ────────────────────────────────────────────────────── */}
      <div>
        <div class="section-label" style={{ marginBottom: 10 }}>Tool Router</div>
        <div class="card">
          <div class="settings-row" style={{ borderBottom: 'none' }}>
            <div class="settings-label-col">
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <span class="settings-label">Router model</span>
                {routerStatus?.running && (
                  <span class="badge badge-green" style={{ fontSize: 11, padding: '1px 6px' }}>Active</span>
                )}
                {routerStatus?.running && routerStatus.port && (
                  <span class="badge badge-blue" style={{ fontSize: 11, padding: '1px 6px' }}>port {routerStatus.port}</span>
                )}
              </div>
              <div class="settings-sublabel" style={{ marginTop: 3 }}>
                Small model used when Tool Selection is set to <strong>AI Router</strong>.
                Select a downloaded model — Gemma 4 2B recommended.
                Auto-starts when a chat turn needs it.
              </div>
            </div>
            <div class="settings-field">
              <select
                class="input"
                value={routerModel}
                onChange={e => handleRouterModelChange((e.target as HTMLSelectElement).value)}
                disabled={routerModelSaving}
              >
                <option value="">Off</option>
                {[...models].sort((a, b) => a.sizeBytes - b.sizeBytes).map(m => {
                  const { display, quant } = parseModelInfo(m.name)
                  return (
                    <option key={m.name} value={m.name}>
                      {display}{quant ? ` · ${quant}` : ''} · {m.sizeHuman}
                    </option>
                  )
                })}
              </select>
            </div>
          </div>
        </div>
      </div>

      {/* ── Embedding Sidecar card ─────────────────────────────────────────── */}
      <div>
        <div class="section-label">Embedding Sidecar</div>
        <div class="card">
          <div class="settings-row" style={{ borderBottom: 'none' }}>
            <div class="settings-label-col">
              <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                Local embedding
                {embedStatus && (() => {
                  if (embedStatus.running)
                    return <span style={{ fontSize: 11, fontWeight: 500, padding: '2px 7px', borderRadius: 10, background: 'color-mix(in srgb, var(--accent) 15%, transparent)', color: 'var(--accent)' }}>Running</span>
                  if (embedStatus.binaryReady && embedStatus.modelReady)
                    return <span style={{ fontSize: 11, fontWeight: 500, padding: '2px 7px', borderRadius: 10, background: 'var(--surface-2)', color: 'var(--text-2)' }}>Ready</span>
                  if (!embedStatus.binaryReady)
                    return <span style={{ fontSize: 11, fontWeight: 500, padding: '2px 7px', borderRadius: 10, background: 'color-mix(in srgb, var(--warning, #f59e0b) 15%, transparent)', color: 'var(--warning, #f59e0b)' }}>Engine not installed</span>
                  return <span style={{ fontSize: 11, fontWeight: 500, padding: '2px 7px', borderRadius: 10, background: 'color-mix(in srgb, var(--warning, #f59e0b) 15%, transparent)', color: 'var(--warning, #f59e0b)' }}>Model not found</span>
                })()}
              </div>
              <div class="settings-sublabel">
                Uses <strong>nomic-embed-text-v1.5</strong> for local memory embeddings — no API cost, works with any provider.
              </div>
            </div>
            <div class="settings-field">
              <label class="toggle">
                <input
                  type="checkbox"
                  checked={embedEnabled}
                  disabled={embedEnabledSaving || !embedStatus?.binaryReady}
                  onChange={e => handleEmbedEnabledChange((e.target as HTMLInputElement).checked)}
                />
                <span class="toggle-track" />
              </label>
            </div>
          </div>
        </div>
      </div>

      {/* ── Configuration card ─────────────────────────────────────────────── */}
      <div>
        <div class="section-label">Configuration</div>
        <div class="card">

          {/* Context window size */}
          <div class="settings-row">
            <div class="settings-label-col">
              <div class="settings-label">Context size</div>
              <div class="settings-sublabel">
                KV-cache token limit passed to llama-server via --ctx-size.
                Larger values use more VRAM/RAM. Restart the model after changing.
              </div>
            </div>
            <div class="settings-field">
              <select
                class="input"
                value={ctxSize}
                onChange={e => handleCtxSizeChange(parseInt((e.target as HTMLSelectElement).value))}
                disabled={ctxSizeSaving}
              >
                {CTX_SIZE_OPTIONS.map(n => (
                  <option key={n} value={n}>
                    {n >= 1024 ? `${n / 1024}K tokens` : `${n} tokens`}
                  </option>
                ))}
              </select>
            </div>
          </div>

          {/* KV cache quantisation */}
          <div class="settings-row">
            <div class="settings-label-col">
              <div class="settings-label">KV cache quantisation</div>
              <div class="settings-sublabel">
                Precision for the attention key/value cache during inference (-ctk/-ctv).
                Restart the model after changing.
              </div>
            </div>
            <div class="settings-field">
              <select
                class="input"
                value={kvQuant}
                onChange={e => handleKvQuantChange((e.target as HTMLSelectElement).value)}
                disabled={kvQuantSaving}
              >
                {KV_CACHE_QUANT_OPTIONS.map((option) => (
                  <option value={option.value} key={option.value}>
                    {option.label} — {option.detail}
                  </option>
                ))}
              </select>
            </div>
          </div>

          {/* Speculative decoding */}
          <div class={`settings-row${isMobile ? ' settings-row-mobile' : ''}`}>
            <div class="settings-label-col">
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <div class="settings-label">Speculative decoding</div>
                {draftModel && (
                  <span class="badge badge-blue" style={{ fontSize: 11, padding: '1px 6px' }}>
                    {parseModelInfo(draftModel).display}
                  </span>
                )}
              </div>
              <div class="settings-sublabel">
                Uses a smaller same-family model to draft tokens that the primary model validates in bulk.
                Can improve decode speed 1.5–2x. Both models load into RAM simultaneously.
                Restart the model after changing.
              </div>
            </div>
            <div class={`settings-field${isMobile ? ' settings-field-mobile' : ''}`}>
              <select
                class="input"
                value={draftModel}
                onChange={e => handleDraftModelChange((e.target as HTMLSelectElement).value)}
                disabled={draftModelSaving}
              >
                <option value="">Off</option>
                {models
                  .filter(m => {
                    const loaded = status?.loadedModel
                    if (!loaded) return true
                    if (m.name === loaded) return false
                    return modelFamily(m.name) === modelFamily(loaded)
                  })
                  .map(m => {
                    const { display, quant } = parseModelInfo(m.name)
                    return (
                      <option key={m.name} value={m.name}>
                        {display}{quant ? ` · ${quant}` : ''} · {m.sizeHuman}
                      </option>
                    )
                  })}
              </select>
            </div>
          </div>

          {/* Pin model in RAM */}
          <div class={`settings-row engine-inline-control-row${isMobile ? ' settings-row-mobile' : ''}`}>
            <div class="settings-label-col">
              <div class="settings-label">Pin model in RAM</div>
              <div class="settings-sublabel">
                Lock model weights in physical memory (--mlock) to prevent the OS from
                paging them to disk under memory pressure. Prevents random TPS drops but
                uses more RAM. Disable on 16 GB machines running large models. Restart the model after changing.
              </div>
            </div>
            <div class={`settings-field engine-inline-control-field${isMobile ? ' settings-field-mobile settings-field-mobile-toggle' : ''}`} style={isMobile ? undefined : { flex: '0 0 160px' }}>
              <label class="toggle">
                <input
                  type="checkbox"
                  checked={mlock}
                  onChange={e => handleMlockChange((e.target as HTMLInputElement).checked)}
                  disabled={mlockSaving}
                />
                <span class="toggle-track" />
              </label>
            </div>
          </div>

          {/* Server port */}
          <div class="settings-row">
            <div class="settings-label-col">
              <div class="settings-label">Server Port</div>
              <div class="settings-sublabel">
                Port Llama listens on (managed by Atlas). Restart daemon after changing.
              </div>
            </div>
            <div class="settings-field">
              <input
                class="input input-sm"
                type="number"
                min={1024}
                max={65535}
                value={serverPort}
                onChange={e => handleServerPortChange(Number((e.target as HTMLInputElement).value))}
                disabled={serverPortSaving}
              />
            </div>
          </div>

          {/* llama-server version + update — merged row */}
          <div class={`settings-row engine-inline-control-row${isMobile ? ' settings-row-mobile' : ''}`} style={{ borderBottom: 'none' }}>
            <div class="settings-label-col">
              <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                llama-server
                {binaryReady && !isUpToDate && (
                  <span class="badge badge-yellow" style={{ fontSize: 11, padding: '1px 6px' }}>Update</span>
                )}
              </div>
              <div class="settings-sublabel">
                {binaryReady
                  ? isUpToDate
                    ? `Up to date — latest release ${targetVersion}.`
                    : `Update available — latest: ${targetVersion}`
                  : 'Not installed — run make install or make download-engine'}
                {versionCheckFailed && (
                  <span style={{ marginLeft: 6, color: 'var(--theme-text-muted)' }}>
                    (Version check unavailable)
                  </span>
                )}
              </div>
            </div>
            <div class={`settings-field engine-inline-control-field${isMobile ? ' settings-field-mobile' : ''}`} style={isMobile ? undefined : { flexShrink: 0 }}>
              {isUpdating ? (
                <button class="btn btn-sm" onClick={handleCancelUpdate}>Cancel</button>
              ) : (
                <button
                  class="btn btn-sm btn-primary"
                  onClick={() => handleUpdate(targetVersion)}
                  disabled={isUpdating}
                >
                  {isUpToDate ? 'Reinstall' : 'Update'}
                </button>
              )}
            </div>
          </div>

          {/* Update progress */}
          {(isUpdating || update?.done || update?.error) && (
            <div style={{ borderTop: '1px solid var(--theme-border-subtle)', padding: '14px 20px', display: 'flex', flexDirection: 'column', gap: 10 }}>
              {isUpdating && (
                <div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12.5, color: 'var(--theme-text-secondary)', marginBottom: 7 }}>
                    <span>Downloading llama-server <code style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{update!.version}</code>…</span>
                    <span style={{ color: 'var(--theme-text-muted)' }}>
                      {update!.total > 0
                        ? `${(update!.downloaded / 1e6).toFixed(1)} / ${(update!.total / 1e6).toFixed(1)} MB · ${update!.percent.toFixed(1)}%`
                        : `${(update!.downloaded / 1e6).toFixed(1)} MB`}
                    </span>
                  </div>
                  <div style={{ height: 4, background: 'var(--theme-border-strong)', borderRadius: 2, overflow: 'hidden' }}>
                    <div style={{ height: '100%', width: `${update!.percent}%`, background: 'var(--theme-accent-fill)', borderRadius: 2, transition: 'width 0.25s' }} />
                  </div>
                </div>
              )}
              {update?.done && (
                <div class="banner banner-success" style={{ borderRadius: 6 }}>
                  <span class="banner-message">✓ Engine updated to {update.version}.</span>
                  <button class="banner-dismiss" onClick={() => setUpdate(null)} title="Dismiss">✕</button>
                </div>
              )}
              {update?.error && (
                <div class="banner banner-error" style={{ borderRadius: 6 }}>
                  <span class="banner-message">Update failed: {update.error}</span>
                  <button class="banner-dismiss" onClick={() => setUpdate(null)} title="Dismiss">✕</button>
                </div>
              )}
            </div>
          )}

        </div>
      </div>

      {/* ── Download a model ───────────────────────────────────────────────── */}
      <div>
        <div class="section-label">Download Model</div>
        <div class="card">

          <div class={`settings-row${isMobile ? ' settings-row-mobile' : ''}`}>
            <div class="settings-label-col">
              <div class="settings-label">Starter Models</div>
              <div class="settings-sublabel">Curated GGUF models that run well on Apple Silicon</div>
            </div>
            <div class={`settings-field${isMobile ? ' settings-field-mobile' : ''}`} style={isMobile ? undefined : { flex: '0 0 300px' }}>
              <select
                class="input"
                value={dlPreset}
                onChange={e => handlePresetChange((e.target as HTMLSelectElement).value)}
                disabled={isDownloading}
              >
                <option value="">Choose a model to download</option>
                <optgroup label="Primary Models">
                  {PRIMARY_MODELS.map(m => (
                    <option key={m.filename} value={m.filename}>{m.label}</option>
                  ))}
                </optgroup>
                <optgroup label="Router Models">
                  {ROUTER_MODELS.map(m => (
                    <option key={m.filename} value={m.filename}>{m.label}</option>
                  ))}
                </optgroup>
              </select>
            </div>
          </div>

          <div class={`settings-row${isMobile ? ' settings-row-mobile' : ''}`} style={{ borderBottom: 'none' }}>
            <div class="settings-label-col">
              <div class="settings-label">Download URL</div>
              <div class="settings-sublabel">Direct link to any <code>.gguf</code> file</div>
            </div>
            <div class={`settings-field${isMobile ? ' settings-field-mobile' : ''}`} style={isMobile ? undefined : { flex: '0 0 300px' }}>
              <input
                class="input"
                type="url"
                placeholder="https://huggingface.co/…/model.gguf"
                value={dlURL}
                onInput={e => {
                  const url = (e.target as HTMLInputElement).value
                  setDlURL(url)
                  const name = url.split('/').pop()?.split('?')[0] ?? ''
                  if (name) {
                    setDlFilename(name)
                  } else if (url) {
                    setError('Could not determine filename from URL.')
                  }
                }}
                disabled={isDownloading}
              />
            </div>
          </div>

          {/* Footer: progress + actions */}
          <div style={{ borderTop: '1px solid var(--theme-border-subtle)', padding: '14px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>

            {download && !download.done && download.percent > 0 && (
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12.5, color: 'var(--theme-text-secondary)', marginBottom: 7 }}>
                  <span>
                    {isDownloading
                      ? <>Downloading <code style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{download.filename}</code>…</>
                      : <><code style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{download.filename}</code> — paused</>}
                  </span>
                  <span style={{ color: 'var(--theme-text-muted)' }}>
                    {download.total > 0
                      ? `${(download.downloaded / 1e9).toFixed(2)} / ${(download.total / 1e9).toFixed(2)} GB · ${download.percent.toFixed(1)}%`
                      : `${(download.downloaded / 1e6).toFixed(1)} MB`}
                  </span>
                </div>
                <div style={{ height: 4, background: 'var(--theme-border-strong)', borderRadius: 2, overflow: 'hidden' }}>
                  <div style={{ height: '100%', width: `${download.percent}%`, background: isDownloading ? 'var(--theme-accent-fill)' : 'var(--theme-text-muted)', borderRadius: 2, transition: 'width 0.25s' }} />
                </div>
              </div>
            )}

            {download?.done && (
              <div class="banner banner-success" style={{ borderRadius: 6 }}>
                <span class="banner-message">✓ {download.filename} downloaded successfully.</span>
                <button class="banner-dismiss" onClick={handleDismissDownload} title="Dismiss">✕</button>
              </div>
            )}
            {download?.error && download.error !== 'paused' && (
              <div class="banner banner-error" style={{ borderRadius: 6 }}>
                <span class="banner-message">Download failed: {download.error}</span>
              </div>
            )}

            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              {isDownloading && (
                <button class="btn btn-sm" onClick={handleCancelDownload}>Cancel</button>
              )}
              {!isDownloading && download?.error && (
                <>
                  <button class="btn btn-sm btn-primary" onClick={handleDownload} disabled={!dlURL}>Resume</button>
                  <button class="btn btn-sm" onClick={handleDismissDownload}>Cancel</button>
                </>
              )}
              {!isDownloading && !download?.error && !download?.done && (
                <button
                  class="btn btn-sm btn-primary"
                  onClick={handleDownload}
                  disabled={!dlURL}
                >
                  Download
                </button>
              )}
            </div>

          </div>
        </div>
      </div>

      {pendingDelete && (
        <ConfirmDialog
          title={`Delete ${pendingDelete}?`}
          body="This model file will be permanently removed from disk."
          confirmLabel="Delete"
          danger
          onConfirm={confirmDelete}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </div>
  )
}
