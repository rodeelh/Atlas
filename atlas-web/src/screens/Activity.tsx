import { useState, useEffect } from 'preact/hooks'
import { api, LogEntry, RuntimeStatus, RuntimeConfig, EngineStatus } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { ErrorBanner } from '../components/ErrorBanner'
import { formatAtlasModelName, formatProviderModelName } from '../modelName'
import { toast } from '../toast'

const LogCopyIcon = () => (
  <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <rect x="5" y="5" width="9" height="9" rx="1.5" />
    <path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5" />
  </svg>
)

const LogCheckIcon = () => (
  <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M3 8l4 4 6-7" />
  </svg>
)

// ── Formatters ─────────────────────────────────────────────────────────────────

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch { return iso }
}

function formatUptime(startedAt: string): string {
  const s = Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  const rm = m % 60
  if (h < 24) return rm > 0 ? `${h}h ${rm}m` : `${h}h`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

function formatRelative(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60000) return 'just now'
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

function formatLogMessage(message: string): string {
  return message.replace(/\s+/g, ' ').trim()
}

function levelClass(level: string): string {
  switch (level.toLowerCase()) {
    case 'debug':                return 'debug'
    case 'info':                 return 'info'
    case 'warning': case 'warn': return 'warn'
    case 'error': case 'fault':  return 'error'
    default:                     return 'info'
  }
}

function formatLevelLabel(level: string): string {
  switch (level.toLowerCase()) {
    case 'debug': return 'Debug'
    case 'warning':
    case 'warn': return 'Warning'
    case 'error': return 'Error'
    case 'fault': return 'Fault'
    default: return 'Info'
  }
}

function formatMetadataKey(key: string): string {
  const expanded = key
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .trim()
  if (!expanded) return key
  return expanded.charAt(0).toUpperCase() + expanded.slice(1)
}

function formatMetadataValue(value: string): string {
  return value.replace(/\s+/g, ' ').trim()
}

function stateBadge(state: string) {
  switch (state.toLowerCase()) {
    case 'ready':    return <span class="badge badge-green">{state}</span>
    case 'starting': return <span class="badge badge-yellow">{state}</span>
    case 'degraded': return <span class="badge badge-yellow">{state}</span>
    case 'stopped':  return <span class="badge badge-red">{state}</span>
    default:         return <span class="badge badge-gray">{state}</span>
  }
}


function formatProvider(p: string | null | undefined): string {
  switch (p) {
    case 'openai':       return 'OpenAI'
    case 'anthropic':    return 'Anthropic'
    case 'gemini':       return 'Gemini'
    case 'lm_studio':    return 'LM Studio'
    case 'ollama':       return 'Ollama'
    case 'atlas_engine': return 'Local LM'
    case 'atlas_mlx':    return 'Local LM'
    default:             return p ?? '—'
  }
}

function activeModelName(cfg: RuntimeConfig | null): string {
  if (!cfg) return '—'
  switch (cfg.activeAIProvider) {
    case 'anthropic':    return cfg.selectedAnthropicModel?.trim()    || '—'
    case 'gemini':       return cfg.selectedGeminiModel?.trim()       || '—'
    case 'lm_studio':    return cfg.selectedLMStudioModel?.trim()     || '—'
    case 'ollama':       return cfg.selectedOllamaModel?.trim()       || '—'
    case 'atlas_engine': return formatAtlasModelName(cfg.selectedAtlasEngineModel?.trim() || '') || '—'
    case 'atlas_mlx':    return formatProviderModelName('atlas_mlx', cfg.selectedAtlasMLXModel?.trim() || '') || '—'
    default:             return cfg.selectedOpenAIPrimaryModel?.trim() || '—'
  }
}

type LogFilter = 'all' | 'info' | 'warn' | 'error'

// ── Component ──────────────────────────────────────────────────────────────────

export function Activity() {
  const [logs, setLogs]               = useState<LogEntry[]>([])
  const [status, setStatus]           = useState<RuntimeStatus | null>(null)
  const [config, setConfig]           = useState<RuntimeConfig | null>(null)
  const [engineStatus, setEngineStatus] = useState<EngineStatus | null>(null)
  const [loading, setLoading]         = useState(true)
  const [error, setError]             = useState<string | null>(null)
  const [logFilter, setLogFilter]     = useState<LogFilter>('all')
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [copiedLogId, setCopiedLogId] = useState<string | null>(null)
  const [expandedLogIds, setExpandedLogIds] = useState<Set<string>>(new Set())

  const copyLog = async (entry: LogEntry) => {
    const metadataLines = entry.metadata && Object.keys(entry.metadata).length > 0
      ? '\n' + Object.entries(entry.metadata)
        .map(([key, value]) => `${formatMetadataKey(key)}: ${formatMetadataValue(value)}`)
        .join('\n')
      : ''
    const text = `[${formatTime(entry.timestamp)}] [${formatLevelLabel(entry.level).toUpperCase()}] ${formatLogMessage(entry.message)}${metadataLines}`
    try {
      await navigator.clipboard.writeText(text)
      setCopiedLogId(entry.id)
      toast.success('Copied')
      setTimeout(() => setCopiedLogId(prev => prev === entry.id ? null : prev), 1800)
    } catch {
      toast.error('Could not copy')
    }
  }

  const load = async () => {
    try {
      const [logData, statusData, configData, engineData] = await Promise.allSettled([
        api.logs(200), api.status(), api.config(), api.engineStatus(),
      ])
      if (logData.status === 'fulfilled')    setLogs(logData.value)
      if (statusData.status === 'fulfilled')  setStatus(statusData.value)
      if (configData.status === 'fulfilled')  setConfig(configData.value)
      if (engineData.status === 'fulfilled')  setEngineStatus(engineData.value)
      setLastUpdated(new Date())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load activity.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    const interval = setInterval(load, 10000)
    return () => clearInterval(interval)
  }, [])

  const filteredLogs = [...logs].reverse().filter(entry => {
    if (logFilter === 'all') return true
    const lv = entry.level.toLowerCase()
    if (logFilter === 'warn')  return lv === 'warn' || lv === 'warning'
    if (logFilter === 'error') return lv === 'error' || lv === 'fault'
    return lv === logFilter
  })

  const toggleDetails = (id: string) => {
    setExpandedLogIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  if (loading) {
    return (
      <div class="screen">
        <PageHeader title="Activity" subtitle="Daemon health and event log" />
        <div style={{ display: 'flex', justifyContent: 'center', padding: '48px' }}>
          <span class="spinner" />
        </div>
      </div>
    )
  }

  return (
    <div class="screen">
      <PageHeader
        title="Activity"
        subtitle={lastUpdated ? `Updated ${formatRelative(lastUpdated.toISOString())}` : 'Daemon health and event log'}
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* ── Stats ── */}
      <div class="card">
        <div class="stat-grid">
          {/* System */}
          <div class="stat-cell">
            <div class="stat-label">State</div>
            <div class="stat-value">{status ? stateBadge(status.state) : '—'}</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Uptime</div>
            <div class="stat-value">{status?.startedAt ? formatUptime(status.startedAt) : '—'}</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Port</div>
            <div class="stat-value">{status?.runtimePort ?? '—'}</div>
          </div>
          {/* AI */}
          <div class="stat-cell">
            <div class="stat-label">Active Model</div>
            <div class="stat-value">{activeModelName(config)}</div>
            <div class="stat-note">{formatProvider(config?.activeAIProvider)}</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Tokens In</div>
            <div class="stat-value">{status?.tokensIn != null ? status.tokensIn.toLocaleString() : '—'}</div>
            <div class="stat-note">since restart</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Tokens Out</div>
            <div class="stat-value">{status?.tokensOut != null ? status.tokensOut.toLocaleString() : '—'}</div>
            <div class="stat-note">since restart</div>
          </div>
          {/* Engine LM — always shown; dims when not running */}
          <div class="stat-cell" style={{ opacity: engineStatus?.running ? 1 : 0.4 }}>
            <div class="stat-label">Engine tok/s</div>
            <div class="stat-value">
              {engineStatus?.running && engineStatus.lastTPS != null && engineStatus.lastTPS > 0
                ? engineStatus.lastTPS.toFixed(1)
                : '—'}
            </div>
            <div class="stat-note">decode speed</div>
          </div>
          {/* Activity */}
          <div class="stat-cell">
            <div class="stat-label">Conversations</div>
            <div class="stat-value">{status?.activeConversationCount ?? '—'}</div>
            <div class="stat-note">active</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Pending Approvals</div>
            <div class="stat-value">{status?.pendingApprovalCount ?? '—'}</div>
            <div class="stat-note">awaiting review</div>
          </div>
        </div>
        {status?.lastError && (
          <div style={{ padding: '12px 20px' }}>
            <ErrorBanner error={status.lastError} />
          </div>
        )}
      </div>

      {/* ── Logs ── */}
      <div style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
        <div class="section-label activity-log-header">
          <span>Event Log</span>
          <div class="log-filter-tabs">
            {(['all', 'info', 'warn', 'error'] as LogFilter[]).map(f => (
              <button
                key={f}
                class={`log-filter-tab${logFilter === f ? ' active' : ''}`}
                onClick={() => setLogFilter(f)}
              >
                {f === 'all' ? 'All' : f === 'warn' ? 'Warnings' : f.charAt(0).toUpperCase() + f.slice(1)}
              </button>
            ))}
          </div>
          <span class="activity-live">
            <span class="activity-live-dot" />
            Live
          </span>
        </div>
        <div class="card" style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          {filteredLogs.length === 0 ? (
            <div style={{ padding: '24px', textAlign: 'center', color: 'var(--text-3)', fontSize: '13px' }}>
              {logFilter === 'all' ? 'Send a message to start seeing activity here.' : `No ${logFilter === 'warn' ? 'warning' : logFilter} entries yet.`}
            </div>
          ) : (
            <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', padding: '8px 0' }}>
              {filteredLogs.map(entry => {
                const isError = entry.level === 'error' || entry.level === 'fault'
                const isExpanded = expandedLogIds.has(entry.id)
                const metadataEntries = entry.metadata
                  ? Object.entries(entry.metadata)
                      .map(([key, value]) => ({
                        key,
                        label: formatMetadataKey(key),
                        value: formatMetadataValue(value),
                      }))
                      .filter(item => item.value.length > 0)
                  : []
                return (
                  <div class={`log-entry${isError ? ' log-entry-error' : ''}`} key={entry.id}>
                    <div class="log-entry-body">
                      <div class="log-entry-summary">
                        <span class="log-time">{formatTime(entry.timestamp)}</span>
                        <span class={`log-level-badge ${levelClass(entry.level)}`} title={entry.level}>
                          {formatLevelLabel(entry.level)}
                        </span>
                        <span class="log-summary-separator" aria-hidden="true">-</span>
                        <span class="log-message" title={formatLogMessage(entry.message)}>{formatLogMessage(entry.message)}</span>
                      </div>
                      {isExpanded && (
                        <div class="log-details">
                          <div class="log-details-row">
                            <span class="log-details-label">Summary</span>
                            <span class="log-details-value">{formatLogMessage(entry.message)}</span>
                          </div>
                          {metadataEntries.length > 0 && (
                            <div class="log-details-row">
                              <span class="log-details-label">Metadata</span>
                              <div class="log-details-meta-list">
                                {metadataEntries.map(item => (
                                  <div class="log-details-meta-item" key={`${entry.id}-${item.key}`}>
                                    <span class="log-details-meta-key">{item.label}</span>
                                    <span class="log-details-meta-value">{item.value}</span>
                                  </div>
                                ))}
                              </div>
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                    <button
                      class={`log-action-btn${isExpanded ? ' active' : ''}`}
                      onClick={() => toggleDetails(entry.id)}
                      title={isExpanded ? 'Hide details' : 'Show details'}
                      aria-label={isExpanded ? 'Hide log entry details' : 'Show log entry details'}
                    >
                      {isExpanded ? 'Hide' : 'Details'}
                    </button>
                    <button
                      class={`log-action-btn log-copy-btn${copiedLogId === entry.id ? ' copied' : ''}`}
                      onClick={() => copyLog(entry)}
                      title="Copy entry"
                      aria-label="Copy log entry"
                    >
                      {copiedLogId === entry.id ? <LogCheckIcon /> : <LogCopyIcon />}
                    </button>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
