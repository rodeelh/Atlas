import { useState, useEffect, useCallback, useRef } from 'preact/hooks'
import { api } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { ErrorBanner } from '../components/ErrorBanner'
import type { TokenUsageSummary, TokenUsageEvent, DailyUsageSeries } from '../api/contracts'
import { formatProviderModelName } from '../modelName'
import { ConfirmDialog } from '../components/ConfirmDialog'

/* ── Formatters ──────────────────────────────────────────────────────────── */

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

function fmtCost(usd: number): string {
  if (usd === 0) return '$0.00'
  if (usd < 0.0001) return '<$0.0001'
  if (usd < 0.01) return '$' + usd.toFixed(4)
  return '$' + usd.toFixed(2)
}

function fmtDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, {
      month: 'short', day: 'numeric',
      hour: '2-digit', minute: '2-digit',
    })
  } catch { return iso }
}

function providerLabel(provider: string): string {
  switch (provider) {
    case 'openai':       return 'OpenAI'
    case 'anthropic':    return 'Anthropic'
    case 'gemini':       return 'Gemini'
    case 'openrouter':   return 'OpenRouter'
    case 'lm_studio':    return 'LM Studio'
    case 'ollama':       return 'Ollama'
    case 'atlas_engine': return 'Local LM'
    case 'atlas_mlx':    return 'Local LM'
    default:             return provider
  }
}

function providerBadgeClass(provider: string): string {
  switch (provider) {
    case 'openai':       return 'badge badge-green'
    case 'anthropic':    return 'badge badge-yellow'
    case 'gemini':       return 'badge badge-blue'
    case 'openrouter':   return 'badge badge-blue'
    case 'atlas_engine': return 'badge badge-blue'
    case 'atlas_mlx':    return 'badge badge-blue'
    default:             return 'badge badge-gray'
  }
}

function providerDotColor(provider: string): string {
  switch (provider) {
    case 'openai':       return '#4caf87'
    case 'anthropic':    return '#e8a838'
    case 'gemini':       return '#5b8ee6'
    case 'openrouter':   return '#5b8ee6'
    case 'atlas_engine': return '#7c6fe0'
    case 'atlas_mlx':    return '#7c6fe0'
    case 'lm_studio':    return '#7c6fe0'
    case 'ollama':       return '#7c6fe0'
    default:             return 'var(--text-3)'
  }
}

/* ── Event source helpers ────────────────────────────────────────────────── */

type EventSource = 'chat' | 'agent' | 'system'

function eventSource(conversationId: string): EventSource {
  if (!conversationId) return 'system'
  if (conversationId.startsWith('task-')) return 'agent'
  return 'chat'
}

function sourceBadgeClass(src: EventSource): string {
  switch (src) {
    case 'chat':   return 'badge badge-green'
    case 'agent':  return 'badge badge-yellow'
    case 'system': return 'badge badge-blue'
  }
}

function sourceLabel(src: EventSource): string {
  switch (src) {
    case 'chat':   return 'Chat'
    case 'agent':  return 'Agent'
    case 'system': return 'System'
  }
}

// Returns a short ID for display: conv prefix for chat, task ID for agents, empty for system
function sourceId(conversationId: string): string {
  const src = eventSource(conversationId)
  if (src === 'system') return ''
  return conversationId.slice(0, 8)
}

function formatUsageModelName(provider: string, model: string): string {
  const formatted = formatProviderModelName(provider, model)
  if (provider === 'atlas_engine') {
    return formatted ? `Llama - ${formatted}` : 'Llama'
  }
  if (provider === 'atlas_mlx') {
    return formatted ? `MLX - ${formatted}` : 'MLX'
  }
  return formatted || model
}

/* ── Daily cost bar chart ─────────────────────────────────────────────────── */

