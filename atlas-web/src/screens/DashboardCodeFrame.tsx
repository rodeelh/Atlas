// DashboardCodeFrame — sandboxed iframe host for agent-authored TSX widgets.
//
// Flow:
//   1. The Go backend compiles the agent's TSX via esbuild and stores the
//      JS output on Widget.code.compiled. Imports of `preact`,
//      `preact/hooks`, and `@atlas/ui` are marked external — the iframe
//      resolves them via an importmap.
//   2. This component assembles an srcdoc document with:
//        - a CSP meta tag restricting origin to esm.sh + data: URLs,
//        - an importmap mapping preact* to esm.sh and @atlas/ui to an
//          inline data: URL of the stdlib (AU_STDLIB below),
//        - a boot script that dynamic-imports the compiled widget and
//          renders <Widget data={...} /> into #root.
//   3. The parent forwards every new WidgetData payload over postMessage.
//
// Sandbox model: sandbox="allow-scripts" (no allow-same-origin, no top nav,
// no forms). Widgets cannot read cookies, storage, or the parent DOM;
// connect-src 'none' in CSP blocks fetch/XHR/WebSocket even if a widget
// somehow bypassed the lexical forbidden-token check in the backend.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import type { JSX } from 'preact'

export interface DashboardWidgetAction {
  action: 'refresh-source' | 'open-drilldown' | 'set-filter' | 'navigate-dashboard'
  source?: string
  dashboardId?: string
  filterKey?: string
  value?: unknown
  title?: string
  data?: unknown
}

