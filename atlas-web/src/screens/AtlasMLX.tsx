import { useEffect, useState, useRef } from 'preact/hooks'
import { api, type MLXStatus, type MLXModelInfo, type RuntimeConfig } from '../api/client'
import type { MLXInferenceStats, MLXSchedulerStats } from '../api/contracts'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import { parseMLXModelInfo } from '../modelName'

// ── Curated MLX-community starter models ─────────────────────────────────────
// All repos are from https://github.com/ml-explore/mlx-lm and hosted under
// the mlx-community organisation on Hugging Face.
const PRIMARY_MODELS = [
  {
    label: 'Llama 3.2 3B Instruct  (4-bit · ~1.8 GB · 8 GB+ RAM)',
    repo: 'mlx-community/Llama-3.2-3B-Instruct-4bit',
  },
  {
    label: 'Qwen 2.5 7B Instruct  (4-bit · ~4.3 GB · 16 GB+ RAM)',
    repo: 'mlx-community/Qwen2.5-7B-Instruct-4bit',
  },
  {
    label: 'Llama 3.1 8B Instruct  (4-bit · ~4.9 GB · 16 GB+ RAM)',
    repo: 'mlx-community/Meta-Llama-3.1-8B-Instruct-4bit',
  },
  {
    label: 'Phi 4 Mini Instruct  (4-bit · ~2.3 GB · 8 GB+ RAM)',
    repo: 'mlx-community/phi-4-mini-instruct-4bit',
  },
  {
    label: 'Mistral 7B Instruct v0.3  (4-bit · ~4.1 GB · 16 GB+ RAM)',
    repo: 'mlx-community/Mistral-7B-Instruct-v0.3-4bit',
  },
]

const ROUTER_MODELS = [
  {
    label: 'Qwen 2.5 0.5B Instruct  (4-bit · ~0.3 GB · lightest)',
    repo: 'mlx-community/Qwen2.5-0.5B-Instruct-4bit',
  },
  {
    label: 'Llama 3.2 1B Instruct  (4-bit · ~0.6 GB · recommended)',
    repo: 'mlx-community/Llama-3.2-1B-Instruct-4bit',
  },
]

const ALL_STARTER_MODELS = [...PRIMARY_MODELS, ...ROUTER_MODELS]

const CTX_SIZE_OPTIONS = [2048, 4096, 8192, 16384, 32768]

// Ring buffer: last 20 inference turns for the TPS graph.
const MAX_TPS_SLOTS = 20
let _mlxTpsHistory: number[] = []

type DownloadState = {
  repo: string
  done: boolean
  error: string | null
  line: string   // last progress line from mlx_lm subprocess
}

type InstallState = {
  done: boolean
  error: string | null
  line: string
}

