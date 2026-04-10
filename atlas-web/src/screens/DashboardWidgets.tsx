// Built-in dashboard widget renderers.
//
// Each component receives a Widget definition and the resolved data payload.
// Charts use Chart.js. Metric, News, List, Table, and Markdown are custom
// Preact components. custom_html stays for hand-authored widgets only — it
// is not available to the AI generator.

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

Chart.register(
  LineController, BarController,
  LineElement, BarElement, PointElement,
  LinearScale, CategoryScale, TimeScale,
  Filler, Tooltip, Legend,
)

// ── helpers ───────────────────────────────────────────────────────────────────

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
    default:         return typeof v === 'number' ? n.toLocaleString() : String(v)
  }
}

function asArray(v: unknown): unknown[] {
  if (Array.isArray(v)) return v
  if (v && typeof v === 'object') {
    const obj = v as Record<string, unknown>
    for (const key of ['rows', 'items', 'results', 'data', 'events', 'history']) {
      if (Array.isArray(obj[key])) return obj[key] as unknown[]
    }
  }
  return []
}

function asString(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  return String(v)
}

interface SeriesPoint { x: string; y: number }

function extractSeries(widget: DashboardWidget, data: unknown): SeriesPoint[] {
  const seriesPath = (widget.options?.seriesPath as string) || ''
  const xKey = (widget.options?.x as string) || 'date'
  const yKey = (widget.options?.y as string) || 'value'
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

// Shared Chart.js default styles
const CHART_FONT = '-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif'
const GRID_COLOR  = 'rgba(156,163,175,0.15)'
const TICK_COLOR  = 'rgba(107,114,128,0.9)'

// ── metric ────────────────────────────────────────────────────────────────────

export function MetricWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const path    = (widget.options?.path as string)    || ''
  const format  = (widget.options?.format as string)  || ''
  const label   = (widget.options?.label as string)   || widget.description || ''
  const prefix  = (widget.options?.prefix as string)  || ''
  const suffix  = (widget.options?.suffix as string)  || ''
  const changePath = (widget.options?.changePath as string) || ''
  const raw     = path ? valueAtPath(data, path) : data
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
  const points    = useMemo(() => extractSeries(widget, data), [data, widget.options])
  const color     = (widget.options?.color as string) || '#3b82f6'
  const filled    = (widget.options?.filled as boolean) ?? true

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
            backgroundColor: '#1f2937',
            titleColor: '#f9fafb',
            bodyColor: '#d1d5db',
            padding: 10,
            cornerRadius: 8,
            callbacks: {
              label: ctx => {
                const fmt = (widget.options?.format as string) || ''
                return ' ' + formatValue(ctx.raw, fmt)
              },
            },
          },
        },
        scales: {
          x: {
            grid: { color: GRID_COLOR },
            ticks: { color: TICK_COLOR, font: { family: CHART_FONT, size: 11 }, maxTicksLimit: 8, maxRotation: 0 },
          },
          y: {
            grid: { color: GRID_COLOR },
            ticks: {
              color: TICK_COLOR,
              font: { family: CHART_FONT, size: 11 },
              callback: (v) => formatValue(v, (widget.options?.format as string) || ''),
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
  const points    = useMemo(() => extractSeries(widget, data), [data, widget.options])
  const color     = (widget.options?.color as string) || '#6366f1'

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
            backgroundColor: '#1f2937',
            titleColor: '#f9fafb',
            bodyColor: '#d1d5db',
            padding: 10,
            cornerRadius: 8,
          },
        },
        scales: {
          x: {
            grid: { display: false },
            ticks: { color: TICK_COLOR, font: { family: CHART_FONT, size: 11 }, maxTicksLimit: 12, maxRotation: 0 },
          },
          y: {
            grid: { color: GRID_COLOR },
            ticks: {
              color: TICK_COLOR,
              font: { family: CHART_FONT, size: 11 },
              callback: (v) => formatValue(v, (widget.options?.format as string) || ''),
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

// ── news / results feed ───────────────────────────────────────────────────────
// Renders structured search results: [{title, description, url}]
// Sourced from websearch.query (resolver parses into data.results[]).

export function NewsWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const itemsPath   = (widget.options?.itemsPath as string) || 'results'
  const titleKey    = (widget.options?.titleKey as string)  || 'title'
  const bodyKey     = (widget.options?.bodyKey as string)   || 'description'
  const urlKey      = (widget.options?.urlKey as string)    || 'url'
  const limit       = (widget.options?.limit as number)     || 6

  const raw   = valueAtPath(data, itemsPath)
  const items = Array.isArray(raw) ? raw : asArray(data)

  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No results</div>
  }

  return (
    <div class="dashboard-widget-body dw-news">
      {items.slice(0, limit).map((item, i) => {
        if (!item || typeof item !== 'object') return null
        const obj   = item as Record<string, unknown>
        const title = asString(obj[titleKey] || obj.title || obj.name || `Result ${i + 1}`)
        const body  = asString(obj[bodyKey]  || obj.description || obj.summary || '')
        const url   = asString(obj[urlKey]   || obj.url || '')
        return (
          <div key={i} class="dw-news-card">
            <div class="dw-news-title">{title}</div>
            {body  && <div class="dw-news-body">{body}</div>}
            {url   && <div class="dw-news-url">{url}</div>}
          </div>
        )
      })}
    </div>
  )
}

// ── table ─────────────────────────────────────────────────────────────────────

export function TableWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const path         = (widget.options?.path as string) || ''
  const limit        = (widget.options?.limit as number)    || 100
  const explicitCols = widget.options?.columns as string[] | undefined
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
  const trimmed      = rows.slice(0, limit)

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
  const itemsPath = (widget.options?.itemsPath as string) || ''
  const labelKey  = (widget.options?.labelKey as string)  || ''
  const limit     = (widget.options?.limit as number)     || 50
  const items     = asArray(itemsPath ? valueAtPath(data, itemsPath) : data)

  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No items</div>
  }
  return (
    <div class="dashboard-widget-body dashboard-widget-list">
      <ul>
        {items.slice(0, limit).map((item, i) => {
          let label: string
          if (item && typeof item === 'object' && labelKey) {
            label = asString((item as Record<string, unknown>)[labelKey])
          } else if (item && typeof item === 'object') {
            const obj = item as Record<string, unknown>
            label = asString(obj.title ?? obj.name ?? obj.message ?? JSON.stringify(item))
          } else {
            label = asString(item)
          }
          return <li key={i}>{label}</li>
        })}
      </ul>
    </div>
  )
}