// ── @atlas/ui stdlib ──────────────────────────────────────────────────────────
// Kept inline so the iframe has no network dependency on us; served to the
// widget module through the importmap below. Components are intentionally
// minimal — enough primitives for an agent to compose a widget in TSX.
const AU_STDLIB = `import { h } from 'preact'

function _post(action) {
  try { window.parent.postMessage({ type: 'atlas:action', action }, '*') } catch (_) {}
}

function _fmt(v, format) {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'number') {
    if (format === 'currency') return '$' + v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    if (format === 'percent') return (v * 100).toFixed(1) + '%'
    if (format === 'integer') return Math.round(v).toLocaleString()
    if (format === 'compact') return Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(v)
    return v.toLocaleString()
  }
  return String(v)
}

export const Card = ({ children, title, subtitle }) =>
  h('div', { class: 'au-card' }, [
    (title || subtitle) ? h('div', { class: 'au-card-head', key: 'h' }, [
      title ? h('h4', { class: 'au-card-title', key: 't' }, title) : null,
      subtitle ? h('div', { class: 'au-card-sub', key: 's' }, subtitle) : null,
    ]) : null,
    h('div', { class: 'au-card-body', key: 'b' }, children),
  ])

export const Metric = ({ value, label, format, prefix, suffix, accent }) =>
  h('div', { class: 'au-metric' }, [
    h('div', { class: 'au-metric-value' + (accent ? ' au-accent' : ''), key: 'v' },
      (prefix || '') + _fmt(value, format) + (suffix || '')),
    label ? h('div', { class: 'au-metric-label', key: 'l' }, label) : null,
  ])

export const Row = ({ children, gap = 12, align = 'flex-start', wrap }) =>
  h('div', {
    style: 'display:flex;flex-direction:row;gap:' + gap + 'px;align-items:' + align + (wrap ? ';flex-wrap:wrap' : ''),
  }, children)

export const Col = ({ children, gap = 8, align = 'stretch' }) =>
  h('div', {
    style: 'display:flex;flex-direction:column;gap:' + gap + 'px;align-items:' + align,
  }, children)

export const Text = ({ children, size, muted, mono, weight, color }) =>
  h('span', {
    class: 'au-text' + (muted ? ' au-muted' : '') + (mono ? ' au-mono' : ''),
    style: [
      size ? 'font-size:' + size : '',
      weight ? 'font-weight:' + weight : '',
      color ? 'color:' + color : '',
    ].filter(Boolean).join(';'),
  }, children)

export const Badge = ({ children, tone }) =>
  h('span', { class: 'au-badge au-badge-' + (tone || 'neutral') }, children)

export const List = ({ items, render }) =>
  h('ul', { class: 'au-list' }, (Array.isArray(items) ? items : []).map((item, i) =>
    h('li', { class: 'au-list-item', key: i }, render ? render(item, i) : _fmt(item))
  ))

export const Table = ({ columns, rows, compact }) => {
  const cols = Array.isArray(columns) ? columns : []
  const data = Array.isArray(rows) ? rows : []
  return h('table', { class: 'au-table' + (compact ? ' au-table-compact' : '') }, [
    h('thead', { key: 'h' }, h('tr', null, cols.map((c, i) =>
      h('th', { key: c.key || i }, c.label || c.key || String(c))
    ))),
    h('tbody', { key: 'b' }, data.map((row, ri) =>
      h('tr', { key: ri }, cols.map((c, ci) => {
        const key = c.key || c
        const v = row && typeof row === 'object' ? row[key] : row
        return h('td', { key: ci }, c.format ? c.format(v, row) : _fmt(v, c.fmt))
      }))
    )),
  ])
}

export const Progress = ({ value, max = 100, tone }) => {
  const pct = Math.max(0, Math.min(100, (Number(value) / Number(max || 1)) * 100))
  return h('div', { class: 'au-progress' },
    h('div', { class: 'au-progress-fill au-progress-' + (tone || 'accent'), style: 'width:' + pct.toFixed(1) + '%' }))
}

export const Spark = ({ values, color, height = 28, filled }) => {
  const pts = (Array.isArray(values) ? values : []).map(Number).filter(Number.isFinite)
  if (pts.length < 2) return h('div', { class: 'au-spark au-spark-empty' })
  const min = Math.min.apply(null, pts), max = Math.max.apply(null, pts)
  const w = 100, span = max - min || 1
  const coords = pts.map((v, i) => {
    const x = (i / (pts.length - 1)) * w
    const y = height - ((v - min) / span) * height
    return x.toFixed(2) + ',' + y.toFixed(2)
  })
  const d = 'M' + coords.join(' L')
  const area = filled ? (d + ' L' + w + ',' + height + ' L0,' + height + ' Z') : null
  return h('svg', { class: 'au-spark', viewBox: '0 0 ' + w + ' ' + height, preserveAspectRatio: 'none', width: '100%', height },
    [
      area ? h('path', { d: area, fill: color || 'currentColor', 'fill-opacity': 0.15, key: 'a' }) : null,
      h('path', { d, fill: 'none', stroke: color || 'currentColor', 'stroke-width': 1.5, key: 'l' }),
    ])
}

export const Empty = ({ children }) => h('div', { class: 'au-empty' }, children || 'No data')

export const Error = ({ children }) => h('div', { class: 'au-error' }, children || 'Something went wrong')

export const Button = ({ children, action, onClick, tone, disabled }) =>
  h('button', {
    class: 'au-button au-button-' + (tone || 'neutral'),
    disabled: !!disabled,
    onClick: (e) => {
      onClick && onClick(e)
      if (action) _post(action)
    }
  }, children)

export const Tabs = ({ options, value, filterKey = 'tab' }) =>
  h('div', { class: 'au-tabs' }, (Array.isArray(options) ? options : []).map((option, i) => {
    const item = typeof option === 'object' ? option : { label: String(option), value: option }
    const active = item.value === value
    return h('button', {
      key: i,
      class: 'au-tab' + (active ? ' is-active' : ''),
      onClick: () => _post({ action: 'set-filter', filterKey, value: item.value, title: item.label }),
    }, item.label)
  }))

export const Select = ({ options, value, filterKey = 'select', placeholder = 'Select…' }) =>
  h('select', {
    class: 'au-select',
    value: value ?? '',
    onChange: (e) => _post({ action: 'set-filter', filterKey, value: e.currentTarget.value }),
  }, [
    h('option', { value: '', key: '__placeholder' }, placeholder),
    ...(Array.isArray(options) ? options : []).map((option, i) => {
      const item = typeof option === 'object' ? option : { label: String(option), value: option }
      return h('option', { key: i, value: item.value }, item.label)
    }),
  ])

export const Details = ({ summary, children, title, data, source }) =>
  h('details', {
    class: 'au-details',
    onToggle: (e) => {
      if (e.currentTarget.open) _post({ action: 'open-drilldown', title: title || summary, data, source })
    }
  }, [
    h('summary', { key: 's' }, summary || 'Details'),
    h('div', { class: 'au-details-body', key: 'b' }, children),
  ])

export const TimeRangePicker = ({ value = '7d', options = ['24h', '7d', '30d', '90d'], filterKey = 'timeRange' }) =>
  h('div', { class: 'au-tabs au-time-range' }, (Array.isArray(options) ? options : []).map((option, i) =>
    h('button', {
      key: i,
      class: 'au-tab' + (option === value ? ' is-active' : ''),
      onClick: () => _post({ action: 'set-filter', filterKey, value: option, title: String(option) }),
    }, String(option))
  ))

export const actions = {
  refreshSource: (source) => _post({ action: 'refresh-source', source }),
  openDrilldown: (payload) => _post({ action: 'open-drilldown', ...payload }),
  setFilter: (filterKey, value, title) => _post({ action: 'set-filter', filterKey, value, title }),
  navigateDashboard: (dashboardId) => _post({ action: 'navigate-dashboard', dashboardId }),
}
`

