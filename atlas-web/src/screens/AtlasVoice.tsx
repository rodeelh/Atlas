import { useEffect, useRef, useState } from 'preact/hooks'
import { api, type VoiceModelInfo, type VoiceStatus } from '../api/client'
import { toast } from '../toast'

const WHISPER_MODELS = [
  { filename: 'ggml-tiny.en.bin',   label: 'Tiny  (English)',       size: '~75 MB',   note: 'Fastest · English only' },
  { filename: 'ggml-base.en.bin',   label: 'Base  (English)',       size: '~142 MB',  note: 'Default · Good balance' },
  { filename: 'ggml-small.en.bin',  label: 'Small  (English)',      size: '~466 MB',  note: 'Higher accuracy' },
  { filename: 'ggml-medium.en.bin', label: 'Medium  (English)',     size: '~1.5 GB',  note: 'High accuracy' },
  { filename: 'ggml-large-v3.bin',  label: 'Large v3',             size: '~2.9 GB',  note: 'Best accuracy · Multilingual' },
  { filename: 'ggml-tiny.bin',      label: 'Tiny  (Multilingual)',  size: '~75 MB',   note: 'Fastest · All languages' },
  { filename: 'ggml-base.bin',      label: 'Base  (Multilingual)',  size: '~142 MB',  note: 'Balanced · All languages' },
  { filename: 'ggml-small.bin',     label: 'Small  (Multilingual)', size: '~466 MB',  note: 'Higher accuracy · All languages' },
]
const WHISPER_BASE_URL = 'https://huggingface.co/ggerganov/whisper.cpp/resolve/main/'

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
  done: boolean
  error: string | null
}