function DailyChart({ series }: { series: DailyUsageSeries[] }) {
  const [hovered, setHovered]   = useState<number | null>(null)
  const [mouse, setMouse]       = useState<{ x: number; y: number }>({ x: 0, y: 0 })
  const containerRef            = useRef<HTMLDivElement>(null)
  const [width, setWidth]       = useState(400)

  // Track actual container width so bars always fill it regardless of window size
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const ro = new ResizeObserver(entries => {
      const w = entries[0].contentRect.width
      if (w > 0) setWidth(w)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  if (series.length === 0) {
    return (
      <div class="empty-state" style={{ padding: '32px 0', minHeight: 'unset' }}>
        <p>No usage data for this period</p>
      </div>
    )
  }

  const maxCost    = Math.max(...series.map(d => d.costUSD), 0.000001)
  const chartH     = 140
  const labelH     = 20
  const leftW      = 44   // y-axis label column (px)
  const chartW     = Math.max(1, width - leftW)
  const gridLevels = [0.25, 0.5, 0.75, 1.0]

  const slotW      = chartW / series.length
  const barW       = Math.max(4, Math.min(52, slotW * 0.6))
  const labelEvery = Math.max(1, Math.ceil(series.length / 8))

  return (
    <div
      ref={containerRef}
      style={{ position: 'relative', width: '100%' }}
      onMouseMove={(e: MouseEvent) => setMouse({ x: e.clientX, y: e.clientY })}
    >
      <svg
        width={width}
        height={chartH + labelH}
        style={{ display: 'block', width: '100%', height: `${chartH + labelH}px` }}
      >
        {/* Y-axis labels + grid lines — left column then chart area */}
        {gridLevels.map(level => {
          const gy = chartH - level * (chartH - 4)
          return (
            <g key={level}>
              <text
                x={leftW - 6} y={gy + 3.5}
                text-anchor="end"
                font-size="10"
                fill="var(--text-2)"
              >
                {fmtCost(maxCost * level)}
              </text>
              <line
                x1={leftW} y1={gy} x2={width} y2={gy}
                stroke="var(--border)"
                stroke-width={level === 1.0 ? 1 : 0.5}
                stroke-dasharray={level === 1.0 ? undefined : '3 4'}
              />
            </g>
          )
        })}

        {/* Bars + date labels */}
        {series.map((d, i) => {
          const h     = Math.max(3, (d.costUSD / maxCost) * (chartH - 4))
          const slotX = leftW + i * slotW
          const x     = slotX + (slotW - barW) / 2
          const y     = chartH - h
          const isHov = hovered === i
          return (
            <g key={d.date}>
              <rect
                x={x} y={y} width={barW} height={h}
                rx="3"
                fill="var(--accent)"
                fill-opacity={isHov ? 0.95 : 0.55}
                style={{ cursor: 'default', transition: 'fill-opacity 0.12s' }}
                onMouseEnter={() => setHovered(i)}
                onMouseLeave={() => setHovered(null)}
              />
              {i % labelEvery === 0 && (
                <text
                  x={slotX + slotW / 2}
                  y={chartH + labelH - 3}
                  text-anchor="middle"
                  font-size="10"
                  fill="var(--text-2)"
                >
                  {d.date.slice(5)}
                </text>
              )}
            </g>
          )
        })}
      </svg>

      {hovered !== null && (
        <div class="usage-chart-tooltip" style={{ top: `${mouse.y + 14}px`, left: `${mouse.x + 14}px` }}>
          <div class="usage-chart-tooltip-date">{series[hovered].date}</div>
          <div class="usage-chart-tooltip-row">{fmtTokens(series[hovered].totalTokens)} tokens</div>
          <div class="usage-chart-tooltip-cost">{fmtCost(series[hovered].costUSD)}</div>
          <div class="usage-chart-tooltip-row">{series[hovered].turnCount} turn{series[hovered].turnCount !== 1 ? 's' : ''}</div>
          {series[hovered].turnCount > 0 && (
            <div class="usage-chart-tooltip-row">{fmtCost(series[hovered].costUSD / series[hovered].turnCount)} / turn</div>
          )}
        </div>
      )}
    </div>
  )
}

/* ── Range type ──────────────────────────────────────────────────────────── */

type Range = '7d' | '30d' | '90d' | 'all'

const RANGES: { id: Range; label: string; days: number }[] = [
  { id: '7d',  label: '7d',  days: 7  },
  { id: '30d', label: '30d', days: 30 },
  { id: '90d', label: '90d', days: 90 },
  { id: 'all', label: 'All', days: 0  },
]

/* ── Main screen ─────────────────────────────────────────────────────────── */

type SortKey = 'provider' | 'model' | 'turnCount' | 'inputTokens' | 'outputTokens' | 'tokensPerTurn' | 'totalCostUSD' | 'avgCostPerTurn'
type SortDir = 'asc' | 'desc'

const USAGE_SORT_STORAGE_KEY = 'atlasUsageByModelSort'

function readPersistedUsageSort(): { sortKey: SortKey; sortDir: SortDir } {
  try {
    const raw = localStorage.getItem(USAGE_SORT_STORAGE_KEY)
    if (!raw) return { sortKey: 'totalCostUSD', sortDir: 'desc' }
    const parsed = JSON.parse(raw) as { sortKey?: string; sortDir?: string }
    const validKeys: SortKey[] = ['provider', 'model', 'turnCount', 'inputTokens', 'outputTokens', 'tokensPerTurn', 'totalCostUSD', 'avgCostPerTurn']
    const sortKey = validKeys.includes(parsed.sortKey as SortKey) ? parsed.sortKey as SortKey : 'totalCostUSD'
    const sortDir = parsed.sortDir === 'asc' || parsed.sortDir === 'desc' ? parsed.sortDir : 'desc'
    return { sortKey, sortDir }
  } catch {
    return { sortKey: 'totalCostUSD', sortDir: 'desc' }
  }
}

export function Usage() {
  const [range, setRange]         = useState<Range>('30d')
  const [summary, setSummary]     = useState<TokenUsageSummary | null>(null)
  const [events, setEvents]       = useState<TokenUsageEvent[]>([])
  const [eventsOpen, setEventsOpen] = useState(false)
  const [sourceFilter, setSourceFilter] = useState<EventSource | 'all'>('all')
  const [loading, setLoading]     = useState(true)
  const [error, setError]         = useState<string | null>(null)
  const [clearing, setClearing]     = useState(false)
  const [clearMsg, setClearMsg]     = useState<string | null>(null)
  const [pendingClear, setPendingClear] = useState(false)
  const [{ sortKey, sortDir }, setSortState] = useState(() => readPersistedUsageSort())
  const [isMobile, setIsMobile]   = useState(() => window.innerWidth <= 480)
  const [showMinorModels, setShowMinorModels] = useState(false)

  const handleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortState(current => ({ ...current, sortDir: current.sortDir === 'desc' ? 'asc' : 'desc' }))
    } else {
      setSortState({ sortKey: key, sortDir: 'desc' })
    }
  }

  const sortArrow = (key: SortKey) => {
    if (key !== sortKey) return <span style={{ opacity: 0.25, marginLeft: '3px' }}>↕</span>
    return <span style={{ marginLeft: '3px' }}>{sortDir === 'desc' ? '↓' : '↑'}</span>
  }

  const selectedRange = RANGES.find(r => r.id === range)!

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const s = await api.usageSummary({ days: selectedRange.days || undefined })
      setSummary(s)
    } catch (e: any) {
      setError(e?.message ?? 'Failed to load usage data')
    } finally {
      setLoading(false)
    }
  }, [range])

  useEffect(() => { load() }, [load])

  useEffect(() => {
    const onResize = () => setIsMobile(window.innerWidth <= 480)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  useEffect(() => {
    if (!eventsOpen) return
    api.usageEvents({ limit: 200 }).then(e => setEvents(e.events)).catch(() => {})
  }, [eventsOpen])

  useEffect(() => {
    try {
      localStorage.setItem(USAGE_SORT_STORAGE_KEY, JSON.stringify({ sortKey, sortDir }))
    } catch {
      /* ignore persistence failures */
    }
  }, [sortKey, sortDir])

  const handleClear = () => {
    setPendingClear(true)
  }

  const confirmClear = async () => {
    setPendingClear(false)
    setClearing(true)
    try {
      const before = new Date(Date.now() - 90 * 24 * 60 * 60 * 1000).toISOString()
      const res = await api.deleteUsage(before)
      setClearMsg(`Deleted ${res.deleted} record${res.deleted !== 1 ? 's' : ''}.`)
      load()
    } catch {
      setClearMsg('Failed to clear data.')
    } finally {
      setClearing(false)
      setTimeout(() => setClearMsg(null), 4000)
    }
  }

  if (loading && !summary) {
    return (
      <div class="screen">
        <PageHeader title="Usage" subtitle="Token consumption and estimated cost" />
        <PageSpinner />
      </div>
    )
  }

  return (
    <div class="screen">
      <PageHeader
        title="Usage"
        subtitle="Token consumption and estimated spend across all providers"
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* ── Summary (4-up horizontal) + Daily chart side by side ── */}
      <div style={{ display: 'flex', flexDirection: isMobile ? 'column' : 'row', gap: '12px', alignItems: 'stretch' }}>

        {/* Summary: 4 stats in a row */}
        <div class="card" style={{ flex: 1 }}>
          <div class="card-header">
            <span class="card-title">Summary</span>
          </div>
          {(() => {
            const activeDays = summary ? summary.dailySeries.filter(d => d.turnCount > 0).length : 0
            return (
              <div class="stat-grid">
                {/* Row 1 — cost */}
                <div class="stat-cell">
                  <div class="stat-label">Total Spent</div>
                  <div class="stat-value">{summary ? fmtCost(summary.totalCostUSD) : '—'}</div>
                  <div class="stat-note">estimated</div>
                </div>
                <div class="stat-cell">
                  <div class="stat-label">Avg Cost / Event</div>
                  <div class="stat-value">
                    {summary && summary.turnCount > 0
                      ? fmtCost(summary.totalCostUSD / summary.turnCount)
                      : '—'}
                  </div>
                  <div class="stat-note">per LLM call</div>
                </div>
                <div class="stat-cell">
                  <div class="stat-label">Daily Avg Cost</div>
                  <div class="stat-value">
                    {summary && activeDays > 0
                      ? fmtCost(summary.totalCostUSD / activeDays)
                      : '—'}
                  </div>
                  <div class="stat-note">on active days</div>
                </div>
                <div class="stat-cell">
                  <div class="stat-label">Events</div>
                  <div class="stat-value">{summary ? String(summary.turnCount) : '—'}</div>
                  <div class="stat-note">chat, agent & background</div>
                </div>
                {/* Row 2 — tokens */}
                <div class="stat-cell">
                  <div class="stat-label">Active Days</div>
                  <div class="stat-value">{summary ? String(activeDays) : '—'}</div>
                  <div class="stat-note">
                    {summary ? `of ${summary.dailySeries.length} in range` : ''}
                  </div>
                </div>
                <div class="stat-cell">
                  <div class="stat-label">Input Tokens</div>
                  <div class="stat-value">{summary ? fmtTokens(summary.totalInputTokens) : '—'}</div>
                  <div class="stat-note">
                    {summary
                      ? `prompt · ${fmtTokens(summary.totalCachedInputTokens)} cached`
                      : 'prompt'}
                  </div>
                </div>
                <div class="stat-cell">
                  <div class="stat-label">Output Tokens</div>
                  <div class="stat-value">{summary ? fmtTokens(summary.totalOutputTokens) : '—'}</div>
                  <div class="stat-note">completion</div>
                </div>
                <div class="stat-cell">
                  <div class="stat-label">Avg Tokens / Event</div>
                  <div class="stat-value">
                    {summary && summary.turnCount > 0
                      ? fmtTokens(Math.round(summary.totalTokens / summary.turnCount))
                      : '—'}
                  </div>
                  <div class="stat-note">in + out</div>
                </div>
              </div>
            )
          })()}
        </div>

        {/* Daily cost chart — matches summary card height */}
        <div class="card" style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
          <div class="card-header">
            <span class="card-title">
              Daily Cost — {selectedRange.days > 0 ? `Last ${selectedRange.days} Days` : 'All Time'}
            </span>
            <div class="log-filter-tabs">
              {RANGES.map(r => (
                <button
                  key={r.id}
                  class={`log-filter-tab${range === r.id ? ' active' : ''}`}
                  onClick={() => setRange(r.id)}
                >
                  {r.label}
                </button>
              ))}
            </div>
          </div>
          <div class="card-body" style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}>
            <DailyChart series={summary?.dailySeries ?? []} />
          </div>
        </div>

      </div>

      {/* ── Model breakdown ────────────────────────────────── */}
      <div class="card">
        <div class="card-header">
          <span class="card-title">By Model</span>
        </div>

        {!summary || summary.byModel.length === 0 ? (
          <div class="empty-state" style={{ padding: '32px 0', minHeight: 'unset' }}>
            <p>No data for this period</p>
          </div>
        ) : (() => {
          const sorted = [...summary.byModel].sort((a, b) => {
            let av: number | string, bv: number | string
            switch (sortKey) {
              case 'provider':      av = a.provider;      bv = b.provider;      break
              case 'model':
                av = formatUsageModelName(a.provider, a.model)
                bv = formatUsageModelName(b.provider, b.model)
                break
              case 'turnCount':     av = a.turnCount;     bv = b.turnCount;     break
              case 'inputTokens':   av = a.inputTokens;   bv = b.inputTokens;   break
              case 'outputTokens':  av = a.outputTokens;  bv = b.outputTokens;  break
              case 'tokensPerTurn':
                av = a.turnCount > 0 ? a.totalTokens / a.turnCount : -1
                bv = b.turnCount > 0 ? b.totalTokens / b.turnCount : -1
                break
              case 'totalCostUSD':  av = a.totalCostUSD;  bv = b.totalCostUSD;  break
              case 'avgCostPerTurn':
                av = a.turnCount > 0 ? a.totalCostUSD / a.turnCount : -1
                bv = b.turnCount > 0 ? b.totalCostUSD / b.turnCount : -1
                break
            }
            const cmp = typeof av === 'string' ? av.localeCompare(bv as string) : (av as number) - (bv as number)
            return sortDir === 'desc' ? -cmp : cmp
          })

          const thStyle = (key: SortKey, align: 'left' | 'right' = 'right') => ({
            cursor: 'pointer',
            userSelect: 'none' as const,
            textAlign: align,
            color: key === sortKey ? 'var(--theme-text-primary)' : undefined,
          })

          return (
            <>
              {isMobile ? (
                <>
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', padding: '0 16px 12px' }}>
                    {[
                      ['provider', 'Provider'],
                      ['model', 'Model'],
                      ['turnCount', 'Calls'],
                      ['inputTokens', 'Input'],
                      ['outputTokens', 'Output'],
                      ['tokensPerTurn', 'Tok/Call'],
                      ['totalCostUSD', 'Cost'],
                    ].map(([key, label]) => (
                      <button
                        key={key}
                        class={`log-filter-tab${sortKey === key ? ' active' : ''}`}
                        onClick={() => handleSort(key as SortKey)}
                      >
                        {label}{sortKey === key ? ` ${sortDir === 'desc' ? '↓' : '↑'}` : ''}
                      </button>
                    ))}
                  </div>

                  {sorted.map((m, i) => (
                    <div class="row" key={i} style={{ display: 'block', paddingTop: 14, paddingBottom: 14 }}>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                          <span class={providerBadgeClass(m.provider)} style={{ alignSelf: 'flex-start' }}>
                            {providerLabel(m.provider)}
                          </span>
                          <span class="skill-name" style={{ fontFamily: 'monospace', fontSize: '12px', fontWeight: 400, lineHeight: 1.55, wordBreak: 'break-word' }}>
                            {formatUsageModelName(m.provider, m.model)}
                          </span>
                        </div>

                        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '10px 16px' }}>
                          <div>
                            <div class="stat-label" style={{ marginBottom: 2 }}>Calls</div>
                            <div class="stat-value">{m.turnCount}</div>
                          </div>
                          <div>
                            <div class="stat-label" style={{ marginBottom: 2 }}>Cost</div>
                            {m.totalCostUSD === 0
                              ? <div class="stat-note" title="Local model — no cost">—</div>
                              : <div class="stat-value">{fmtCost(m.totalCostUSD)}</div>}
                          </div>
                          <div>
                            <div class="stat-label" style={{ marginBottom: 2 }}>Input</div>
                            <div class="stat-value">{fmtTokens(m.inputTokens)}</div>
                          </div>
                          <div>
                            <div class="stat-label" style={{ marginBottom: 2 }}>Output</div>
                            <div class="stat-value">{fmtTokens(m.outputTokens)}</div>
                          </div>
                          <div>
                            <div class="stat-label" style={{ marginBottom: 2 }}>Tok / Turn</div>
                            <div class="stat-value">
                              {m.turnCount > 0 ? fmtTokens(Math.round(m.totalTokens / m.turnCount)) : '—'}
                            </div>
                          </div>
                          <div style={{ gridColumn: '1 / -1' }}>
                            <div class="stat-label" style={{ marginBottom: 2 }}>Avg / Call</div>
                            {m.totalCostUSD === 0 || m.turnCount === 0
                              ? <div class="stat-note">—</div>
                              : <div class="stat-value">{fmtCost(m.totalCostUSD / m.turnCount)}</div>}
                          </div>
                        </div>
                      </div>
                    </div>
                  ))}
                </>
              ) : (() => {
                  const MINOR_THRESHOLD = 50
                  const primary = sorted.filter(m => m.turnCount >= MINOR_THRESHOLD)
                  const minor   = sorted.filter(m => m.turnCount < MINOR_THRESHOLD)
                  const hasMajor = primary.length > 0
                  const visibleRows = hasMajor ? primary : sorted
                  const totals = sorted.reduce((acc, m) => ({
                    turnCount:    acc.turnCount    + m.turnCount,
                    inputTokens:  acc.inputTokens  + m.inputTokens,
                    outputTokens: acc.outputTokens + m.outputTokens,
                    totalTokens:  acc.totalTokens  + m.totalTokens,
                    totalCostUSD: acc.totalCostUSD + m.totalCostUSD,
                  }), { turnCount: 0, inputTokens: 0, outputTokens: 0, totalTokens: 0, totalCostUSD: 0 })

                  const ModelRow = ({ m, i }: { m: typeof sorted[0]; i: number }) => (
                    <div class="row" key={i} style={{ background: i % 2 === 1 ? 'var(--row-alt)' : undefined }}>
                      <div style={{ flex: '0 0 96px', display: 'flex', alignItems: 'center', gap: 6 }}>
                        <span style={{ width: 7, height: 7, borderRadius: '50%', background: providerDotColor(m.provider), flexShrink: 0, display: 'inline-block' }} />
                        <span style={{ fontSize: '12px', color: 'var(--theme-text-primary)', whiteSpace: 'nowrap' }}>{providerLabel(m.provider)}</span>
                      </div>
                      <div style={{ flex: 1, minWidth: 0, overflow: 'hidden' }}>
                        <span class="skill-name" style={{ fontFamily: 'monospace', fontSize: '12px', fontWeight: 400, display: 'block', overflow: 'hidden', whiteSpace: 'nowrap', textOverflow: 'ellipsis' }} title={formatUsageModelName(m.provider, m.model)}>
                          {formatUsageModelName(m.provider, m.model)}
                        </span>
                      </div>
                      <div style={{ flex: '0 0 52px', textAlign: 'right' }}>
                        <span class="stat-value">{m.turnCount}</span>
                      </div>
                      <div style={{ flex: '0 0 64px', textAlign: 'right' }}>
                        <span class="stat-value">{fmtTokens(m.inputTokens)}</span>
                      </div>
                      <div style={{ flex: '0 0 64px', textAlign: 'right' }}>
                        <span class="stat-value">{fmtTokens(m.outputTokens)}</span>
                      </div>
                      <div style={{ flex: '0 0 76px', textAlign: 'right' }}>
                        <span class="stat-value">
                          {m.turnCount > 0 ? fmtTokens(Math.round(m.totalTokens / m.turnCount)) : '—'}
                        </span>
                      </div>
                      <div style={{ flex: '0 0 72px', textAlign: 'right' }}>
                        {m.totalCostUSD === 0
                          ? <span class="stat-note">Free</span>
                          : <span class="stat-value">{fmtCost(m.totalCostUSD)}</span>
                        }
                      </div>
                      <div style={{ flex: '0 0 80px', textAlign: 'right' }}>
                        {m.totalCostUSD === 0 || m.turnCount === 0
                          ? <span class="stat-note">Free</span>
                          : <span class="stat-value">{fmtCost(m.totalCostUSD / m.turnCount)}</span>
                        }
                      </div>
                    </div>
                  )

                  return (
                    <>
                      {/* Scrollable area: headers + rows */}
                      <div style={{ maxHeight: '480px', overflowY: 'auto' }}>
                        {/* Column headers */}
                        <div class="row" style={{ paddingTop: '8px', paddingBottom: '8px', background: 'var(--surface-2)', position: 'sticky', top: 0, zIndex: 1 }}>
                          <div style={{ flex: '0 0 96px', ...thStyle('provider', 'left') }} onClick={() => handleSort('provider')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Provider{sortArrow('provider')}</span>
                          </div>
                          <div style={{ flex: 1, minWidth: 0, ...thStyle('model', 'left') }} onClick={() => handleSort('model')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Model{sortArrow('model')}</span>
                          </div>
                          <div style={{ flex: '0 0 52px', ...thStyle('turnCount') }} onClick={() => handleSort('turnCount')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Calls{sortArrow('turnCount')}</span>
                          </div>
                          <div style={{ flex: '0 0 64px', ...thStyle('inputTokens') }} onClick={() => handleSort('inputTokens')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Input{sortArrow('inputTokens')}</span>
                          </div>
                          <div style={{ flex: '0 0 64px', ...thStyle('outputTokens') }} onClick={() => handleSort('outputTokens')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Output{sortArrow('outputTokens')}</span>
                          </div>
                          <div style={{ flex: '0 0 76px', ...thStyle('tokensPerTurn') }} onClick={() => handleSort('tokensPerTurn')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Tok/Call{sortArrow('tokensPerTurn')}</span>
                          </div>
                          <div style={{ flex: '0 0 72px', ...thStyle('totalCostUSD') }} onClick={() => handleSort('totalCostUSD')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Cost{sortArrow('totalCostUSD')}</span>
                          </div>
                          <div style={{ flex: '0 0 80px', ...thStyle('avgCostPerTurn') }} onClick={() => handleSort('avgCostPerTurn')}>
                            <span class="stat-label" style={{ marginBottom: 0 }}>Avg/Call{sortArrow('avgCostPerTurn')}</span>
                          </div>
                        </div>

                        {visibleRows.map((m, i) => <ModelRow key={i} m={m} i={i} />)}

                        {showMinorModels && hasMajor && minor.map((m, i) => <ModelRow key={`minor-${i}`} m={m} i={i} />)}
                      </div>

                      {/* Totals footer — pinned outside scroll container */}
                      <div class="row" style={{ paddingTop: '8px', paddingBottom: '8px', borderTop: '1px solid var(--theme-border-subtle)', background: 'var(--surface-2)' }}>
                        <div style={{ flex: '0 0 96px' }} />
                        <div style={{ flex: 1, minWidth: 0, overflow: 'hidden' }}>
                          <span class="stat-label" style={{ marginBottom: 0, display: 'block', whiteSpace: 'nowrap' }}>Total</span>
                        </div>
                        <div style={{ flex: '0 0 52px', textAlign: 'right' }}>
                          <span class="stat-note">{totals.turnCount}</span>
                        </div>
                        <div style={{ flex: '0 0 64px', textAlign: 'right' }}>
                          <span class="stat-note">{fmtTokens(totals.inputTokens)}</span>
                        </div>
                        <div style={{ flex: '0 0 64px', textAlign: 'right' }}>
                          <span class="stat-note">{fmtTokens(totals.outputTokens)}</span>
                        </div>
                        <div style={{ flex: '0 0 76px', textAlign: 'right' }}>
                          <span class="stat-note">
                            {totals.turnCount > 0 ? fmtTokens(Math.round(totals.totalTokens / totals.turnCount)) : '—'}
                          </span>
                        </div>
                        <div style={{ flex: '0 0 72px', textAlign: 'right' }}>
                          <span class="stat-note">{fmtCost(totals.totalCostUSD)}</span>
                        </div>
                        <div style={{ flex: '0 0 80px', textAlign: 'right' }}>
                          <span class="stat-note">
                            {totals.turnCount > 0 ? fmtCost(totals.totalCostUSD / totals.turnCount) : '—'}
                          </span>
                        </div>
                      </div>

                      {/* Minor models expand/collapse — below totals */}
                      {hasMajor && minor.length > 0 && (
                        <div
                          class="row"
                          style={{ cursor: 'pointer', opacity: 0.6, justifyContent: 'center' }}
                          onClick={() => setShowMinorModels(v => !v)}
                        >
                          <span class="stat-label" style={{ marginBottom: 0, fontSize: '11px' }}>
                            {showMinorModels ? '▲ Hide' : '▼ Show'} {minor.length} model{minor.length !== 1 ? 's' : ''} with &lt;{MINOR_THRESHOLD} calls
                          </span>
                        </div>
                      )}
                    </>
                  )
                })()
              }
            </>
          )
        })()}
      </div>

      {/* ── Recent events (collapsible) ────────────────────── */}
      <div class="section-label activity-log-header">
        <span>Recent Events</span>
        <button
          class="btn btn-sm btn-ghost"
          onClick={() => setEventsOpen(v => !v)}
        >
          {eventsOpen ? 'Collapse' : 'Expand'}
        </button>
      </div>

      {eventsOpen && (
        <div class="card">
          {/* Source filter tabs */}
          <div style={{ display: 'flex', gap: 6, padding: '12px 16px 0', flexWrap: 'wrap' }}>
            {(['all', 'chat', 'agent', 'system'] as const).map(src => (
              <button
                key={src}
                class={`log-filter-tab${sourceFilter === src ? ' active' : ''}`}
                onClick={() => setSourceFilter(src)}
              >
                {src === 'all' ? 'All' : sourceLabel(src as EventSource)}
              </button>
            ))}
          </div>

          {(() => {
            const filtered = sourceFilter === 'all'
              ? events
              : events.filter(e => eventSource(e.conversationId) === sourceFilter)

            if (filtered.length === 0) {
              return (
                <div class="empty-state" style={{ padding: '32px 0', minHeight: 'unset' }}>
                  <p>{events.length === 0 ? 'No events recorded yet' : 'No events for this filter'}</p>
                </div>
              )
            }

            return isMobile ? (
              filtered.map(e => {
                const src = eventSource(e.conversationId)
                const id  = sourceId(e.conversationId)
                return (
                  <div class="row" key={e.id} style={{ display: 'block', paddingTop: 14, paddingBottom: 14 }}>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                          <span class={sourceBadgeClass(src)}>{sourceLabel(src)}</span>
                          <span class={providerBadgeClass(e.provider)}>{providerLabel(e.provider)}</span>
                          <span class="skill-meta">{fmtDate(e.recordedAt)}</span>
                        </div>
                        <span style={{ fontFamily: 'monospace', fontSize: '11px', color: 'var(--theme-text-secondary)', lineHeight: 1.5, wordBreak: 'break-word' }}>
                          {formatUsageModelName(e.provider, e.model)}
                        </span>
                        {id && (
                          <span style={{ fontFamily: 'monospace', fontSize: '11px', color: 'var(--theme-text-muted)' }}>
                            {src === 'agent' ? 'task' : 'conv'} {id}
                          </span>
                        )}
                      </div>
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: '10px 12px' }}>
                        <div>
                          <div class="stat-label" style={{ marginBottom: 2 }}>In</div>
                          <div class="stat-value">{fmtTokens(e.inputTokens)}</div>
                        </div>
                        <div>
                          <div class="stat-label" style={{ marginBottom: 2 }}>Out</div>
                          <div class="stat-value">{fmtTokens(e.outputTokens)}</div>
                        </div>
                        <div>
                          <div class="stat-label" style={{ marginBottom: 2 }}>Cost</div>
                          {e.totalCostUSD === 0
                            ? <div class="stat-note">—</div>
                            : <div class="stat-value">{fmtCost(e.totalCostUSD)}</div>}
                        </div>
                      </div>
                    </div>
                  </div>
                )
              })
            ) : (
              <>
                {/* Column header */}
                <div class="row" style={{ paddingTop: '8px', paddingBottom: '8px', background: 'none', cursor: 'default' }}>
                  <div style={{ flex: '1 1 120px' }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>Time</span>
                  </div>
                  <div style={{ flex: '0 0 68px' }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>Source</span>
                  </div>
                  <div style={{ flex: '0 0 60px' }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>ID</span>
                  </div>
                  <div style={{ flex: '0 0 88px' }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>Provider</span>
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>Model</span>
                  </div>
                  <div style={{ flex: '0 0 52px', textAlign: 'right' }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>In</span>
                  </div>
                  <div style={{ flex: '0 0 52px', textAlign: 'right' }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>Out</span>
                  </div>
                  <div style={{ flex: '0 0 64px', textAlign: 'right' }}>
                    <span class="stat-label" style={{ marginBottom: 0 }}>Cost</span>
                  </div>
                </div>

                {filtered.map(e => {
                  const src = eventSource(e.conversationId)
                  const id  = sourceId(e.conversationId)
                  return (
                    <div class="row" key={e.id}>
                      <div style={{ flex: '1 1 120px' }}>
                        <span class="skill-meta">{fmtDate(e.recordedAt)}</span>
                      </div>
                      <div style={{ flex: '0 0 68px' }}>
                        <span class={sourceBadgeClass(src)}>{sourceLabel(src)}</span>
                      </div>
                      <div style={{ flex: '0 0 60px' }}>
                        <span style={{ fontFamily: 'monospace', fontSize: '11px', color: 'var(--theme-text-muted)' }}>
                          {id || '—'}
                        </span>
                      </div>
                      <div style={{ flex: '0 0 88px' }}>
                        <span class={providerBadgeClass(e.provider)}>
                          {providerLabel(e.provider)}
                        </span>
                      </div>
                      <div style={{ flex: 1, minWidth: 0, overflow: 'hidden' }}>
                        <span style={{ fontFamily: 'monospace', fontSize: '11px', color: 'var(--theme-text-secondary)', display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {formatUsageModelName(e.provider, e.model)}
                        </span>
                      </div>
                      <div style={{ flex: '0 0 52px', textAlign: 'right' }}>
                        <span class="stat-value">{fmtTokens(e.inputTokens)}</span>
                      </div>
                      <div style={{ flex: '0 0 52px', textAlign: 'right' }}>
                        <span class="stat-value">{fmtTokens(e.outputTokens)}</span>
                      </div>
                      <div style={{ flex: '0 0 64px', textAlign: 'right' }}>
                        {e.totalCostUSD === 0
                          ? <span class="stat-note">—</span>
                          : <span class="stat-value">{fmtCost(e.totalCostUSD)}</span>
                        }
                      </div>
                    </div>
                  )
                })}
              </>
            )
          })()}
        </div>
      )}

      {/* ── Data management ────────────────────────────────── */}
      <div class="card">
        <div class="row">
          <div style={{ flex: 1, minWidth: 0 }}>
            <div class="skill-name">Clear Old Data</div>
            <div class="skill-meta">Delete usage records older than 90 days</div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            {clearMsg && <span class="skill-meta">{clearMsg}</span>}
            <button
              class="btn btn-sm btn-ghost"
              style={{ color: 'var(--red)' }}
              disabled={clearing}
              onClick={handleClear}
            >
              {clearing ? <span class="spinner" style={{ width: '11px', height: '11px' }} /> : 'Clear'}
            </button>
          </div>
        </div>
      </div>

      {pendingClear && (
        <ConfirmDialog
          title="Clear old usage data?"
          body="Usage records older than 90 days will be permanently deleted."
          confirmLabel="Clear"
          danger
          onConfirm={confirmClear}
          onCancel={() => setPendingClear(false)}
        />
      )}
    </div>
  )
}