// Pinned Preact version. Bumping this string is the only place to change
// the Preact runtime shipped to widgets.
const PREACT_VERSION = '10.24.3'
const PREACT_BASE = `https://esm.sh/preact@${PREACT_VERSION}`

const IFRAME_CSS = `
  html,body{margin:0;padding:0;min-height:100%;box-sizing:border-box;}
  body{
    font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;
    font-size:13px;line-height:1.5;color:#e5e7eb;background:transparent;
    padding:12px;
  }
  *{box-sizing:border-box;}
  .au-card{display:flex;flex-direction:column;gap:8px;}
  .au-card-head{display:flex;flex-direction:column;gap:2px;}
  .au-card-title{margin:0;font-size:13px;font-weight:600;color:#f9fafb;letter-spacing:-0.01em;}
  .au-card-sub{font-size:11px;color:#9ca3af;}
  .au-card-body{display:flex;flex-direction:column;gap:8px;}
  .au-metric{display:flex;flex-direction:column;gap:2px;}
  .au-metric-value{font-size:1.8rem;font-weight:600;font-feature-settings:"tnum";color:#f9fafb;letter-spacing:-0.015em;}
  .au-metric-value.au-accent{color:#a5b4fc;}
  .au-metric-label{font-size:11px;color:#9ca3af;text-transform:uppercase;letter-spacing:0.04em;}
  .au-text.au-muted{color:#9ca3af;}
  .au-text.au-mono{font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,monospace;}
  .au-badge{display:inline-flex;align-items:center;gap:4px;padding:2px 8px;border-radius:999px;font-size:11px;font-weight:500;}
  .au-badge-neutral{background:rgba(148,163,184,0.15);color:#cbd5e1;}
  .au-badge-accent{background:rgba(99,102,241,0.18);color:#c7d2fe;}
  .au-badge-ok{background:rgba(34,197,94,0.15);color:#86efac;}
  .au-badge-warn{background:rgba(250,204,21,0.15);color:#fde68a;}
  .au-badge-err{background:rgba(239,68,68,0.15);color:#fca5a5;}
  .au-list{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:4px;}
  .au-list-item{padding:6px 0;border-bottom:1px solid rgba(148,163,184,0.12);font-size:12px;}
  .au-list-item:last-child{border-bottom:none;}
  .au-table{width:100%;border-collapse:collapse;font-size:11.5px;}
  .au-table th,.au-table td{padding:6px 8px;text-align:left;border-bottom:1px solid rgba(148,163,184,0.12);}
  .au-table th{font-weight:600;color:#9ca3af;text-transform:uppercase;letter-spacing:0.04em;font-size:10px;}
  .au-table-compact th,.au-table-compact td{padding:3px 6px;}
  .au-table tr:last-child td{border-bottom:none;}
  .au-progress{width:100%;height:6px;background:rgba(148,163,184,0.15);border-radius:999px;overflow:hidden;}
  .au-progress-fill{height:100%;border-radius:inherit;transition:width .3s ease;}
  .au-progress-accent{background:#6366f1;}
  .au-progress-ok{background:#22c55e;}
  .au-progress-warn{background:#eab308;}
  .au-progress-err{background:#ef4444;}
  .au-spark{display:block;color:#6366f1;}
  .au-spark-empty{height:28px;}
  .au-empty{color:#6b7280;font-size:12px;padding:8px 0;}
  .au-error{color:#fca5a5;font-size:12px;padding:8px 0;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,monospace;white-space:pre-wrap;}
  .au-button{appearance:none;border:1px solid rgba(148,163,184,0.18);background:rgba(148,163,184,0.08);color:#f9fafb;border-radius:8px;padding:7px 10px;font-size:12px;font-weight:600;cursor:pointer;}
  .au-button-accent{background:rgba(99,102,241,0.18);border-color:rgba(99,102,241,0.3);color:#c7d2fe;}
  .au-button-ok{background:rgba(34,197,94,0.16);border-color:rgba(34,197,94,0.28);color:#86efac;}
  .au-button:disabled{opacity:.45;cursor:default;}
  .au-tabs{display:flex;flex-wrap:wrap;gap:6px;}
  .au-tab{appearance:none;border:1px solid rgba(148,163,184,0.16);background:rgba(148,163,184,0.08);color:#cbd5e1;border-radius:999px;padding:5px 9px;font-size:11px;cursor:pointer;}
  .au-tab.is-active{background:rgba(99,102,241,0.18);border-color:rgba(99,102,241,0.3);color:#c7d2fe;}
  .au-select{width:100%;padding:7px 9px;border-radius:8px;border:1px solid rgba(148,163,184,0.16);background:rgba(17,24,39,0.9);color:#f9fafb;}
  .au-details{border:1px solid rgba(148,163,184,0.12);border-radius:10px;padding:8px 10px;background:rgba(148,163,184,0.05);}
  .au-details summary{cursor:pointer;font-weight:600;}
  .au-details-body{padding-top:8px;}
`