export function AtlasMLX({ hidePageHeader = false }: { hidePageHeader?: boolean } = {}) {
  const [status, setStatus]     = useState<MLXStatus | null>(null)
  const [models, setModels]     = useState<MLXModelInfo[]>([])
  const [error, setError]       = useState<string | null>(null)
  const [loading, setLoading]   = useState(true)
  const [acting, setActing]     = useState(false)
  const [isMobile, setIsMobile] = useState(() => window.innerWidth <= 480)

  // Config state
  const [ctxSize, setCtxSize]             = useState(4096)
  const [ctxSizeSaving, setCtxSizeSaving] = useState(false)
  const [serverPort, setServerPort]       = useState(11990)
  const [serverPortSaving, setServerPortSaving] = useState(false)

  // Router (MLX-exclusive)
  const [routerStatus, setRouterStatus]   = useState<MLXStatus | null>(null)
  const [routerModel, setRouterModel]     = useState('')
  const [routerModelSaving, setRouterModelSaving] = useState(false)
  const [routerActing, setRouterActing]   = useState(false)

  // Download state
  const [dlRepo, setDlRepo]         = useState('')
  const [dlPreset, setDlPreset]     = useState('')
  const [download, setDownload]     = useState<DownloadState | null>(null)
  const dlAbortRef = useRef<(() => void) | null>(null)

  // Install / upgrade state
  const [install, setInstall]       = useState<InstallState | null>(null)
  const installAbortRef = useRef<(() => void) | null>(null)

  // Per-turn TPS history for the performance graph
  const [tpsHistory, setTpsHistory] = useState<number[]>(_mlxTpsHistory)
  const lastInfRef = useRef<MLXInferenceStats | null>(null)

  const load = async () => {
    try {
      const [s, m, rs] = await Promise.all([
        api.mlxStatus(),
        api.mlxModels(),
        api.mlxRouterStatus().catch(() => null),
      ])
      setStatus(s); setModels(m); setError(null)
      if (rs) setRouterStatus(rs)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load MLX status.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
    const interval = setInterval(load, 4000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    const onResize = () => setIsMobile(window.innerWidth <= 480)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // Load config values on mount
  useEffect(() => {
    api.config().then(cfg => {
      if (cfg.atlasMLXPort && cfg.atlasMLXPort > 0) setServerPort(cfg.atlasMLXPort)
      if (cfg.atlasMLXCtxSize && cfg.atlasMLXCtxSize > 0) setCtxSize(cfg.atlasMLXCtxSize)
      if (cfg.atlasMLXRouterModel) setRouterModel(cfg.atlasMLXRouterModel)
    }).catch(() => {})

    // Restore any in-progress download that survived a page refresh.
    api.mlxDownloadStatus().then(s => {
      if (!s.repo || !s.active) return
      setDownload({ repo: s.repo, done: false, error: null, line: 'Resuming…' })
      setDlRepo(s.repo)
    }).catch(() => {})
  }, [])

  // Append to TPS history when a new inference result arrives from the status poll.
  useEffect(() => {
    const inf = status?.lastInference
    if (!inf || inf.decodeTPS <= 0) return
    if (lastInfRef.current === inf) return // same object reference — no new data
    lastInfRef.current = inf
    setTpsHistory(h => {
      const n = [...h.slice(-(MAX_TPS_SLOTS - 1)), inf.decodeTPS]
      _mlxTpsHistory = n
      return n
    })
  }, [status?.lastInference])

  const handleCtxSizeChange = async (newSize: number) => {
    setCtxSize(newSize)
    setCtxSizeSaving(true)
    try { await api.updateConfig({ atlasMLXCtxSize: newSize } as Partial<RuntimeConfig>) }
    catch { /* best-effort */ }
    finally { setCtxSizeSaving(false) }
  }

  const handleServerPortChange = async (newPort: number) => {
    if (!Number.isFinite(newPort) || newPort < 1024 || newPort > 65535) return
    setServerPort(newPort)
    setServerPortSaving(true)
    try { await api.updateConfig({ atlasMLXPort: newPort } as Partial<RuntimeConfig>) }
    catch { /* best-effort */ }
    finally { setServerPortSaving(false) }
  }

  const handleRouterModelChange = async (model: string) => {
    setRouterModel(model)
    setRouterModelSaving(true)
    setError(null)
    try {
      await api.updateConfig({ atlasMLXRouterModel: model } as Partial<RuntimeConfig>)
      if (model) {
        setRouterStatus(await api.mlxRouterStart(model))
      } else if (routerStatus?.running) {
        setRouterStatus(await api.mlxRouterStop())
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Router operation failed.')
    } finally {
      setRouterModelSaving(false)
    }
  }

  const handleStart = async (modelName: string) => {
    setActing(true); setError(null)
    try { setStatus(await api.mlxStart(modelName, undefined, ctxSize)) }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to load model.') }
    finally { setActing(false) }
  }

  const handleStop = async () => {
    setActing(true); setError(null)
    try { setStatus(await api.mlxStop()) }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to eject model.') }
    finally { setActing(false) }
  }

  const handleDelete = async (name: string) => {
    if (!confirm(`Delete ${name}?`)) return
    setError(null)
    try { setModels(await api.mlxDeleteModel(name)) }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to delete model.') }
  }

  const handlePresetChange = (preset: string) => {
    setDlPreset(preset)
    const found = ALL_STARTER_MODELS.find(m => m.repo === preset)
    if (found) setDlRepo(found.repo)
  }

  // SSE download — POST { repo } → line-by-line progress events
  const handleDownload = async () => {
    const repo = dlRepo.trim()
    // Require "org/model-name" — at least one non-empty word segment on each side of "/".
    if (!repo || !/^[\w][\w.-]*\/[\w][\w.-]*$/.test(repo)) {
      setError('Invalid repo format. Expected: org/model-name (e.g. mlx-community/Llama-3.2-3B-Instruct-4bit)')
      return
    }
    setDownload({ repo, done: false, error: null, line: 'Starting download…' })
    setError(null)

    const controller = new AbortController()
    dlAbortRef.current = () => controller.abort()

    try {
      const resp = await fetch(`${api.mlxDownloadBaseURL()}/engine/mlx/models/download`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ repo }),
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
            setDownload(prev => prev ? { ...prev, line: data.line ?? prev.line } : null)
          } else if (event === 'done') {
            setDownload(prev => prev ? { ...prev, done: true, line: 'Download complete.' } : null)
            if (data.models) setModels(data.models)
            setDlRepo(''); setDlPreset('')
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
    api.mlxDismissDownload().catch(() => {})
  }

  // SSE install/upgrade — POST → line-by-line pip output
  const handleInstall = async () => {
    setInstall({ done: false, error: null, line: 'Starting install…' })
    setError(null)

    const controller = new AbortController()
    installAbortRef.current = () => controller.abort()

    try {
      const resp = await fetch(`${api.mlxInstallBaseURL()}/engine/mlx/install`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
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
            setInstall(prev => prev ? { ...prev, line: data.line ?? prev.line } : null)
          } else if (event === 'done') {
            setInstall(prev => prev ? { ...prev, done: true, line: `Installed — mlx-lm ${data.version ?? ''}` } : null)
            if (data.status) setStatus(data.status)
            installAbortRef.current = null
          } else if (event === 'error') {
            setInstall(prev => prev ? { ...prev, error: data.message } : null)
            installAbortRef.current = null
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        setInstall(prev => prev ? { ...prev, error: e instanceof Error ? e.message : 'Install failed' } : null)
      }
      installAbortRef.current = null
    }
  }

  const handleCancelInstall = () => {
    installAbortRef.current?.()
    setInstall(null)
  }

  const isRunning       = status?.running ?? false
  const isLoading       = status?.loading ?? false
  const venvReady       = status?.venvReady ?? false
  const pkgVersion      = status?.packageVersion ?? ''
  const latestVersion   = status?.latestVersion ?? ''
  const isAppleSilicon  = status?.isAppleSilicon ?? true // optimistic until first load
  const isDownloading   = !!download && !download.done && !download.error
  const isInstalling    = !!install && !install.done && !install.error
  const scheduler: MLXSchedulerStats | null = status?.scheduler ?? null
  const schedulerQueueDepth = scheduler?.queueDepth ?? 0
  const schedulerActive = scheduler?.activeRequests ?? 0
  const schedulerAvgWaitMs = ((scheduler?.avgQueueWaitSec ?? 0) * 1000)

  // Version comparison helpers
  const hasUpgrade = venvReady && pkgVersion && latestVersion && pkgVersion !== latestVersion
  const mlxInstallLabel = !venvReady ? 'Install' : hasUpgrade ? 'Upgrade' : 'Reinstall'
  const mlxVersionNote = !venvReady
    ? `Not installed${latestVersion ? ` — latest: v${latestVersion}` : ''}`
    : hasUpgrade
      ? `v${pkgVersion} installed — upgrade available: v${latestVersion}`
      : pkgVersion
        ? `v${pkgVersion} installed${latestVersion ? ' — up to date' : ''}`
        : 'Installed — version unknown'

  if (loading) {
    return (
      <div class="screen">
        {!hidePageHeader && <PageHeader title="MLX" subtitle="Apple Silicon local inference via mlx-lm." />}
        <div style={{ display: 'flex', justifyContent: 'center', padding: '48px' }}>
          <span class="spinner" />
        </div>
      </div>
    )
  }

  return (
    <div class="screen">
      {!hidePageHeader && <PageHeader title="MLX" subtitle="Apple Silicon local inference via mlx-lm." />}

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* ── Hardware gate banner ──────────────────────────────────────────── */}
      {!isAppleSilicon && (
        <div class="banner banner-warn" style={{ marginBottom: 16 }}>
          <span class="banner-message">
            MLX requires Apple Silicon (M-series chip). This machine is not supported.
            Use <strong>Llama</strong> instead.
          </span>
        </div>
      )}

      {/* ── Performance ──────────────────────────────────────────────────── */}
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
                {(status?.lastInference?.decodeTPS ?? 0) > 0
                  ? `${status!.lastInference!.decodeTPS.toFixed(1)}`
                  : '—'}
              </div>
              <div class="stat-note">
                {(status?.lastInference?.decodeTPS ?? 0) > 0
                  ? 'tok / sec'
                  : isLoading ? 'loading model…' : 'awaiting first turn'}
              </div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">First Token</div>
              <div class="stat-value">
                {(status?.lastInference?.firstTokenSec ?? 0) > 0
                  ? `${(status!.lastInference!.firstTokenSec! * 1000).toFixed(0)}ms`
                  : '—'}
              </div>
              <div class="stat-note">time to first token</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Prompt Tokens</div>
              <div class="stat-value">
                {(status?.lastInference?.promptTokens ?? 0) > 0
                  ? String(status!.lastInference!.promptTokens)
                  : '—'}
              </div>
              <div class="stat-note">prompt tokens</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Generation Time</div>
              <div class="stat-value">
                {(status?.lastInference?.generationSec ?? 0) > 0
                  ? status!.lastInference!.generationSec >= 60
                    ? `${(status!.lastInference!.generationSec / 60).toFixed(1)}m`
                    : `${status!.lastInference!.generationSec.toFixed(1)}s`
                  : '—'}
              </div>
              <div class="stat-note">{(status?.lastInference?.generationSec ?? 0) > 0 ? 'total' : ''}</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Output Tokens</div>
              <div class="stat-value">
                {(status?.lastInference?.completionTokens ?? 0) > 0
                  ? String(status!.lastInference!.completionTokens)
                  : '—'}
              </div>
              <div class="stat-note">output tokens</div>
            </div>
            <div class="stat-cell">
              <div class="stat-label">Scheduler</div>
              <div class="stat-value">{schedulerActive}/{scheduler?.maxConcurrency ?? '—'}</div>
              <div class="stat-note">active / max · queue {schedulerQueueDepth}</div>
            </div>
          </div>
        </div>

        {/* TPS per-turn graph */}
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
              const yLabelW  = 38
              const topPad   = 10
              const botPad   = 6
              const rightPad = 16
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

              const rawMax  = tpsHistory.length > 0 ? Math.max(...tpsHistory) : 10
              const niceMax = Math.ceil(rawMax / 5) * 5 || 10
              const yTicks  = [0, Math.round(niceMax * 0.33), Math.round(niceMax * 0.66), niceMax]
              const toY = (v: number) => topPad + chartH - (v / niceMax) * chartH

              const nSlots = Math.max(tpsHistory.length, 1)
              const toSlotX = (i: number, total: number) => {
                if (total <= 1) return yLabelW
                const span = total >= MAX_TPS_SLOTS ? chartW : (total - 1) / (MAX_TPS_SLOTS - 1) * chartW
                return yLabelW + (i / (total - 1)) * span
              }
              const buildPts = (series: number[]) => {
                if (series.length < 2) return ''
                return series.map((v, i) => `${toSlotX(i, series.length).toFixed(1)},${toY(v).toFixed(1)}`).join(' ')
              }

              const n      = tpsHistory.length
              const pts    = buildPts(tpsHistory)
              const lastX  = n > 0 ? toSlotX(n - 1, n) : yLabelW
              const lastY  = n > 0 ? toY(tpsHistory[n - 1]) : topPad + chartH
              const fill   = n >= 2
                ? `${toSlotX(0, n).toFixed(1)},${topPad + chartH} ${pts} ${lastX.toFixed(1)},${topPad + chartH}`
                : ''
              const stopped = !isRunning

              return (
                <svg width="100%" viewBox={`0 0 ${totalW} ${totalH}`}
                  preserveAspectRatio="none"
                  style={{ display: 'block', width: '100%', height: `${totalH}px` }}
                >
                  <defs>
                    <linearGradient id="mlx-tps-fill" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stop-color="var(--accent)" stop-opacity="0.18" />
                      <stop offset="100%" stop-color="var(--accent)" stop-opacity="0.01" />
                    </linearGradient>
                  </defs>
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
                  <line x1={yLabelW} y1={topPad} x2={yLabelW} y2={topPad + chartH}
                    stroke="var(--theme-border-subtle, #e5e7eb)" stroke-width="1" />
                  {n >= 2 && (
                    <>
                      <polygon points={fill} fill="url(#mlx-tps-fill)" />
                      <polyline points={pts} fill="none"
                        stroke={stopped ? 'var(--theme-text-muted, #9ca3af)' : 'var(--accent)'}
                        stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round" />
                      {!stopped && <circle cx={lastX} cy={lastY} r="3" fill="var(--accent)" />}
                    </>
                  )}
                  {n === 1 && <circle cx={lastX} cy={lastY} r="3" fill="var(--accent)" />}
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

      {/* ── Models ───────────────────────────────────────────────────────── */}
      <div>
        <div class="section-label" style={{ marginBottom: 10 }}>Models</div>

        {!venvReady && (
          <div class="banner banner-warn" style={{ marginBottom: 12, borderRadius: '6px' }}>
            <span class="banner-message">
              mlx-lm not installed. Use the <strong>Install / Upgrade</strong> section below to set up the Python environment.
            </span>
          </div>
        )}

        <div class="card">
          {models.length === 0 ? (
            <div style={{ padding: '40px 20px', textAlign: 'center', color: 'var(--theme-text-muted)', fontSize: 13 }}>
              No models downloaded yet — use the section below to get started.
            </div>
          ) : (
            [...models].sort((a, b) => a.sizeBytes - b.sizeBytes).map(m => {
              const isActive = isRunning && status?.loadedModel === m.name
              const { display, quant } = parseMLXModelInfo(m.name)
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
                          disabled={acting || !venvReady || !isAppleSilicon}
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

      {/* ── Tool Router ──────────────────────────────────────────────────── */}
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
                Select a downloaded model — Qwen 2.5 0.5B recommended.
                Auto-starts when a chat turn needs it.
              </div>
            </div>
            <div class="settings-field">
              <select
                class="input"
                value={routerModel}
                onChange={(e) => handleRouterModelChange((e.target as HTMLSelectElement).value)}
                disabled={routerModelSaving || !venvReady}
              >
                <option value="">Off</option>
                {[...models].sort((a, b) => a.sizeBytes - b.sizeBytes).map(m => {
                  const { display, quant } = parseMLXModelInfo(m.name)
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

      {/* ── Configuration ────────────────────────────────────────────────── */}
      <div>
        <div class="section-label">Configuration</div>
        <div class="card">

          {/* Context window size */}
          <div class="settings-row">
            <div class="settings-label-col">
              <div class="settings-label">Context size</div>
              <div class="settings-sublabel">
                Token limit per inference call passed to mlx_lm.server via --max-tokens.
                Larger values use more memory. Restart the model after changing.
              </div>
            </div>
            <div class="settings-field">
              <select
                class="input"
                value={ctxSize}
                onChange={(e) => handleCtxSizeChange(Number((e.target as HTMLSelectElement).value))}
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

          {/* Server port */}
          <div class="settings-row">
            <div class="settings-label-col">
              <div class="settings-label">Server Port</div>
              <div class="settings-sublabel">
                Port MLX listens on (managed by Atlas). Restart daemon after changing.
              </div>
            </div>
            <div class="settings-field">
              <input
                class="input input-sm"
                type="number"
                min={1024}
                max={65535}
                value={serverPort}
                onChange={(e) => handleServerPortChange(Number((e.target as HTMLInputElement).value))}
                disabled={serverPortSaving}
              />
            </div>
          </div>

          {/* mlx-lm version + install/upgrade — mirrors "llama-server" row in Engine LM */}
          <div class={`settings-row engine-inline-control-row${isMobile ? ' settings-row-mobile' : ''}`} style={{ borderBottom: 'none' }}>
            <div class="settings-label-col">
              <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                mlx-lm
                {hasUpgrade && (
                  <span class="badge badge-yellow" style={{ fontSize: 11, padding: '1px 6px' }}>Update</span>
                )}
              </div>
              <div class="settings-sublabel">
                {mlxVersionNote}
              </div>
            </div>
            <div class={`settings-field engine-inline-control-field${isMobile ? ' settings-field-mobile' : ''}`} style={isMobile ? undefined : { flexShrink: 0 }}>
              {isInstalling ? (
                <button class="btn btn-sm" onClick={handleCancelInstall}>Cancel</button>
              ) : (
                <button
                  class="btn btn-sm btn-primary"
                  onClick={handleInstall}
                  disabled={isInstalling || !isAppleSilicon}
                >
                  {mlxInstallLabel}
                </button>
              )}
            </div>
          </div>

          {/* Install progress */}
          {(isInstalling || install?.done || install?.error) && (
            <div style={{ borderTop: '1px solid var(--theme-border-subtle)', padding: '14px 20px', display: 'flex', flexDirection: 'column', gap: 10 }}>
              {isInstalling && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span class="spinner spinner-sm" />
                  <span style={{ fontSize: 12, fontFamily: 'var(--font-mono)', color: 'var(--theme-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
                    {install?.line || 'Installing mlx-lm…'}
                  </span>
                </div>
              )}
              {install?.done && (
                <div class="banner banner-success" style={{ borderRadius: 6 }}>
                  <span class="banner-message">✓ {install.line}</span>
                </div>
              )}
              {install?.error && (
                <div class="banner banner-error" style={{ borderRadius: 6 }}>
                  <span class="banner-message">Install failed: {install.error}</span>
                </div>
              )}
              {(install?.done || install?.error) && (
                <button class="btn btn-sm btn-ghost" style={{ alignSelf: 'flex-start' }} onClick={() => setInstall(null)}>Dismiss</button>
              )}
            </div>
          )}

        </div>
      </div>

      {/* ── Download Model ───────────────────────────────────────────────── */}
      <div>
        <div class="section-label">Download Model</div>
        <div class="card">

          <div class={`settings-row${isMobile ? ' settings-row-mobile' : ''}`}>
            <div class="settings-label-col">
              <div class="settings-label">Starter Models</div>
              <div class="settings-sublabel">Curated mlx-community models pre-quantized for Apple Silicon</div>
            </div>
            <div class={`settings-field${isMobile ? ' settings-field-mobile' : ''}`} style={isMobile ? undefined : { flex: '0 0 300px' }}>
              <select
                class="input"
                value={dlPreset}
                onChange={(e) => handlePresetChange((e.target as HTMLSelectElement).value)}
                disabled={isDownloading}
              >
                <option value="">Choose a model to download</option>
                <optgroup label="Primary Models">
                  {PRIMARY_MODELS.map(m => (
                    <option key={m.repo} value={m.repo}>{m.label}</option>
                  ))}
                </optgroup>
                <optgroup label="Router Models">
                  {ROUTER_MODELS.map(m => (
                    <option key={m.repo} value={m.repo}>{m.label}</option>
                  ))}
                </optgroup>
              </select>
            </div>
          </div>

          <div class={`settings-row${isMobile ? ' settings-row-mobile' : ''}`} style={{ borderBottom: 'none' }}>
            <div class="settings-label-col">
              <div class="settings-label">HuggingFace Repo</div>
              <div class="settings-sublabel">e.g. <code>mlx-community/Llama-3.2-3B-Instruct-4bit</code></div>
            </div>
            <div class={`settings-field${isMobile ? ' settings-field-mobile' : ''}`} style={isMobile ? undefined : { flex: '0 0 300px' }}>
              <input
                class="input"
                placeholder="org/model-name"
                value={dlRepo}
                onInput={(e) => { setDlRepo((e.target as HTMLInputElement).value); setDlPreset('') }}
                disabled={isDownloading}
              />
            </div>
          </div>

          {/* Footer: progress + actions */}
          <div style={{ borderTop: '1px solid var(--theme-border-subtle)', padding: '14px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>

            {download && !download.done && !download.error && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span class="spinner spinner-sm" />
                  <span style={{ fontSize: 12.5, color: 'var(--theme-text-secondary)' }}>
                    Downloading <code style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{download.repo.split('/').pop()}</code>…
                  </span>
                </div>
                {download.line && (
                  <div style={{ fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--theme-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', paddingLeft: 26 }}>
                    {download.line}
                  </div>
                )}
              </div>
            )}

            {download?.done && (
              <div class="banner banner-success" style={{ borderRadius: 6 }}>
                <span class="banner-message">✓ {download.repo.split('/').pop()} downloaded successfully.</span>
              </div>
            )}
            {download?.error && download.error !== 'paused' && (
              <div class="banner banner-error" style={{ borderRadius: 6 }}>
                <span class="banner-message">Download failed: {download.error}</span>
              </div>
            )}
            {download?.error === 'paused' && (
              <div style={{ color: 'var(--theme-text-muted)', fontSize: 12 }}>
                Paused — click Resume to continue.
              </div>
            )}

            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              {download?.done && (
                <button class="btn btn-sm btn-ghost" onClick={handleDismissDownload}>Dismiss</button>
              )}
              {isDownloading && (
                <button class="btn btn-sm" onClick={handleCancelDownload}>Cancel</button>
              )}
              {!isDownloading && download?.error && (
                <>
                  <button
                    class="btn btn-sm btn-primary"
                    onClick={handleDownload}
                    disabled={!/^[\w][\w.-]*\/[\w][\w.-]*$/.test(dlRepo.trim()) || !venvReady}
                  >Resume</button>
                  <button class="btn btn-sm" onClick={handleDismissDownload}>Cancel</button>
                </>
              )}
              {!isDownloading && !download?.error && !download?.done && (
                <button
                  class="btn btn-sm btn-primary"
                  onClick={handleDownload}
                  disabled={!/^[\w][\w.-]*\/[\w][\w.-]*$/.test(dlRepo.trim()) || !venvReady}
                >
                  Download
                </button>
              )}
            </div>

          </div>
        </div>
      </div>

    </div>
  )
}
