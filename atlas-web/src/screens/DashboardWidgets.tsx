// Built-in dashboard widget renderers.
//
// Each component receives a Widget definition and the resolved data payload
// (whatever the runtime /resolve endpoint returned). Widgets must be tolerant
// of missing/wrong-shaped data — the runtime guarantees safety, not schema.
//
// Stage 4 ships six built-in widget kinds. The custom_html iframe widget
// lands in stage 5; the renderer falls through to a placeholder for it now.

import { JSX } from 'preact'
import { useEffect, useMemo, useRef } from 'preact/hooks'
import type { DashboardWidget } from '../api/client'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

// ── helpers ───────────────────────────────────────────────────────────────────

/** valueAtPath drills into a nested object using a dotted path ("a.b.c"). */
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

function formatNumber(v: unknown, format?: string): string {
  if (v === null || v === undefined) return '—'
  const n = typeof v === 'number' ? v : Number(v)
  if (Number.isNaN(n)) return String(v)
  switch (format) {
    case 'currency':
      return `$${n.toFixed(2)}`
    case 'integer':
      return Math.round(n).toLocaleString()
    case 'percent':
      return `${(n * 100).toFixed(1)}%`
    default:
      return n.toLocaleString()
  }
}

function asArray(v: unknown): unknown[] {
  if (Array.isArray(v)) return v
  if (v && typeof v === 'object' && 'rows' in (v as Record<string, unknown>)) {
    const rows = (v as Record<string, unknown>).rows
    if (Array.isArray(rows)) return rows
  }
  return []
}

function asString(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  return String(v)
}

// ── metric ────────────────────────────────────────────────────────────────────

export function MetricWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const path = (widget.options?.path as string) || ''
  const format = (widget.options?.format as string) || ''
  const raw = path ? valueAtPath(data, path) : data
  const display = formatNumber(raw, format)
  return (
    <div class="dashboard-widget-body dashboard-widget-metric">
      <div class="dashboard-metric-value">{display}</div>
      {widget.description && <div class="dashboard-metric-label">{widget.description}</div>}
    </div>
  )
}

// ── table ─────────────────────────────────────────────────────────────────────

export function TableWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const rows = asArray(data)
  const limit = (widget.options?.limit as number) || 100
  const explicitCols = widget.options?.columns as string[] | undefined
  const trimmed = rows.slice(0, limit)
  if (trimmed.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No data</div>
  }
  // Derive columns from the first row if not declared explicitly.
  const sample = trimmed[0]
  const columns = explicitCols ?? (sample && typeof sample === 'object' ? Object.keys(sample as Record<string, unknown>).slice(0, 6) : ['value'])
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

// ── line/bar charts ───────────────────────────────────────────────────────────
//
// Charts are rendered with a hand-rolled SVG so we don't drag in a chart lib
// for stage 4. The widget options.seriesPath / x / y describe how to extract
// the series from the resolved data.

interface ChartPoint { x: string; y: number }

function extractSeries(widget: DashboardWidget, data: unknown): ChartPoint[] {
  const seriesPath = (widget.options?.seriesPath as string) || ''
  const xKey = (widget.options?.x as string) || 'date'
  const yKey = (widget.options?.y as string) || 'value'
  const arr = seriesPath ? valueAtPath(data, seriesPath) : data
  if (!Array.isArray(arr)) return []
  const out: ChartPoint[] = []
  for (const item of arr) {
    if (item && typeof item === 'object') {
      const obj = item as Record<string, unknown>
      const y = Number(obj[yKey] ?? 0)
      if (!Number.isFinite(y)) continue
      out.push({ x: asString(obj[xKey] ?? ''), y })
    }
  }
  return out
}