// ── markdown ──────────────────────────────────────────────────────────────────

export function MarkdownWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const text = (widget.options?.text as string) || ''
  let body = text
  if (!body && data !== undefined) {
    try { body = '```json\n' + JSON.stringify(data, null, 2) + '\n```' }
    catch { body = String(data) }
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

// ── custom html (manual / hand-authored only) ─────────────────────────────────

function buildCustomSrcdoc(widget: DashboardWidget): string {
  const html  = widget.html || '<div id="root"></div>'
  const css   = widget.css  || ''
  const js    = widget.js   || ''
  const safeJS = js.replace(/<\/script>/gi, '<\\/script>')
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; font-src data:;">
<style>
html,body{margin:0;padding:0;height:100%;box-sizing:border-box;}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',system-ui,sans-serif;color:#e5e7eb;background:transparent;font-size:13px;line-height:1.45;padding:10px 12px;}
*{box-sizing:border-box;}
${css}
</style>
</head>
<body>
${html}
<script>
(function(){
  var atlasData=null;
  function tryRender(){
    if(typeof window.atlasRender==='function'){
      try{window.atlasRender(atlasData);}
      catch(err){document.body.innerHTML='<pre style="color:#f87171;font-family:monospace">'+String(err&&err.message||err)+'</pre>';}
    }
  }
  window.addEventListener('message',function(e){
    if(e&&e.data&&e.data.type==='atlas:data'){atlasData=e.data.data;tryRender();}
  });
  try{window.parent.postMessage({type:'atlas:ready'},'*');}catch(_){}
})();
${safeJS}
</script>
</body>
</html>`
}

export function CustomHtmlWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const srcdoc    = useMemo(() => buildCustomSrcdoc(widget), [widget.html, widget.css, widget.js])
  const dataRef   = useRef<unknown>(data)
  dataRef.current = data

  useEffect(() => {
    const iframe = iframeRef.current
    if (!iframe) return undefined
    function send() {
      try { iframe?.contentWindow?.postMessage({ type: 'atlas:data', data: dataRef.current }, '*') }
      catch { /* iframe gone */ }
    }
    function onMessage(e: MessageEvent) {
      if (e.source !== iframe?.contentWindow) return
      const payload = e.data as { type?: string } | undefined
      if (payload?.type === 'atlas:ready') send()
    }
    window.addEventListener('message', onMessage)
    iframe.addEventListener('load', send)
    send()
    return () => {
      window.removeEventListener('message', onMessage)
      iframe.removeEventListener('load', send)
    }
  }, [srcdoc])

  useEffect(() => {
    try { iframeRef.current?.contentWindow?.postMessage({ type: 'atlas:data', data }, '*') }
    catch { /* ignore */ }
  }, [data])

  return (
    <div class="dashboard-widget-body dashboard-widget-custom">
      <iframe
        ref={iframeRef}
        sandbox="allow-scripts"
        srcDoc={srcdoc}
        title={widget.title || 'Custom widget'}
        style={{ width: '100%', height: '100%', border: 'none', background: 'transparent', display: 'block' }}
      />
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
  switch (widget.kind) {
    case 'metric':      return <MetricWidget     widget={widget} data={data} />
    case 'table':       return <TableWidget      widget={widget} data={data} />
    case 'line_chart':  return <LineChartWidget  widget={widget} data={data} />
    case 'bar_chart':   return <BarChartWidget   widget={widget} data={data} />
    case 'markdown':    return <MarkdownWidget   widget={widget} data={data} />
    case 'list':        return <ListWidget       widget={widget} data={data} />
    case 'news':        return <NewsWidget       widget={widget} data={data} />
    case 'custom_html': return <CustomHtmlWidget widget={widget} data={data} />
    default:
      return <div class="dashboard-widget-body dashboard-empty">Unknown widget: {widget.kind}</div>
  }
}