function toDataURL(js: string): string {
  // Escape </script> — not strictly required for data URLs but keeps the
  // srcdoc HTML parser happy if a widget ever emits that token as a string.
  const safe = js.replace(/<\/script>/gi, '<\\/script>')
  return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(safe)
}

function buildSrcdoc(compiled: string): string {
  const stdlibURL = toDataURL(AU_STDLIB)
  const widgetURL = toDataURL(compiled)
  // esm.sh returns ESM modules; the importmap wires the bare specifiers
  // used by the compiled TSX to their real URLs. CSP blocks every other
  // origin and every network side-channel.
  const importMap = {
    imports: {
      'preact': PREACT_BASE,
      'preact/hooks': `${PREACT_BASE}/hooks`,
      'preact/jsx-runtime': `${PREACT_BASE}/jsx-runtime`,
      '@atlas/ui': stdlibURL,
    },
  }
  const boot = `
import { h, render } from 'preact'

let currentData = undefined
let WidgetComponent = null

function errorDisplay(err) {
  const root = document.getElementById('root')
  if (!root) return
  root.innerHTML = ''
  const pre = document.createElement('pre')
  pre.className = 'au-error'
  pre.textContent = String((err && err.message) || err)
  root.appendChild(pre)
  try { window.parent.postMessage({ type: 'atlas:error', message: String((err && err.message) || err) }, '*') } catch (_) {}
}

let painted = false
function paint() {
  if (!WidgetComponent) return
  try {
    render(h(WidgetComponent, { data: currentData }), document.getElementById('root'))
    if (!painted) { painted = true; try { window.parent.postMessage({ type: 'atlas:paint' }, '*') } catch (_) {} }
  } catch (err) { errorDisplay(err) }
}

async function load() {
  try {
    const mod = await import(${JSON.stringify(widgetURL)})
    WidgetComponent = mod.default
    if (typeof WidgetComponent !== 'function') throw new Error('widget must export default a component')
    paint()
  } catch (err) { errorDisplay(err) }
}

window.addEventListener('message', (e) => {
  if (!e.data || typeof e.data !== 'object') return
  if (e.data.type === 'atlas:data') {
    currentData = e.data.data
    paint()
  }
})

window.addEventListener('error', (e) => { errorDisplay(e.error || e.message) })
window.addEventListener('unhandledrejection', (e) => { errorDisplay(e.reason) })

try { window.parent.postMessage({ type: 'atlas:ready' }, '*') } catch (_) {}
load()
`.trim()

  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline' https://esm.sh data:; script-src-elem 'unsafe-inline' https://esm.sh data:; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none';">
<style>${IFRAME_CSS}</style>
<script type="importmap">${JSON.stringify(importMap)}</script>
</head>
<body>
<div id="root"></div>
<script type="module">${boot}</script>
</body>
</html>`
}

// ── component ────────────────────────────────────────────────────────────────

export interface DashboardCodeFrameProps {
  /** sha256(tsx) — forces a remount when the compiled module changes. */
  hash: string
  /** esbuild-produced ESM module text (agent-authored Widget, default export). */
  compiled: string
  /** Most recent data payload to pass as the Widget's `data` prop. */
  data: unknown
  onAction?: (action: DashboardWidgetAction) => void
}

export function DashboardCodeFrame({ hash, compiled, data, onAction }: DashboardCodeFrameProps): JSX.Element {
  const iframeRef    = useRef<HTMLIFrameElement>(null)
  const dataRef      = useRef<unknown>(data)
  dataRef.current    = data
  const [widgetReady, setWidgetReady] = useState(false)
  const [widgetError, setWidgetError] = useState<string | null>(null)
  const [reloadKey,   setReloadKey]   = useState(0)

  // The srcdoc only depends on the compiled module — when TSX changes, the
  // parent will pass a new hash + compiled; useMemo keyed on hash keeps this
  // stable across data-only updates.
  const srcdoc = useMemo(() => buildSrcdoc(compiled), [hash, compiled, reloadKey])

  // Reset ready/error state whenever srcdoc changes.
  useEffect(() => {
    setWidgetReady(false)
    setWidgetError(null)
  }, [srcdoc])

  useEffect(() => {
    const iframe = iframeRef.current
    if (!iframe) return undefined
    function send() {
      try { iframe!.contentWindow?.postMessage({ type: 'atlas:data', data: dataRef.current }, '*') }
      catch { /* iframe gone */ }
    }
    function onMessage(e: MessageEvent) {
      if (e.source !== iframe!.contentWindow) return
      const payload = e.data as { type?: string; message?: string; action?: DashboardWidgetAction } | undefined
      if (payload?.type === 'atlas:ready') send()
      if (payload?.type === 'atlas:paint') setWidgetReady(true)
      if (payload?.type === 'atlas:error') setWidgetError(payload.message ?? 'Widget error')
      if (payload?.type === 'atlas:action' && payload.action && onAction) onAction(payload.action)
    }
    window.addEventListener('message', onMessage)
    iframe.addEventListener('load', send)
    send()
    return () => {
      window.removeEventListener('message', onMessage)
      iframe.removeEventListener('load', send)
    }
  }, [onAction, srcdoc])

  useEffect(() => {
    try { iframeRef.current?.contentWindow?.postMessage({ type: 'atlas:data', data }, '*') }
    catch { /* noop */ }
  }, [data])

  function handleRetry() {
    setWidgetError(null)
    setWidgetReady(false)
    setReloadKey(k => k + 1)
  }

  return (
    <div class="dashboard-widget-code-frame-wrap" style="position:relative;width:100%;height:100%;">
      {!widgetReady && !widgetError && (
        <div class="empty-state" style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center;pointer-events:none;">
          Loading widget…
        </div>
      )}
      {widgetError && (
        <div class="dashboard-widget-code-error" style="padding:8px;">
          <pre class="dashboard-widget-error" style="margin:0 0 8px 0;">{widgetError}</pre>
          <button class="btn btn-sm" onClick={handleRetry}>Retry</button>
        </div>
      )}
      <iframe
        key={reloadKey}
        ref={iframeRef}
        class="dashboard-widget-code-frame"
        style={widgetError ? 'display:none' : undefined}
        sandbox="allow-scripts"
        srcDoc={srcdoc}
        title="widget"
      />
    </div>
  )
}