export function AtlasVoice() {
  const [status, setStatus]   = useState<VoiceStatus | null>(null)
  const [models, setModels]   = useState<VoiceModelInfo[]>([])
  const [activeModel, setActiveModel] = useState('')
  const [loading, setLoading] = useState(true)

  const [dlPreset, setDlPreset]   = useState('')
  const [download, setDownload]   = useState<DownloadState | null>(null)
  const dlAbortRef = useRef<(() => void) | null>(null)
  // Finding 47: unmounted guard to prevent state updates after component cleanup
  const unmounted = useRef(false)

  const [whisperUpdate, setWhisperUpdate]         = useState<UpdateState | null>(null)
  const whisperUpdateAbortRef = useRef<(() => void) | null>(null)
  const [latestWhisperVersion, setLatestWhisperVersion] = useState<string | null>(null)

  const [kokoroUpdate, setKokoroUpdate]           = useState<UpdateState | null>(null)
  const kokoroUpdateAbortRef = useRef<(() => void) | null>(null)
  const [latestKokoroVersion, setLatestKokoroVersion]   = useState<string | null>(null)

  const load = async () => {
    try {
      const [s, m, cfg] = await Promise.all([
        api.voiceStatus(),
        api.voiceModels('whisper'),
        api.config(),
      ])
      setStatus(s)
      setModels(m)
      setActiveModel(cfg.voiceWhisperModel || 'ggml-base.en.bin')
    } catch { /* non-fatal */ } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    unmounted.current = false
    void load()
    fetch('https://api.github.com/repos/ggml-org/whisper.cpp/releases/latest')
      .then(r => r.json())
      .then((d: { tag_name?: string }) => { if (d.tag_name) setLatestWhisperVersion(d.tag_name) })
      .catch(() => {})
    fetch('https://pypi.org/pypi/kokoro-onnx/json')
      .then(r => r.json())
      .then((d: { info?: { version?: string } }) => { if (d.info?.version) setLatestKokoroVersion(d.info.version) })
      .catch(() => {})
    return () => { unmounted.current = true }
  }, [])

  const handleDownload = async (filenameArg?: string) => {
    const target = filenameArg ?? dlPreset
    if (!target) return
    const preset = WHISPER_MODELS.find(m => m.filename === target)
    if (!preset) return

    const filename = preset.filename
    const url = WHISPER_BASE_URL + filename
    const prevModel = activeModel
    setDownload({ filename, downloaded: 0, total: 0, percent: 0, done: false, error: null })
    dlAbortRef.current = null

    const ctrl = new AbortController()
    dlAbortRef.current = () => ctrl.abort()

    try {
      const BASE = api.engineDownloadBaseURL()
      const resp = await fetch(`${BASE}/voice/models/whisper/download`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url, filename }),
        signal: ctrl.signal,
      })
      if (!resp.ok || !resp.body) {
        const txt = await resp.text().catch(() => resp.statusText)
        setDownload(prev => prev ? { ...prev, error: txt } : null)
        return
      }
      const reader = resp.body.getReader()
      const dec = new TextDecoder()
      let buf = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += dec.decode(value, { stream: true })
        const parts = buf.split('\n\n')
        buf = parts.pop() ?? ''
        for (const part of parts) {
          const lines = part.trim().split('\n')
          let event = '', data = ''
          for (const ln of lines) {
            if (ln.startsWith('event:')) event = ln.slice(6).trim()
            else if (ln.startsWith('data:')) data = ln.slice(5).trim()
          }
          if (!event || !data) continue
          const parsed = JSON.parse(data)
          if (event === 'progress') {
            if (unmounted.current) return
            setDownload(prev => prev ? { ...prev, ...parsed } : null)
          } else if (event === 'done') {
            if (unmounted.current) return
            setDownload(prev => prev ? { ...prev, done: true, percent: 100 } : null)
            if (parsed.models) setModels(parsed.models)
            // Auto-activate the new model — Finding 46: surface activation failures
            try {
              await api.updateConfig({ voiceWhisperModel: filename })
              if (unmounted.current) return
              setActiveModel(filename)
            } catch {
              if (!unmounted.current) {
                setDownload(prev => prev
                  ? { ...prev, error: 'Download complete but activation failed. Please select the model manually.' }
                  : null)
              }
            }
            // Delete the previous model if it's different
            if (prevModel && prevModel !== filename) {
              try {
                const updated = await api.voiceDeleteModel('whisper', prevModel)
                if (!unmounted.current) setModels(updated)
              } catch { /* non-fatal */ }
            }
            if (!unmounted.current) {
              setDlPreset('')
              toast.success(`${filename} downloaded and activated`)
            }
          } else if (event === 'error') {
            if (unmounted.current) return
            setDownload(prev => prev ? { ...prev, error: parsed.message } : null)
          }
        }
      }
    } catch (e) {
      if (unmounted.current) return
      if ((e as Error).name !== 'AbortError') {
        setDownload(prev => prev ? { ...prev, error: (e as Error).message || 'Download failed' } : null)
      } else {
        setDownload(prev => prev ? { ...prev, error: 'Cancelled' } : null)
      }
    }
  }

  const handleWhisperUpdate = async (version: string) => {
    setWhisperUpdate({ version, done: false, error: null })
    const controller = new AbortController()
    whisperUpdateAbortRef.current = () => controller.abort()
    try {
      const resp = await fetch(`${api.voiceWhisperUpdateBaseURL()}/voice/whisper/update`, {
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
          if (event === 'done') {
            setWhisperUpdate(prev => prev ? { ...prev, done: true } : null)
            void load()
            whisperUpdateAbortRef.current = null
          } else if (event === 'error') {
            setWhisperUpdate(prev => prev ? { ...prev, error: data.message } : null)
            whisperUpdateAbortRef.current = null
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        setWhisperUpdate(prev => prev ? { ...prev, error: e instanceof Error ? e.message : 'Build failed' } : null)
      }
      whisperUpdateAbortRef.current = null
    }
  }

  const handleKokoroUpdate = async () => {
    setKokoroUpdate({ version: '', done: false, error: null })
    const controller = new AbortController()
    kokoroUpdateAbortRef.current = () => controller.abort()
    try {
      const resp = await fetch(`${api.voiceKokoroUpdateBaseURL()}/voice/kokoro/update`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version: '' }),
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
          if (event === 'done') {
            setKokoroUpdate(prev => prev ? { ...prev, done: true } : null)
            if (data.version) setLatestKokoroVersion(data.version)
            void load()
            kokoroUpdateAbortRef.current = null
          } else if (event === 'error') {
            setKokoroUpdate(prev => prev ? { ...prev, error: data.message } : null)
            kokoroUpdateAbortRef.current = null
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        setKokoroUpdate(prev => prev ? { ...prev, error: e instanceof Error ? e.message : 'Upgrade failed' } : null)
      }
      kokoroUpdateAbortRef.current = null
    }
  }

  const isDownloading     = !!download && !download.done && !download.error
  const isWhisperUpdating = !!whisperUpdate && !whisperUpdate.done && !whisperUpdate.error
  const isKokoroUpdating  = !!kokoroUpdate  && !kokoroUpdate.done  && !kokoroUpdate.error

  const whisperBuildTag   = status?.whisperBuildTag || ''
  const kokoroCurrentVer  = status?.kokoroVersion   || ''
  const whisperIsUpToDate = !!latestWhisperVersion && whisperBuildTag === latestWhisperVersion
  const kokoroIsUpToDate  = !!latestKokoroVersion  && kokoroCurrentVer === latestKokoroVersion
  const whisperTarget     = latestWhisperVersion ?? 'latest'

  const activeModelInfo   = models.find(m => m.name === activeModel)
  const downloadedNames   = new Set(models.map(m => m.name))

  const statusDot = (running: boolean, ready: boolean) => (
    <span style={{
      display: 'inline-block', width: 8, height: 8, borderRadius: '50%', flexShrink: 0,
      background: ready ? 'var(--theme-success, #22c55e)' : running ? '#f59e0b' : 'var(--theme-text-secondary)',
    }} />
  )

  if (loading) {
    return <div style={{ padding: '24px 20px' }}><span class="spinner spinner-sm" /></div>
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>

      {/* ── Whisper ── */}
      <div class="card settings-group" style={{ overflow: 'visible' }}>
        <div class="card-header"><span class="card-title">Whisper</span></div>

        {/* Active model + download/change */}
        <div class="settings-row" style={{ flexWrap: 'wrap', gap: '8px' }}>
          <div class="settings-label-col">
            <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              {activeModel || 'No model selected'}
              {status && statusDot(status.whisperRunning, status.whisperReady)}
            </div>
            <div class="settings-sublabel">
              {activeModelInfo ? `${activeModelInfo.sizeHuman} · active` : 'Select a model to download'}
            </div>
          </div>
          <div class="settings-field" style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
            <select
              class="input"
              style={{ minWidth: '220px' }}
              value={dlPreset}
              disabled={isDownloading}
              onChange={(e) => {
                const val = (e.target as HTMLSelectElement).value
                setDlPreset(val)
                if (val) void handleDownload(val)
              }}
            >
              <option value="">Change model…</option>
              {WHISPER_MODELS.map((m) => (
                <option key={m.filename} value={m.filename}>
                  {m.label} · {m.size}{downloadedNames.has(m.filename) && m.filename !== activeModel ? ' · downloaded' : ''}
                </option>
              ))}
            </select>
            {isDownloading && (
              <button class="btn btn-sm" onClick={() => { dlAbortRef.current?.() }}>Cancel</button>
            )}
          </div>
        </div>

        {/* Download progress */}
        {download && (
          <div style={{ padding: '10px 20px 14px' }}>
            {download.error ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <span style={{ fontSize: '13px', color: 'var(--theme-error, #ef4444)' }}>
                  {download.error === 'Cancelled' ? 'Download cancelled.' : `Error: ${download.error}`}
                </span>
                <button class="btn btn-sm" onClick={() => setDownload(null)}>Dismiss</button>
              </div>
            ) : download.done ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <span style={{ fontSize: '13px', color: 'var(--theme-success, #22c55e)' }}>
                  ✓ {download.filename} downloaded and activated.
                </span>
                <button class="btn btn-sm" onClick={() => setDownload(null)}>Dismiss</button>
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '12px', color: 'var(--theme-text-secondary)' }}>
                  <span>{download.filename}</span>
                  <span>
                    {download.total > 0
                      ? `${(download.downloaded / 1e6).toFixed(1)} / ${(download.total / 1e6).toFixed(1)} MB · ${download.percent.toFixed(0)}%`
                      : 'Connecting…'}
                  </span>
                </div>
                <div style={{ height: '4px', borderRadius: '2px', background: 'var(--theme-border)', overflow: 'hidden' }}>
                  <div style={{ height: '100%', borderRadius: '2px', background: 'var(--theme-accent)', width: `${download.percent}%`, transition: 'width 0.3s ease' }} />
                </div>
              </div>
            )}
          </div>
        )}

        {/* whisper-server version + update */}
        <div class="settings-row" style={{ borderTop: '1px solid var(--theme-border)' }}>
          <div class="settings-label-col">
            <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              whisper-server
              {whisperBuildTag && !whisperIsUpToDate && latestWhisperVersion && (
                <span class="badge badge-yellow" style={{ fontSize: 11, padding: '1px 6px' }}>Update</span>
              )}
            </div>
            <div class="settings-sublabel">
              {whisperBuildTag
                ? whisperIsUpToDate
                  ? `Up to date — ${whisperBuildTag}`
                  : latestWhisperVersion
                    ? `${whisperBuildTag} → ${latestWhisperVersion} available`
                    : `Built from source — ${whisperBuildTag}`
                : 'Not built — run make install'}
            </div>
          </div>
          <div class="settings-field">
            {isWhisperUpdating ? (
              <button class="btn btn-sm" style={{ minWidth: 90 }} onClick={() => { whisperUpdateAbortRef.current?.(); setWhisperUpdate(null) }}>Cancel</button>
            ) : (
              <button class="btn btn-sm btn-primary" style={{ minWidth: 90 }} disabled={isWhisperUpdating} onClick={() => void handleWhisperUpdate(whisperTarget)}>
                {whisperBuildTag && whisperIsUpToDate ? 'Rebuild' : whisperBuildTag ? 'Update' : 'Build'}
              </button>
            )}
          </div>
        </div>

        {(isWhisperUpdating || whisperUpdate?.done || whisperUpdate?.error) && (
          <div style={{ borderTop: '1px solid var(--theme-border-subtle)', padding: '14px 20px', display: 'flex', flexDirection: 'column', gap: 10 }}>
            {isWhisperUpdating && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <span class="spinner spinner-sm" />
                <span style={{ fontSize: 13, color: 'var(--theme-text-secondary)' }}>Building whisper-server…</span>
              </div>
            )}
            {whisperUpdate?.done && (
              <div class="banner banner-success" style={{ borderRadius: 6 }}>
                <span class="banner-message">✓ whisper-server built and installed.</span>
                <button class="banner-dismiss" onClick={() => setWhisperUpdate(null)} title="Dismiss">✕</button>
              </div>
            )}
            {whisperUpdate?.error && (
              <div class="banner banner-error" style={{ borderRadius: 6 }}>
                <span class="banner-message">Build failed: {whisperUpdate.error}</span>
                <button class="banner-dismiss" onClick={() => setWhisperUpdate(null)} title="Dismiss">✕</button>
              </div>
            )}
          </div>
        )}
      </div>

      {/* ── Kokoro ── */}
      <div class="card settings-group" style={{ overflow: 'visible' }}>
        <div class="card-header"><span class="card-title">Kokoro</span></div>

        {/* Engine status */}
        <div class="settings-row">
          <div class="settings-label-col">
            <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              Engine
              {status && statusDot(status.kokoroRunning, status.kokoroReady)}
            </div>
            <div class="settings-sublabel">
              {status?.kokoroReady ? 'Ready' : status?.kokoroRunning ? 'Starting…' : 'Idle'} · kokoro-onnx, on-device neural TTS
            </div>
          </div>
          <div class="settings-field">
            <button class="btn btn-sm" style={{ minWidth: 90 }} onClick={() => api.voiceKokoroWarmup().catch(() => {})}>Warm up</button>
          </div>
        </div>

        {/* kokoro-onnx version + update */}
        <div class="settings-row" style={{ borderTop: '1px solid var(--theme-border)' }}>
          <div class="settings-label-col">
            <div class="settings-label" style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              kokoro-onnx
              {kokoroCurrentVer && !kokoroIsUpToDate && latestKokoroVersion && (
                <span class="badge badge-yellow" style={{ fontSize: 11, padding: '1px 6px' }}>Update</span>
              )}
            </div>
            <div class="settings-sublabel">
              {kokoroCurrentVer
                ? kokoroIsUpToDate
                  ? `Up to date — v${kokoroCurrentVer}`
                  : latestKokoroVersion
                    ? `v${kokoroCurrentVer} → v${latestKokoroVersion} available`
                    : `Installed — v${kokoroCurrentVer}`
                : 'Not installed — run make install'}
            </div>
          </div>
          <div class="settings-field">
            {isKokoroUpdating ? (
              <button class="btn btn-sm" style={{ minWidth: 90 }} onClick={() => { kokoroUpdateAbortRef.current?.(); setKokoroUpdate(null) }}>Cancel</button>
            ) : (
              <button class="btn btn-sm btn-primary" style={{ minWidth: 90 }} disabled={isKokoroUpdating} onClick={() => void handleKokoroUpdate()}>
                {kokoroCurrentVer && kokoroIsUpToDate ? 'Reinstall' : kokoroCurrentVer ? 'Update' : 'Install'}
              </button>
            )}
          </div>
        </div>

        {(isKokoroUpdating || kokoroUpdate?.done || kokoroUpdate?.error) && (
          <div style={{ borderTop: '1px solid var(--theme-border-subtle)', padding: '14px 20px', display: 'flex', flexDirection: 'column', gap: 10 }}>
            {isKokoroUpdating && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <span class="spinner spinner-sm" />
                <span style={{ fontSize: 13, color: 'var(--theme-text-secondary)' }}>Installing kokoro-onnx…</span>
              </div>
            )}
            {kokoroUpdate?.done && (
              <div class="banner banner-success" style={{ borderRadius: 6 }}>
                <span class="banner-message">✓ kokoro-onnx updated successfully.</span>
                <button class="banner-dismiss" onClick={() => setKokoroUpdate(null)} title="Dismiss">✕</button>
              </div>
            )}
            {kokoroUpdate?.error && (
              <div class="banner banner-error" style={{ borderRadius: 6 }}>
                <span class="banner-message">Upgrade failed: {kokoroUpdate.error}</span>
                <button class="banner-dismiss" onClick={() => setKokoroUpdate(null)} title="Dismiss">✕</button>
              </div>
            )}
          </div>
        )}
      </div>

    </div>
  )
}
