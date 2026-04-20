// Built-in preset renderers for v2 dashboard widgets.
//
// Each preset receives the widget definition and the resolved data payload.
// Charts use Chart.js. Metric, Table, List, and Markdown are Preact
// components. Agent-authored code widgets are delegated to
// DashboardCodeFrame (sandboxed iframe).

import { JSX } from 'preact'
import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import {
  Chart,
  LineController, BarController, PieController, ScatterController,
  LineElement, BarElement, PointElement, ArcElement,
  LinearScale, CategoryScale, TimeScale,
  Filler, Tooltip, Legend,
  type ChartConfiguration,
} from 'chart.js'
import type { DashboardWidget } from '../api/client'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { DashboardCodeFrame, type DashboardWidgetAction } from './DashboardCodeFrame'

Chart.register(
  LineController, BarController, PieController, ScatterController,
  LineElement, BarElement, PointElement, ArcElement,
  LinearScale, CategoryScale, TimeScale,
  Filler, Tooltip, Legend,
)

// ── helpers ───────────────────────────────────────────────────────────────────

function widgetOptions(widget: DashboardWidget): Record<string, unknown> {
  return (widget.code?.options as Record<string, unknown>) || {}
}

function valueAtPath(data: unknown, path: string): unknown {
  if (!path) return data
  const parts = path.split('.')
  let current: unknown = data
  for (const part of parts) {
    if (current && typeof current === 'object' && part in (current as Record<string, unknown>)) {
      current = (current as Record<string, unknown>)[part]
    } else {
      return undefined
    }
  }
  return current
}