export function LineChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const points = extractSeries(widget, data)
  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No series data</div>
  }
  const W = 320
  const H = 140
  const PAD = 16
  const maxY = Math.max(...points.map(p => p.y), 1)
  const stepX = points.length > 1 ? (W - PAD * 2) / (points.length - 1) : 0
  const path = points.map((p, i) => {
    const x = PAD + stepX * i
    const y = H - PAD - (p.y / maxY) * (H - PAD * 2)
    return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
  return (
    <div class="dashboard-widget-body dashboard-widget-chart">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" width="100%" height="100%">
        <line x1={PAD} y1={H - PAD} x2={W - PAD} y2={H - PAD} stroke="currentColor" stroke-width="0.5" opacity="0.3" />
        <path d={path} fill="none" stroke="var(--accent)" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round" />
        {points.map((p, i) => {
          const x = PAD + stepX * i
          const y = H - PAD - (p.y / maxY) * (H - PAD * 2)
          return <circle key={i} cx={x} cy={y} r="1.8" fill="var(--accent)" />
        })}
      </svg>
      <div class="dashboard-chart-axis">
        <span>{points[0]?.x}</span>
        <span>{points[points.length - 1]?.x}</span>
      </div>
    </div>
  )
}

export function BarChartWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const points = extractSeries(widget, data)
  if (points.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No series data</div>
  }
  const W = 320
  const H = 140
  const PAD = 16
  const maxY = Math.max(...points.map(p => p.y), 1)
  const barW = (W - PAD * 2) / points.length
  return (
    <div class="dashboard-widget-body dashboard-widget-chart">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" width="100%" height="100%">
        <line x1={PAD} y1={H - PAD} x2={W - PAD} y2={H - PAD} stroke="currentColor" stroke-width="0.5" opacity="0.3" />
        {points.map((p, i) => {
          const h = (p.y / maxY) * (H - PAD * 2)
          const x = PAD + barW * i + 1
          const y = H - PAD - h
          return <rect key={i} x={x} y={y} width={Math.max(barW - 2, 1)} height={h} fill="var(--accent)" opacity="0.85" />
        })}
      </svg>
    </div>
  )
}

// ── markdown ──────────────────────────────────────────────────────────────────
//
// Two modes:
//   1. options.text — static markdown embedded by the dashboard author.
//   2. resolved data — when a runtime/skill source is set, we render the data
//      as JSON inside a fenced code block. Useful for "show me /status".

