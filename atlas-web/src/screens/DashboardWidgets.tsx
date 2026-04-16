// Built-in preset renderers for v2 dashboard widgets.
//
// Each preset receives the widget definition and the resolved data payload.
// Charts use Chart.js. Metric, Table, List, and Markdown are Preact
// components. Agent-authored code widgets are delegated to
// DashboardCodeFrame (sandboxed iframe).

import { JSX } from 'preact'
import { useEffect, useMemo, useRef } from 'preact/hooks'
import {
  Chart,
  LineController, BarController,
  LineElement, BarElement, PointElement,
  LinearScale, CategoryScale, TimeScale,
  Filler, Tooltip, Legend,
  type ChartConfiguration,
} from 'chart.js'
import type { DashboardWidget } from '../api/client'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { DashboardCodeFrame } from './DashboardCodeFrame'

Chart.register(
  LineController, BarController,
  LineElement, BarElement, PointElement,
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

function extractSeries(opts: Record<string, unknown>, data: unknown): SeriesPoint[] {
  const seriesPath = (opts.seriesPath as string) || ''
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
  if (!path && raw && typeof raw === 'object' && !Array.isArray(raw)) {
    const obj = raw as Record<string, unknown>
    const numericKey = Object.keys(obj).find(k => typeof obj[k] === 'number')
    if (numericKey) raw = obj[numericKey]
    else if (typeof obj.text === 'string') raw = obj.text
  }
  const display = formatValue(raw, format)
  const change  = changePath ? valueAtPath(data, changePath) : undefined
  const changeN = change !== undefined ? Number(change) : NaN
  const trendUp = Number.isFinite(changeN) && changeN > 0
  const trendDn = Number.isFinite(changeN) && changeN < 0

  return (
    <div class="dashboard-widget-body dw-metric">
      <div class="dw-metric-value">{prefix}{display}{suffix}</div>
      {Number.isFinite(changeN) && (
        <div class={`dw-metric-change ${trendUp ? 'up' : trendDn ? 'down' : ''}`}>
          {trendUp ? '▲' : trendDn ? '▼' : '●'} {Math.abs(changeN).toFixed(2)}
        </div>
      )}
      {label && <div class="dw-metric-label">{label}</div>}
    </div>
  )
}

// ── line chart ────────────────────────────────────────────────────────────────

export function LineChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const chartRef  = useRef<Chart | null>(null)
  const opts      = widgetOptions(widget)
  const points    = useMemo(() => extractSeries(opts, data), [data, opts])
  const color     = (opts.color as string) || '#3b82f6'
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
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: '#1f2937', titleColor: '#f9fafb', bodyColor: '#d1d5db',
            padding: 10, cornerRadius: 8,
            callbacks: {
              label: ctx => ' ' + formatValue(ctx.raw, (opts.format as string) || ''),
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
              callback: (v) => formatValue(v, (opts.format as string) || ''),
            },
          },
        },
      },
    }
    chartRef.current = new Chart(canvasRef.current, cfg)
    return () => { chartRef.current?.destroy() }
  }, [points, color, filled])

  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No series data</div>
  }
  return (
    <div class="dashboard-widget-body dw-chart-wrap">
      <canvas ref={canvasRef} />
    </div>
  )
}

// ── bar chart ─────────────────────────────────────────────────────────────────

export function BarChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const chartRef  = useRef<Chart | null>(null)
  const opts      = widgetOptions(widget)
  const points    = useMemo(() => extractSeries(opts, data), [data, opts])
  const color     = (opts.color as string) || '#6366f1'

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
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: '#1f2937', titleColor: '#f9fafb', bodyColor: '#d1d5db',
            padding: 10, cornerRadius: 8,
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
              callback: (v) => formatValue(v, (opts.format as string) || ''),
            },
          },
        },
      },
    }
    chartRef.current = new Chart(canvasRef.current, cfg)
    return () => { chartRef.current?.destroy() }
  }, [points, color])

  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No series data</div>
  }
  return (
    <div class="dashboard-widget-body dw-chart-wrap">
      <canvas ref={canvasRef} />
    </div>
  )
}

// ── table ─────────────────────────────────────────────────────────────────────

export function TableWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const path         = (opts.path as string)    || ''
  const limit        = (opts.limit as number)   || 100
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

  if (trimmed.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No data</div>
  }
  const sample  = trimmed[0]
  const columns = explicitCols ?? (
    sample && typeof sample === 'object'
      ? Object.keys(sample as Record<string, unknown>).slice(0, 8)
      : ['value']
  )
  return (
    <div class="dashboard-widget-body dashboard-widget-table">
      <table>
        <thead>
          <tr>{columns.map(col => <th key={col}>{col}</th>)}</tr>
        </thead>
        <tbody>
          {trimmed.map((row, i) => (
            <tr key={i}>
              {columns.map(col => {
                const cell = row && typeof row === 'object'
                  ? (row as Record<string, unknown>)[col]
                  : row
                return <td key={col}>{asString(cell)}</td>
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ── list ──────────────────────────────────────────────────────────────────────

export function ListWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const opts = widgetOptions(widget)
  const itemsPath = (opts.itemsPath as string) || ''
  const labelKey  = (opts.labelKey as string)  || ''
  const subKey    = (opts.subKey as string)    || ''
  const limit     = (opts.limit as number)     || 50
  const items     = asArray(itemsPath ? valueAtPath(data, itemsPath) : data)

  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No items</div>
  }
  return (
    <div class="dashboard-widget-body dashboard-widget-list">
      <ul>
        {items.slice(0, limit).map((item, i) => {
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
              {sub && <div class="dashboard-list-sub">{sub}</div>}
            </li>
          )
        })}
      </ul>
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
  if (!body) return <div class="dashboard-widget-body dashboard-empty">Nothing to show</div>
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

export function CodeWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const compiled = widget.code?.compiled || ''
  const hash     = widget.code?.hash     || ''
  if (!compiled) {
    return <div class="dashboard-widget-body dashboard-widget-error">⚠ widget has no compiled code</div>
  }
  return (
    <div class="dashboard-widget-body dashboard-widget-code">
      <DashboardCodeFrame hash={hash} compiled={compiled} data={data} />
    </div>
  )
}

// ── dispatch ──────────────────────────────────────────────────────────────────

export function WidgetRenderer(
  { widget, data, error }: { widget: DashboardWidget; data: unknown; error?: string }
): JSX.Element {
  if (error) {
    return <div class="dashboard-widget-body dashboard-widget-error">⚠ {error}</div>
  }
  if (widget.code?.mode === 'code') {
    return <CodeWidget widget={widget} data={data} />
  }
  // preset
  switch (widget.code?.preset) {
    case 'metric':     return <MetricWidget    widget={widget} data={data} />
    case 'table':      return <TableWidget     widget={widget} data={data} />
    case 'line_chart': return <LineChartWidget widget={widget} data={data} />
    case 'bar_chart':  return <BarChartWidget  widget={widget} data={data} />
    case 'markdown':   return <MarkdownWidget  widget={widget} data={data} />
    case 'list':       return <ListWidget      widget={widget} data={data} />
    default:
      return <div class="dashboard-widget-body dashboard-empty">Unknown preset: {widget.code?.preset || '(none)'}</div>
  }
}