function formatValue(v: unknown, format?: string): string {
  if (v === null || v === undefined) return '—'
  const n = typeof v === 'number' ? v : Number(v)
  if (Number.isNaN(n)) return String(v)
  switch (format) {
    case 'currency': return `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
    case 'integer':  return Math.round(n).toLocaleString()
    case 'percent':  return `${(n * 100).toFixed(1)}%`
    case 'decimal':  return n.toLocaleString(undefined, { maximumFractionDigits: 2 })
    case 'compact':  return Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n)
    default:         return typeof v === 'number' ? n.toLocaleString() : String(v)
  }
}

function asArray(v: unknown): unknown[] {
  if (Array.isArray(v)) return v
  if (v && typeof v === 'object') {
    const obj = v as Record<string, unknown>
    // Named array keys we see frequently across skills and runtime feeds.
    for (const key of ['rows', 'items', 'results', 'data', 'events', 'history', 'records', 'entries']) {
      if (Array.isArray(obj[key])) return obj[key] as unknown[]
    }
    // Fallback: if the object has exactly one array-valued field, use it.
    // Covers skill artifacts like {memories: [...]}, {days: [...]}, {hours: [...]},
    // {dashboards: [...]}, etc. without hard-coding every possible key.
    const arrayEntries = Object.entries(obj).filter(([, val]) => Array.isArray(val))
    if (arrayEntries.length === 1) return arrayEntries[0][1] as unknown[]
  }
  return []
}

function asString(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  return String(v)
}

interface SeriesPoint { x: string; y: number }
interface PiePoint { label: string; value: number }
interface ScatterPoint { x: number; y: number; label: string }
interface ThresholdRule { value: number; tone?: 'ok' | 'warn' | 'err' | 'neutral'; label?: string; color?: string }

function extractSeries(opts: Record<string, unknown>, data: unknown): SeriesPoint[] {
  const seriesPath = (opts.seriesPath as string) || (opts.path as string) || ''
  const xKey = (opts.x as string) || 'date'
  const yKey = (opts.y as string) || 'value'
  const raw = seriesPath ? valueAtPath(data, seriesPath) : data
  const arr = Array.isArray(raw) ? raw : asArray(data)
  return arr.flatMap(item => {
    if (!item || typeof item !== 'object') return []
    const obj = item as Record<string, unknown>
    const y = Number(obj[yKey] ?? 0)
    if (!Number.isFinite(y)) return []
    return [{ x: asString(obj[xKey] ?? ''), y }]
  })
}

function extractPieSeries(opts: Record<string, unknown>, data: unknown): PiePoint[] {
  const seriesPath = (opts.seriesPath as string) || (opts.path as string) || ''
  const labelKey = (opts.labelKey as string) || 'label'
  const valueKey = (opts.valueKey as string) || 'value'
  const raw = seriesPath ? valueAtPath(data, seriesPath) : data
  const arr = Array.isArray(raw) ? raw : asArray(data)
  return arr.flatMap(item => {
    if (!item || typeof item !== 'object') return []
    const obj = item as Record<string, unknown>
    const value = Number(obj[valueKey] ?? 0)
    if (!Number.isFinite(value)) return []
    return [{ label: asString(obj[labelKey] ?? obj.name ?? obj.title ?? ''), value }]
  }).filter(item => item.label)
}

function extractScatterSeries(opts: Record<string, unknown>, data: unknown): ScatterPoint[] {
  const seriesPath = (opts.seriesPath as string) || (opts.path as string) || ''
  const xKey = (opts.x as string) || 'x'
  const yKey = (opts.y as string) || 'y'
  const raw = seriesPath ? valueAtPath(data, seriesPath) : data
  const arr = Array.isArray(raw) ? raw : asArray(data)
  return arr.flatMap((item, index) => {
    if (!item || typeof item !== 'object') return []
    const obj = item as Record<string, unknown>
    const xRaw = obj[xKey]
    const x = typeof xRaw === 'number' ? xRaw : Number(xRaw)
    const y = Number(obj[yKey] ?? 0)
    if (!Number.isFinite(y)) return []
    return [{
      x: Number.isFinite(x) ? x : index + 1,
      y,
      label: asString(xRaw ?? index + 1),
    }]
  })
}

function paletteForSeries(count: number, opts?: Record<string, unknown>): string[] {
  const palettes: Record<string, string[]> = {
    atlas: ['#6366f1', '#0ea5e9', '#14b8a6', '#22c55e', '#f59e0b', '#ef4444', '#f97316', '#8b5cf6'],
    ocean: ['#0f766e', '#0891b2', '#2563eb', '#7c3aed', '#0ea5e9', '#14b8a6'],
    warm: ['#f97316', '#ef4444', '#f59e0b', '#eab308', '#fb7185', '#f43f5e'],
    forest: ['#166534', '#15803d', '#22c55e', '#65a30d', '#84cc16', '#10b981'],
    slate: ['#475569', '#64748b', '#334155', '#94a3b8', '#0f172a', '#1e293b'],
  }
  const selected = typeof opts?.palette === 'string' ? opts.palette : 'atlas'
  const base = palettes[selected] || palettes.atlas
  return Array.from({ length: count }, (_, index) => base[index % base.length])
}

function stringMapOption(opts: Record<string, unknown>, key: string): Record<string, string> {
  const raw = opts[key]
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
  return Object.fromEntries(Object.entries(raw as Record<string, unknown>).map(([mapKey, value]) => [mapKey, asString(value)]))
}

function thresholdRules(opts: Record<string, unknown>): ThresholdRule[] {
  const raw = opts.thresholds
  if (!Array.isArray(raw)) return []
  return raw.flatMap(item => {
    if (!item || typeof item !== 'object') return []
    const obj = item as Record<string, unknown>
    const value = Number(obj.value)
    if (!Number.isFinite(value)) return []
    return [{
      value,
      tone: ['ok', 'warn', 'err', 'neutral'].includes(asString(obj.tone)) ? obj.tone as ThresholdRule['tone'] : undefined,
      label: obj.label ? asString(obj.label) : undefined,
      color: obj.color ? asString(obj.color) : undefined,
    }]
  }).sort((a, b) => a.value - b.value)
}

function resolveThreshold(opts: Record<string, unknown>, value: number): ThresholdRule | null {
  const rules = thresholdRules(opts)
  let match: ThresholdRule | null = null
  for (const rule of rules) {
    if (value >= rule.value) match = rule
  }
  return match
}

function displayValue(opts: Record<string, unknown>, raw: unknown, format?: string): string {
  const mapped = stringMapOption(opts, 'valueMap')
  const key = raw === null || raw === undefined ? '' : String(raw)
  if (key && mapped[key] !== undefined) return mapped[key]
  const unitLabel = asString(opts.unitLabel)
  const base = formatValue(raw, format)
  return unitLabel && base !== '—' ? `${base} ${unitLabel}` : base
}

function toneForNumericValue(opts: Record<string, unknown>, value: number, fallback?: 'ok' | 'warn' | 'err' | 'neutral'): 'ok' | 'warn' | 'err' | 'neutral' {
  const threshold = resolveThreshold(opts, value)
  if (threshold?.tone) return threshold.tone
  const warnAt = opts.warnAt !== undefined ? Number(opts.warnAt) : undefined
  const dangerAt = opts.dangerAt !== undefined ? Number(opts.dangerAt) : undefined
  if (dangerAt !== undefined && value >= dangerAt) return 'err'
  if (warnAt !== undefined && value >= warnAt) return 'warn'
  return fallback ?? 'ok'
}

function emptyMessage(widget: DashboardWidget, fallback: string): string {
  const text = widgetOptions(widget).emptyText
  return typeof text === 'string' && text.trim() ? text.trim() : fallback
}

function metricTone(tone: unknown): 'ok' | 'warn' | 'err' | 'neutral' {
  return statusTone(tone)
}

type SortDirection = 'asc' | 'desc'

function cellValue(row: unknown, col: string): unknown {
  return row && typeof row === 'object'
    ? (row as Record<string, unknown>)[col]
    : row
}

function compareCells(a: unknown, b: unknown, direction: SortDirection): number {
  const an = typeof a === 'number' ? a : Number(a)
  const bn = typeof b === 'number' ? b : Number(b)
  const bothNumeric = Number.isFinite(an) && Number.isFinite(bn)
  const result = bothNumeric
    ? an - bn
    : asString(a).localeCompare(asString(b), undefined, { numeric: true, sensitivity: 'base' })
  return direction === 'asc' ? result : -result
}

function numericOptionValue(opts: Record<string, unknown>, data: unknown, key: string, fallback: number): number {
  const path = (opts[key] as string) || ''
  const raw = path ? valueAtPath(data, path) : opts[key]
  const n = Number(raw)
  return Number.isFinite(n) ? n : fallback
}

function normalizedValue(value: number, min: number, max: number): number {
  if (max <= min) return 0
  return Math.max(0, Math.min(1, (value - min) / (max - min)))
}

function toneForValue(value: number, warnAt?: number, dangerAt?: number): 'ok' | 'warn' | 'err' | 'neutral' {
  if (dangerAt !== undefined && value >= dangerAt) return 'err'
  if (warnAt !== undefined && value >= warnAt) return 'warn'
  return 'ok'
}

function statusTone(status: unknown): 'ok' | 'warn' | 'err' | 'neutral' {
  const value = asString(status).toLowerCase()
  if (['ok', 'online', 'active', 'running', 'healthy', 'success', 'ready'].includes(value)) return 'ok'
  if (['warn', 'warning', 'slow', 'pending', 'degraded', 'reviewing', 'thinking'].includes(value)) return 'warn'
  if (['error', 'failed', 'down', 'offline', 'blocked', 'critical'].includes(value)) return 'err'
  return 'neutral'
}

const getChartFont = () =>
  getComputedStyle(document.documentElement).getPropertyValue('--ui-font').trim() ||
  '-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif'
const GRID_COLOR = 'rgba(156,163,175,0.15)'
const TICK_COLOR = 'rgba(107,114,128,0.9)'

// ── metric ────────────────────────────────────────────────────────────────────

export function MetricWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const path       = (opts.path as string)       || ''
  const format     = (opts.format as string)     || ''
  const label      = (opts.label as string)      || widget.description || ''
  const prefix     = (opts.prefix as string)     || ''
  const suffix     = (opts.suffix as string)     || ''
  const changePath = (opts.changePath as string) || ''
  // When no explicit path is given and data is a flat object, fall back to
  // the first numeric or {text} field so the metric doesn't render "[object
  // Object]" for skill outputs the agent hasn't bound precisely.
  let raw: unknown = path ? valueAtPath(data, path) : data
  let inferredField = false
  if (!path && raw && typeof raw === 'object' && !Array.isArray(raw)) {
    const obj = raw as Record<string, unknown>
    const numericKey = Object.keys(obj).find(k => typeof obj[k] === 'number')
    if (numericKey) { raw = obj[numericKey]; inferredField = true }
    else if (typeof obj.text === 'string') { raw = obj.text; inferredField = true }
  }
  const numericRaw = typeof raw === 'number' ? raw : Number(raw)
  const metricToneClass = Number.isFinite(numericRaw) ? toneForNumericValue(opts, numericRaw, 'neutral') : 'neutral'
  const display = displayValue(opts, raw, format)
  const change  = changePath ? valueAtPath(data, changePath) : undefined
  const changeN = change !== undefined ? Number(change) : NaN
  const trendUp = Number.isFinite(changeN) && changeN > 0
  const trendDn = Number.isFinite(changeN) && changeN < 0

  return (
    <div class={`dashboard-widget-body dw-metric dw-tone-${metricToneClass}`}>
      <div
        class="dw-metric-value"
        title={inferredField ? 'Field was inferred automatically — configure a path in the widget options for precision.' : undefined}
      >
        {prefix}{display}{suffix}{inferredField && <span class="dw-metric-inferred" aria-label="inferred field"> ⚠</span>}
      </div>
      {Number.isFinite(changeN) && (
        <div class={`dw-metric-change ${trendUp ? 'up' : trendDn ? 'down' : ''}`}>
          {trendUp ? '▲' : trendDn ? '▼' : '●'} {Math.abs(changeN).toFixed(2)}
        </div>
      )}
      {label && <div class="dw-metric-label">{label}</div>}
    </div>
  )
}

// ── progress ──────────────────────────────────────────────────────────────────

export function ProgressWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const path = (opts.path as string) || ''
  const raw = path ? valueAtPath(data, path) : data
  const value = Number(raw)
  const min = numericOptionValue(opts, data, 'minPath', Number(opts.min ?? 0))
  const max = numericOptionValue(opts, data, 'maxPath', Number(opts.max ?? 100))
  const format = (opts.format as string) || 'decimal'
  const label = (opts.label as string) || widget.description || ''
  const pct = normalizedValue(Number.isFinite(value) ? value : 0, min, max) * 100
  const tone = toneForNumericValue(opts, Number.isFinite(value) ? value : pct)
  const threshold = resolveThreshold(opts, Number.isFinite(value) ? value : pct)

  if (!Number.isFinite(value)) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No progress value')}</div>
  }
  return (
    <div class={`dashboard-widget-body dw-progress dw-tone-${tone}`}>
      <div class="dw-progress-head">
        <span>{label}</span>
        <strong>{displayValue(opts, value, format)}</strong>
      </div>
      <div class="dw-progress-track" aria-label={label || widget.title || 'Progress'} aria-valuemin={min} aria-valuemax={max} aria-valuenow={value} role="progressbar">
        <div class="dw-progress-fill" style={{ width: `${pct}%`, background: threshold?.color || undefined }} />
      </div>
      <div class="dw-progress-meta">{displayValue(opts, min, format)} / {displayValue(opts, max, format)}{threshold?.label ? ` • ${threshold.label}` : ''}</div>
    </div>
  )
}

// ── gauge ─────────────────────────────────────────────────────────────────────

export function GaugeWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const path = (opts.path as string) || ''
  const raw = path ? valueAtPath(data, path) : data
  const value = Number(raw)
  const min = numericOptionValue(opts, data, 'minPath', Number(opts.min ?? 0))
  const max = numericOptionValue(opts, data, 'maxPath', Number(opts.max ?? 100))
  const format = (opts.format as string) || 'decimal'
  const label = (opts.label as string) || widget.description || ''
  const pct = normalizedValue(Number.isFinite(value) ? value : 0, min, max)
  const degrees = -90 + pct * 180
  const tone = toneForNumericValue(opts, Number.isFinite(value) ? value : pct)
  const threshold = resolveThreshold(opts, Number.isFinite(value) ? value : pct)

  if (!Number.isFinite(value)) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No gauge value')}</div>
  }
  return (
    <div class={`dashboard-widget-body dw-gauge dw-tone-${tone}`}>
      <div class="dw-gauge-arc" aria-label={label || widget.title || 'Gauge'}>
        <svg viewBox="0 0 120 70" role="img">
          <path class="dw-gauge-bg" d="M15 60 A45 45 0 0 1 105 60" />
          <path class="dw-gauge-fg" d="M15 60 A45 45 0 0 1 105 60" pathLength="100" style={{ strokeDasharray: `${pct * 100} 100`, stroke: threshold?.color || undefined }} />
          <line class="dw-gauge-needle" x1="60" y1="60" x2="60" y2="24" transform={`rotate(${degrees} 60 60)`} />
          <circle class="dw-gauge-pin" cx="60" cy="60" r="3" />
        </svg>
      </div>
      <div class="dw-gauge-value">{displayValue(opts, value, format)}</div>
      {label && <div class="dw-metric-label">{label}</div>}
    </div>
  )
}

// ── line chart ────────────────────────────────────────────────────────────────

export function LineChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const chartRef  = useRef<Chart | null>(null)
  const [selected, setSelected] = useState<SeriesPoint | null>(null)
  const opts      = widgetOptions(widget)
  const points    = useMemo(() => extractSeries(opts, data ?? null), [data, opts])
  const color     = (opts.color as string) || paletteForSeries(1, opts)[0]
  const filled    = (opts.filled as boolean) ?? true

  useEffect(() => {
    if (!canvasRef.current) return
    chartRef.current?.destroy()
    const cfg: ChartConfiguration<'line'> = {
      type: 'line',
      data: {
        labels: points.map(p => p.x),
        datasets: [{
          label: widget.title || 'Value',
          data: points.map(p => p.y),
          borderColor: color,
          backgroundColor: filled ? color + '22' : 'transparent',
          borderWidth: 2.5,
          pointRadius: points.length <= 30 ? 4 : 0,
          pointHoverRadius: 5,
          pointBackgroundColor: color,
          tension: 0.35,
          fill: filled,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: { duration: 300 },
        onClick: (_event, elements) => {
          const first = elements[0]
          setSelected(first ? points[first.index] ?? null : null)
        },
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: '#1f2937', titleColor: '#f9fafb', bodyColor: '#d1d5db',
            padding: 10, cornerRadius: 8,
            callbacks: {
              label: ctx => ' ' + displayValue(opts, ctx.raw, (opts.format as string) || ''),
            },
          },
        },
        scales: {
          x: {
            grid: { color: GRID_COLOR },
            ticks: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 }, maxTicksLimit: 8, maxRotation: 0 },
          },
          y: {
            grid: { color: GRID_COLOR },
            ticks: {
              color: TICK_COLOR, font: { family: getChartFont(), size: 11 },
              callback: (v) => displayValue(opts, v, (opts.format as string) || ''),
            },
          },
        },
      },
    }
    chartRef.current = new Chart(canvasRef.current, cfg)
    return () => { chartRef.current?.destroy() }
  }, [points, color, filled])

  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, data == null ? 'No data available.' : 'No series data.')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-chart-wrap">
      <div class="dw-chart-toolbar">
        <span>{points.length} point{points.length === 1 ? '' : 's'}</span>
        {selected && <button type="button" onClick={() => setSelected(null)}>Clear</button>}
      </div>
      <canvas ref={canvasRef} />
      {selected && (
        <div class="dw-chart-selection">
          Selected {selected.x}: {displayValue(opts, selected.y, (opts.format as string) || '')}
        </div>
      )}
    </div>
  )
}

export function AreaChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const next = {
    ...widget,
    code: {
      ...widget.code,
      options: {
        ...(widget.code?.options || {}),
        filled: widget.code?.options?.filled ?? true,
      },
    },
  }
  return <LineChartWidget widget={next} data={data} />
}

// ── bar chart ─────────────────────────────────────────────────────────────────

export function BarChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const chartRef  = useRef<Chart | null>(null)
  const [selected, setSelected] = useState<SeriesPoint | null>(null)
  const opts      = widgetOptions(widget)
  const points    = useMemo(() => extractSeries(opts, data ?? null), [data, opts])
  const color     = (opts.color as string) || paletteForSeries(1, opts)[0]

  useEffect(() => {
    if (!canvasRef.current) return
    chartRef.current?.destroy()
    const cfg: ChartConfiguration<'bar'> = {
      type: 'bar',
      data: {
        labels: points.map(p => p.x),
        datasets: [{
          label: widget.title || 'Value',
          data: points.map(p => p.y),
          backgroundColor: color + 'cc',
          borderColor: color,
          borderWidth: 1,
          borderRadius: 4,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: { duration: 300 },
        onClick: (_event, elements) => {
          const first = elements[0]
          setSelected(first ? points[first.index] ?? null : null)
        },
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: '#1f2937', titleColor: '#f9fafb', bodyColor: '#d1d5db',
            padding: 10, cornerRadius: 8,
            callbacks: {
              label: ctx => ' ' + displayValue(opts, ctx.raw, (opts.format as string) || ''),
            },
          },
        },
        scales: {
          x: {
            grid: { display: false },
            ticks: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 }, maxTicksLimit: 12, maxRotation: 0 },
          },
          y: {
            grid: { color: GRID_COLOR },
            ticks: {
              color: TICK_COLOR, font: { family: getChartFont(), size: 11 },
              callback: (v) => displayValue(opts, v, (opts.format as string) || ''),
            },
          },
        },
      },
    }
    chartRef.current = new Chart(canvasRef.current, cfg)
    return () => { chartRef.current?.destroy() }
  }, [points, color])

  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, data == null ? 'No data available.' : 'No series data.')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-chart-wrap">
      <div class="dw-chart-toolbar">
        <span>{points.length} point{points.length === 1 ? '' : 's'}</span>
        {selected && <button type="button" onClick={() => setSelected(null)}>Clear</button>}
      </div>
      <canvas ref={canvasRef} />
      {selected && (
        <div class="dw-chart-selection">
          Selected {selected.x}: {displayValue(opts, selected.y, (opts.format as string) || '')}
        </div>
      )}
    </div>
  )
}

export function PieChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const chartRef  = useRef<Chart | null>(null)
  const [selected, setSelected] = useState<PiePoint | null>(null)
  const opts = widgetOptions(widget)
  const points = useMemo(() => extractPieSeries(opts, data ?? null), [data, opts])
  const colors = useMemo(() => paletteForSeries(points.length, opts), [opts, points.length])

  useEffect(() => {
    if (!canvasRef.current) return
    chartRef.current?.destroy()
    const cfg: ChartConfiguration<'pie'> = {
      type: 'pie',
      data: {
        labels: points.map(p => p.label),
        datasets: [{
          data: points.map(p => p.value),
          backgroundColor: colors.map(color => color + 'dd'),
          borderColor: colors,
          borderWidth: 1.5,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: { duration: 300 },
        cutout: typeof opts.cutout === 'string' || typeof opts.cutout === 'number' ? opts.cutout : undefined,
        onClick: (_event, elements) => {
          const first = elements[0]
          setSelected(first ? points[first.index] ?? null : null)
        },
        plugins: {
          legend: {
            position: 'bottom',
            labels: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 }, boxWidth: 10, usePointStyle: true },
          },
          tooltip: {
            backgroundColor: '#1f2937', titleColor: '#f9fafb', bodyColor: '#d1d5db',
            padding: 10, cornerRadius: 8,
            callbacks: {
              label: ctx => ` ${ctx.label}: ${displayValue(opts, ctx.raw, (opts.format as string) || '')}`,
            },
          },
        },
      },
    }
    chartRef.current = new Chart(canvasRef.current, cfg)
    return () => { chartRef.current?.destroy() }
  }, [colors, opts, points])

  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, data == null ? 'No data available.' : 'No composition data.')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-chart-wrap">
      <div class="dw-chart-toolbar">
        <span>{points.length} segment{points.length === 1 ? '' : 's'}</span>
        {selected && <button type="button" onClick={() => setSelected(null)}>Clear</button>}
      </div>
      <canvas ref={canvasRef} />
      {selected && (
        <div class="dw-chart-selection">
          {selected.label}: {displayValue(opts, selected.value, (opts.format as string) || '')}
        </div>
      )}
    </div>
  )
}

export function DonutChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const next = {
    ...widget,
    code: {
      ...widget.code,
      options: {
        ...(widget.code?.options || {}),
        cutout: widget.code?.options?.cutout ?? '62%',
      },
    },
  }
  return <PieChartWidget widget={next} data={data} />
}

export function ScatterChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const chartRef = useRef<Chart | null>(null)
  const [selected, setSelected] = useState<ScatterPoint | null>(null)
  const opts = widgetOptions(widget)
  const points = useMemo(() => extractScatterSeries(opts, data ?? null), [data, opts])
  const color = (opts.color as string) || paletteForSeries(1, opts)[0]

  useEffect(() => {
    if (!canvasRef.current) return
    chartRef.current?.destroy()
    const cfg: ChartConfiguration<'scatter'> = {
      type: 'scatter',
      data: {
        datasets: [{
          label: widget.title || 'Values',
          data: points.map(point => ({ x: point.x, y: point.y })),
          pointRadius: 5,
          pointHoverRadius: 6,
          pointBackgroundColor: color,
          pointBorderColor: color,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: { duration: 300 },
        onClick: (_event, elements) => {
          const first = elements[0]
          setSelected(first ? points[first.index] ?? null : null)
        },
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: '#1f2937', titleColor: '#f9fafb', bodyColor: '#d1d5db',
            padding: 10, cornerRadius: 8,
            callbacks: {
              title: ctx => points[ctx[0]?.dataIndex ?? 0]?.label || '',
              label: ctx => ` ${displayValue(opts, ctx.raw && typeof ctx.raw === 'object' ? (ctx.raw as { y: number }).y : ctx.raw, (opts.format as string) || '')}`,
            },
          },
        },
        scales: {
          x: {
            grid: { color: GRID_COLOR },
            ticks: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 } },
          },
          y: {
            grid: { color: GRID_COLOR },
            ticks: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 }, callback: (v) => displayValue(opts, v, (opts.format as string) || '') },
          },
        },
      },
    }
    chartRef.current = new Chart(canvasRef.current, cfg)
    return () => { chartRef.current?.destroy() }
  }, [color, opts, points, widget.title])

  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, data == null ? 'No data available.' : 'No scatter data.')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-chart-wrap">
      <div class="dw-chart-toolbar">
        <span>{points.length} point{points.length === 1 ? '' : 's'}</span>
        {selected && <button type="button" onClick={() => setSelected(null)}>Clear</button>}
      </div>
      <canvas ref={canvasRef} />
      {selected && (
        <div class="dw-chart-selection">
          {selected.label}: {displayValue(opts, selected.y, (opts.format as string) || '')}
        </div>
      )}
    </div>
  )
}

export function StackedChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const chartRef = useRef<Chart | null>(null)
  const opts = widgetOptions(widget)
  const seriesPath = (opts.seriesPath as string) || (opts.path as string) || ''
  const xKey = (opts.x as string) || 'date'
  const seriesKeys = Array.isArray(opts.seriesKeys) ? opts.seriesKeys.map(value => asString(value)).filter(Boolean) : []
  const raw = seriesPath ? valueAtPath(data, seriesPath) : data
  const rows = Array.isArray(raw) ? raw : asArray(data)
  const colors = paletteForSeries(Math.max(1, seriesKeys.length), opts)

  useEffect(() => {
    if (!canvasRef.current) return
    chartRef.current?.destroy()
    const cfg: ChartConfiguration<'bar'> = {
      type: 'bar',
      data: {
        labels: rows.map(item => item && typeof item === 'object' ? asString((item as Record<string, unknown>)[xKey]) : ''),
        datasets: seriesKeys.map((key, index) => ({
          label: key,
          data: rows.map(item => item && typeof item === 'object' ? Number((item as Record<string, unknown>)[key] ?? 0) : 0),
          backgroundColor: colors[index] + 'cc',
          borderColor: colors[index],
          borderWidth: 1,
          borderRadius: 4,
          stack: 'total',
        })),
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { position: 'bottom', labels: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 } } },
          tooltip: {
            backgroundColor: '#1f2937', titleColor: '#f9fafb', bodyColor: '#d1d5db',
            padding: 10, cornerRadius: 8,
            callbacks: {
              label: ctx => ` ${ctx.dataset.label}: ${displayValue(opts, ctx.raw, (opts.format as string) || '')}`,
            },
          },
        },
        scales: {
          x: { stacked: true, grid: { display: false }, ticks: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 } } },
          y: { stacked: true, grid: { color: GRID_COLOR }, ticks: { color: TICK_COLOR, font: { family: getChartFont(), size: 11 }, callback: v => displayValue(opts, v, (opts.format as string) || '') } },
        },
      },
    }
    chartRef.current = new Chart(canvasRef.current, cfg)
    return () => { chartRef.current?.destroy() }
  }, [colors, opts, rows, seriesKeys, xKey])

  if (rows.length === 0 || seriesKeys.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No stacked data.')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-chart-wrap">
      <div class="dw-chart-toolbar">
        <span>{rows.length} row{rows.length === 1 ? '' : 's'}</span>
      </div>
      <canvas ref={canvasRef} />
    </div>
  )
}

// ── table ─────────────────────────────────────────────────────────────────────

export function TableWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const [query, setQuery] = useState('')
  const [sortCol, setSortCol] = useState<string | null>(null)
  const [sortDir, setSortDir] = useState<SortDirection>('asc')
  const [page, setPage] = useState(0)
  const opts = widgetOptions(widget)
  const path         = (opts.path as string)    || ''
  const limit        = (opts.limit as number)   || 100
  const pageSize     = Math.max(1, Number(opts.pageSize ?? 10))
  const explicitCols = opts.columns as string[] | undefined
  const raw          = path ? valueAtPath(data, path) : data
  const rows         = Array.isArray(raw)
    ? raw
    : raw && typeof raw === 'object'
      ? Object.entries(raw as Record<string, unknown>).map(([key, value]) => {
          const keyCol = explicitCols?.[0] || 'key'
          const valueCol = explicitCols?.[1] || 'value'
          return { [keyCol]: key, [valueCol]: value }
        })
      : asArray(raw)
  const trimmed = rows.slice(0, limit)

  useEffect(() => { setPage(0) }, [query, sortCol, sortDir, data])

  if (trimmed.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No data')}</div>
  }
  const sample  = trimmed[0]
  const columns = explicitCols ?? (
    sample && typeof sample === 'object'
      ? Object.keys(sample as Record<string, unknown>).slice(0, 8)
      : ['value']
  )
  const filtered = query.trim()
    ? trimmed.filter(row => columns.some(col => asString(cellValue(row, col)).toLowerCase().includes(query.trim().toLowerCase())))
    : trimmed
  const sorted = sortCol
    ? [...filtered].sort((a, b) => compareCells(cellValue(a, sortCol), cellValue(b, sortCol), sortDir))
    : filtered
  const pageCount = Math.max(1, Math.ceil(sorted.length / pageSize))
  const safePage = Math.min(page, pageCount - 1)
  const visibleRows = sorted.slice(safePage * pageSize, safePage * pageSize + pageSize)

  function toggleSort(col: string) {
    if (sortCol === col) {
      setSortDir(dir => dir === 'asc' ? 'desc' : 'asc')
    } else {
      setSortCol(col)
      setSortDir('asc')
    }
  }

  return (
    <div class="dashboard-widget-body dashboard-widget-table">
      <div class="dw-table-controls">
        <input
          aria-label={`${widget.title || 'Table'} search`}
          placeholder="Search rows"
          value={query}
          onInput={e => setQuery((e.currentTarget as HTMLInputElement).value)}
        />
        <span>{sorted.length} row{sorted.length === 1 ? '' : 's'}</span>
      </div>
      {visibleRows.length === 0 ? (
        <div class="dashboard-empty">No matching rows</div>
      ) : (
        <table>
          <thead>
            <tr>
              {columns.map(col => (
                <th key={col}>
                  <button type="button" onClick={() => toggleSort(col)} aria-label={`Sort by ${col}`}>
                    {col}{sortCol === col ? (sortDir === 'asc' ? ' ▲' : ' ▼') : ''}
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {visibleRows.map((row, i) => (
              <tr key={i}>
                {columns.map(col => <td key={col}>{displayValue(opts, cellValue(row, col), col.toLowerCase().includes('percent') ? 'percent' : undefined)}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {pageCount > 1 && (
        <div class="dw-table-pagination">
          <button type="button" onClick={() => setPage(p => Math.max(0, p - 1))} disabled={safePage === 0}>Previous</button>
          <span>Page {safePage + 1} of {pageCount}</span>
          <button type="button" onClick={() => setPage(p => Math.min(pageCount - 1, p + 1))} disabled={safePage >= pageCount - 1}>Next</button>
        </div>
      )}
    </div>
  )
}

// ── list ──────────────────────────────────────────────────────────────────────

export function ListWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const [expanded, setExpanded] = useState(false)
  const opts = widgetOptions(widget)
  const itemsPath = (opts.itemsPath as string) || ''
  const labelKey  = (opts.labelKey as string)  || ''
  const subKey    = (opts.subKey as string)    || ''
  const limit     = (opts.limit as number)     || 50
  const visibleCount = Math.max(1, Number(opts.visibleCount ?? 5))
  const items     = asArray(itemsPath ? valueAtPath(data, itemsPath) : data)
  const limited   = items.slice(0, limit)
  const visible   = expanded ? limited : limited.slice(0, visibleCount)

  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No items')}</div>
  }
  return (
    <div class="dashboard-widget-body dashboard-widget-list">
      <ul>
        {visible.map((item, i) => {
          let label: string
          let sub = ''
          if (item && typeof item === 'object') {
            const obj = item as Record<string, unknown>
            label = labelKey
              ? asString(obj[labelKey])
              : asString(obj.title ?? obj.name ?? obj.message ?? JSON.stringify(item))
            if (subKey) sub = asString(obj[subKey])
          } else {
            label = asString(item)
          }
          return (
            <li key={i}>
              <div>{label}</div>
              {sub && <div class="dashboard-list-sub">{displayValue(opts, sub)}</div>}
            </li>
          )
        })}
      </ul>
      {limited.length > visibleCount && (
        <button class="dw-list-toggle" type="button" onClick={() => setExpanded(value => !value)}>
          {expanded ? 'Show less' : `Show ${limited.length - visibleCount} more`}
        </button>
      )}
    </div>
  )
}

// ── status grid ───────────────────────────────────────────────────────────────

export function StatusGridWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const itemsPath = (opts.itemsPath as string) || (opts.path as string) || ''
  const labelKey = (opts.labelKey as string) || 'title'
  const statusKey = (opts.statusKey as string) || 'status'
  const subKey = (opts.subKey as string) || ''
  const limit = (opts.limit as number) || 24
  const items = asArray(itemsPath ? valueAtPath(data, itemsPath) : data).slice(0, limit)

  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No statuses')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-status-grid">
      {items.map((item, i) => {
        const obj = item && typeof item === 'object' ? item as Record<string, unknown> : { title: item, status: item }
        const label = asString(obj[labelKey] ?? obj.name ?? obj.title ?? item)
        const status = obj[statusKey] ?? obj.state ?? obj.status
        const tone = statusTone(status)
        const sub = subKey ? asString(obj[subKey]) : displayValue(opts, status)
        return (
          <div class={`dw-status-item dw-tone-${tone}`} key={i}>
            <span class="dw-status-dot" aria-hidden="true" />
            <div>
              <strong>{label}</strong>
              {sub && <span>{sub}</span>}
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function KPIGroupWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const itemsPath = (opts.itemsPath as string) || (opts.path as string) || ''
  const labelKey = (opts.labelKey as string) || 'label'
  const valueKey = (opts.valueKey as string) || 'value'
  const deltaKey = (opts.deltaKey as string) || ''
  const toneKey = (opts.toneKey as string) || 'tone'
  const format = (opts.format as string) || 'decimal'
  const limit = (opts.limit as number) || 8
  const items = asArray(itemsPath ? valueAtPath(data, itemsPath) : data).slice(0, limit)

  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No KPI values')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-kpi-group">
      {items.map((item, i) => {
        const obj = item && typeof item === 'object' ? item as Record<string, unknown> : { label: item, value: item }
        const label = asString(obj[labelKey] ?? obj.title ?? obj.name ?? item)
        const value = obj[valueKey]
        const delta = deltaKey ? obj[deltaKey] : undefined
        const deltaN = delta !== undefined ? Number(delta) : NaN
        const numericValue = typeof value === 'number' ? value : Number(value)
        const tone = metricTone(obj[toneKey] ?? (Number.isFinite(numericValue) ? toneForNumericValue(opts, numericValue, Number.isFinite(deltaN) ? (deltaN >= 0 ? 'ok' : 'warn') : 'neutral') : (Number.isFinite(deltaN) ? (deltaN >= 0 ? 'ok' : 'warn') : 'neutral')))
        return (
          <div class={`dw-kpi-card dw-tone-${tone}`} key={i}>
            <span class="dw-kpi-label">{label}</span>
            <strong class="dw-kpi-value">{displayValue(opts, value, format)}</strong>
            {Number.isFinite(deltaN) && (
              <span class={`dw-kpi-delta ${deltaN >= 0 ? 'up' : 'down'}`}>
                {deltaN >= 0 ? '+' : ''}{deltaN.toFixed(1)}
              </span>
            )}
          </div>
        )
      })}
    </div>
  )
}

export function TimelineWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const itemsPath = (opts.itemsPath as string) || (opts.path as string) || ''
  const timeKey = (opts.timeKey as string) || 'date'
  const labelKey = (opts.labelKey as string) || 'title'
  const bodyKey = (opts.bodyKey as string) || 'summary'
  const statusKey = (opts.statusKey as string) || 'status'
  const limit = Math.max(1, Number(opts.limit ?? 12))
  const items = asArray(itemsPath ? valueAtPath(data, itemsPath) : data).slice(0, limit)

  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No timeline events')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-timeline">
      {items.map((item, index) => {
        const obj = item && typeof item === 'object' ? item as Record<string, unknown> : {}
        const tone = statusTone(obj[statusKey])
        return (
          <div class={`dw-timeline-item dw-tone-${tone}`} key={index}>
            <div class="dw-timeline-rail">
              <span class="dw-timeline-dot" aria-hidden="true" />
            </div>
            <div class="dw-timeline-content">
              <div class="dw-timeline-time">{displayValue(opts, obj[timeKey])}</div>
              <strong>{displayValue(opts, obj[labelKey])}</strong>
              {obj[bodyKey] !== undefined && <p>{displayValue(opts, obj[bodyKey])}</p>}
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function HeatmapWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const seriesPath = (opts.seriesPath as string) || (opts.path as string) || ''
  const dateKey = (opts.dateKey as string) || 'date'
  const valueKey = (opts.valueKey as string) || 'value'
  const raw = seriesPath ? valueAtPath(data, seriesPath) : data
  const rows = (Array.isArray(raw) ? raw : asArray(data)).flatMap(item => {
    if (!item || typeof item !== 'object') return []
    const obj = item as Record<string, unknown>
    const date = new Date(asString(obj[dateKey]))
    const value = Number(obj[valueKey])
    if (Number.isNaN(date.getTime()) || !Number.isFinite(value)) return []
    return [{ date, value }]
  }).sort((a, b) => a.date.getTime() - b.date.getTime())
  const maxValue = rows.reduce((max, row) => Math.max(max, row.value), 0)
  const colors = paletteForSeries(5, opts)

  if (rows.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'No heatmap data')}</div>
  }
  return (
    <div class="dashboard-widget-body dw-heatmap">
      {rows.map((row, index) => {
        const intensity = maxValue > 0 ? row.value / maxValue : 0
        const colorIndex = Math.min(colors.length - 1, Math.floor(intensity * colors.length))
        return (
          <div class="dw-heatmap-cell" key={index} title={`${row.date.toLocaleDateString()}: ${displayValue(opts, row.value, (opts.format as string) || '')}`}>
            <span>{row.date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span>
            <strong style={{ color: colors[colorIndex] }}>{displayValue(opts, row.value, (opts.format as string) || '')}</strong>
          </div>
        )
      })}
    </div>
  )
}

// ── markdown ──────────────────────────────────────────────────────────────────

export function MarkdownWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const text = (opts.text as string) || ''
  const path = (opts.path as string) || ''
  let body = text
  if (!body) {
    // Drill into a specific path if the agent specified one (e.g. path="headline"
    // to pull a single string field out of a larger JSON object).
    const raw = path ? valueAtPath(data, path) : data
    if (raw !== undefined && raw !== null) {
      if (typeof raw === 'string') {
        body = raw
      } else {
        try { body = '```json\n' + JSON.stringify(raw, null, 2) + '\n```' }
        catch { body = String(raw) }
      }
    }
  }
  if (!body) return <div class="dashboard-widget-body dashboard-empty">{emptyMessage(widget, 'Nothing to show')}</div>
  const html = DOMPurify.sanitize(marked.parse(body) as string, {
    ALLOWED_TAGS: ['p','br','strong','b','em','i','code','pre','a','ul','ol','li',
                   'h1','h2','h3','h4','table','thead','tbody','tr','th','td',
                   'blockquote','hr','span'],
    ADD_ATTR: ['target', 'rel', 'class'],
  })
  return (
    <div
      class="dashboard-widget-body dashboard-widget-markdown"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

// ── code (agent-authored TSX) ─────────────────────────────────────────────────

export function CodeWidget({
  widget,
  data,
  onAction,
}: {
  widget: DashboardWidget
  data: unknown
  onAction?: (action: DashboardWidgetAction) => void
}): JSX.Element {
  const compiled = widget.code?.compiled || ''
  const hash     = widget.code?.hash     || ''
  if (!compiled) {
    return <div class="dashboard-widget-body dashboard-widget-error">⚠ widget has no compiled code</div>
  }
  return (
    <div class="dashboard-widget-body dashboard-widget-code">
      <DashboardCodeFrame hash={hash} compiled={compiled} data={data} onAction={onAction} />
    </div>
  )
}

// ── dispatch ──────────────────────────────────────────────────────────────────

export function WidgetRenderer(
  {
    widget,
    data,
    error,
    onAction,
  }: {
    widget: DashboardWidget
    data: unknown
    error?: string
    onAction?: (action: DashboardWidgetAction) => void
  }
): JSX.Element {
  if (error) {
    const errorText = widgetOptions(widget).errorText
    return <div class="dashboard-widget-body dashboard-widget-error">⚠ {typeof errorText === 'string' && errorText.trim() ? errorText : error}</div>
  }
  if (widget.code?.mode === 'code') {
    return <CodeWidget widget={widget} data={data} onAction={onAction} />
  }
  // preset
  switch (widget.code?.preset) {
    case 'metric':     return <MetricWidget    widget={widget} data={data} />
    case 'table':      return <TableWidget     widget={widget} data={data} />
    case 'line_chart': return <LineChartWidget widget={widget} data={data} />
    case 'area_chart': return <AreaChartWidget widget={widget} data={data} />
    case 'bar_chart':  return <BarChartWidget  widget={widget} data={data} />
    case 'pie_chart':  return <PieChartWidget  widget={widget} data={data} />
    case 'donut_chart': return <DonutChartWidget widget={widget} data={data} />
    case 'scatter_chart': return <ScatterChartWidget widget={widget} data={data} />
    case 'stacked_chart': return <StackedChartWidget widget={widget} data={data} />
    case 'markdown':   return <MarkdownWidget  widget={widget} data={data} />
    case 'list':       return <ListWidget      widget={widget} data={data} />
    case 'timeline':   return <TimelineWidget  widget={widget} data={data} />
    case 'heatmap':    return <HeatmapWidget   widget={widget} data={data} />
    case 'progress':   return <ProgressWidget  widget={widget} data={data} />
    case 'gauge':      return <GaugeWidget     widget={widget} data={data} />
    case 'status_grid': return <StatusGridWidget widget={widget} data={data} />
    case 'kpi_group':  return <KPIGroupWidget widget={widget} data={data} />
    default:
      return <div class="dashboard-widget-body dashboard-empty">Unknown preset: {widget.code?.preset || '(none)'}</div>
  }
}