export function MarkdownWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const text = (widget.options?.text as string) || ''
  let body = text
  if (!body && data !== undefined) {
    try {
      body = '```json\n' + JSON.stringify(data, null, 2) + '\n```'
    } catch {
      body = String(data)
    }
  }
  if (!body) {
    return <div class="dashboard-widget-body dashboard-empty">Nothing to show</div>
  }
  const html = DOMPurify.sanitize(marked.parse(body) as string, {
    ALLOWED_TAGS: ['p', 'br', 'strong', 'b', 'em', 'i', 'code', 'pre', 'a',
      'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'table', 'thead', 'tbody',
      'tr', 'th', 'td', 'blockquote', 'hr', 'span'],
    ADD_ATTR: ['target', 'rel', 'class'],
  })
  return (
    <div
      class="dashboard-widget-body dashboard-widget-markdown"
      // eslint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

// ── list ──────────────────────────────────────────────────────────────────────

export function ListWidget({ widget, data }: { widget: DashboardWidget; data: unknown }): JSX.Element {
  const itemsPath = (widget.options?.itemsPath as string) || ''
  const labelKey = (widget.options?.labelKey as string) || ''
  const items = asArray(itemsPath ? valueAtPath(data, itemsPath) : data)
  if (items.length === 0) {
    return <div class="dashboard-widget-body dashboard-empty">No items</div>
  }
  const limit = (widget.options?.limit as number) || 50
  return (
    <div class="dashboard-widget-body dashboard-widget-list">
      <ul>
        {items.slice(0, limit).map((item, i) => {
          let label: string
          if (item && typeof item === 'object' && labelKey) {
            label = asString((item as Record<string, unknown>)[labelKey])
          } else if (item && typeof item === 'object') {
            label = asString((item as Record<string, unknown>).title ?? (item as Record<string, unknown>).name ?? JSON.stringify(item))
          } else {
            label = asString(item)
          }
          return <li key={i}>{label}</li>
        })}
      </ul>
    </div>
  )
}

// ── custom html ───────────────────────────────────────────────────────────────
//
// AI-authored HTML/CSS/JS rendered inside a sandboxed iframe.
//
// Two layers of containment:
//   1. <iframe sandbox="allow-scripts">  →  unique opaque origin, can't read
//      parent DOM/cookies/localStorage; can't navigate top; can't submit forms.
//   2. CSP meta tag inside the srcdoc  →  default-src 'none' blocks ALL
//      outbound network. The iframe cannot fetch URLs, load remote scripts,
//      or even pull external fonts.
//
// Data flow:
//   parent → iframe (one-way):  postMessage({ type: 'atlas:data', data })
//   iframe defines window.atlasRender = function(data) { ... } to consume.
//
// There is no iframe→parent message handling for data fetching. Anything the
// widget needs MUST come from its declared source (resolved by the runtime).
// This eliminates exfiltration vectors entirely.

function buildCustomSrcdoc(widget: DashboardWidget): string {
  const html = widget.html || '<div id="root"></div>'
  const css = widget.css || ''
  const js = widget.js || ''
  // Escape closing </script> tags inside user JS so they don't break the
  // wrapping <script> block. Inside an iframe-srcdoc this is the only escape
  // we need — the rest of the document is closed by srcDoc itself, not by us.
  const safeJS = js.replace(/<\/script>/gi, '<\\/script>')
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; font-src data:;">
<style>
html, body { margin: 0; padding: 0; height: 100%; box-sizing: border-box; }
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
  color: #e5e7eb;
  background: transparent;
  font-size: 13px;
  line-height: 1.45;
  padding: 10px 12px;
}
* { box-sizing: border-box; }
${css}
</style>
</head>
<body>
${html}
<script>
(function () {
  var atlasData = null;
  function tryRender() {
    if (typeof window.atlasRender === 'function') {
      try { window.atlasRender(atlasData); }
      catch (err) {
        document.body.innerHTML = '<pre style="color:#f87171;font-family:monospace">' +
          String(err && err.message || err) + '</pre>';
      }
    }
  }
  window.addEventListener('message', function (e) {
    if (e && e.data && e.data.type === 'atlas:data') {
      atlasData = e.data.data;
      tryRender();
    }
  });
  // Tell parent we're ready so it can push the initial data immediately.
  try { window.parent.postMessage({ type: 'atlas:ready' }, '*'); } catch (_) {}
})();
${safeJS}
</script>
</body>
</html>`
}

export function CustomHtmlWidget(
  { widget, data }: { widget: DashboardWidget; data: unknown }
): JSX.Element {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const srcdoc = useMemo(() => buildCustomSrcdoc(widget), [widget.html, widget.css, widget.js])
  const dataRef = useRef<unknown>(data)
  dataRef.current = data

  // Push data into the iframe whenever it (re)loads or the data changes.
  // The iframe also signals 'atlas:ready' on its own load — we listen for
  // that so the first push happens as soon as the iframe DOM is alive,
  // independent of whether the React effect or the load event fires first.
  useEffect(() => {
    const iframe = iframeRef.current
    if (!iframe) return undefined

    function send() {
      try {
        iframe?.contentWindow?.postMessage(
          { type: 'atlas:data', data: dataRef.current },
          '*'
        )
      } catch { /* iframe gone */ }
    }
    function onMessage(e: MessageEvent) {
      // Only honor messages from this iframe's contentWindow.
      if (e.source !== iframe?.contentWindow) return
      const payload = e.data as { type?: string } | undefined
      if (payload?.type === 'atlas:ready') send()
    }

    window.addEventListener('message', onMessage)
    iframe.addEventListener('load', send)
    // In case the iframe was already loaded by the time this effect ran.
    send()
    return () => {
      window.removeEventListener('message', onMessage)
      iframe.removeEventListener('load', send)
    }
  }, [srcdoc])

  // Re-push data on every change without remounting the iframe.
  useEffect(() => {
    const iframe = iframeRef.current
    try {
      iframe?.contentWindow?.postMessage({ type: 'atlas:data', data }, '*')
    } catch { /* ignore */ }
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
    case 'metric':     return <MetricWidget widget={widget} data={data} />
    case 'table':      return <TableWidget widget={widget} data={data} />
    case 'line_chart': return <LineChartWidget widget={widget} data={data} />
    case 'bar_chart':  return <BarChartWidget widget={widget} data={data} />
    case 'markdown':   return <MarkdownWidget widget={widget} data={data} />
    case 'list':       return <ListWidget widget={widget} data={data} />
    case 'custom_html': return <CustomHtmlWidget widget={widget} data={data} />
    default:
      return <div class="dashboard-widget-body dashboard-empty">Unknown widget kind: {widget.kind}</div>
  }
}
