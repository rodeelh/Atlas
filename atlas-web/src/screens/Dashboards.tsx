// Dashboards screen (v2) — list saved dashboards and view widgets live.
//
// Authoring is agent-driven through the dashboard.* skills in chat; this
// screen is a viewer. Widgets render in a 12-column grid at positions the
// backend packer committed. Data is driven by per-dashboard SSE: the
// coordinator replays the latest event for every source on subscribe and
// pushes an event each time a source refreshes. Widgets bind to a source
// by name via `widget.bindings[0].source`.

import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'
import type { JSX } from 'preact'
import { GridStack, type GridStackWidget } from 'gridstack'
import 'gridstack/dist/gridstack.min.css'
import {
  api,
  type DashboardDataSourceBinding,
  type DashboardDefinition,
  type DashboardLayoutUpdate,
  type DashboardPreset,
  type DashboardRefreshEvent,
  type DashboardSize,
  type DashboardStatus,
  type DashboardSummary,
  type DashboardWidget,
  type DashboardWidgetData,
  type DashboardWidgetUpdate,
} from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { EmptyState } from '../components/EmptyState'
import { WidgetRenderer } from './DashboardWidgets'
import { ConfirmDialog } from '../components/ConfirmDialog'
import type { DashboardWidgetAction } from './DashboardCodeFrame'

const SOURCE_TIMEOUT_MS = 6000

const CODE_WIDGET_TEMPLATE = `import { Card, Metric, Row, Text } from '@atlas/ui'

export default function Widget({ data }) {
  return (
    <Card title="Code widget" subtitle="Draft authoring">
      <Row gap={12} align="center" wrap>
        <Metric value={data?.activeConversationCount ?? data?.count ?? 0} label="Active conversations" format="integer" />
        <Text muted size="12px">Edit this widget in the dashboard inspector.</Text>
      </Row>
    </Card>
  )
}`

const CODE_WIDGET_SNIPPETS = [
  {
    key: 'metric',
    label: 'Metric',
    tsx: `import { Card, Metric, Text } from '@atlas/ui'

export default function Widget({ data }) {
  const value = data?.value ?? data?.count ?? 0
  const label = data?.label ?? 'Total'
  return (
    <Card>
      <Text muted>{label}</Text>
      <Metric value={value} />
    </Card>
  )
}`,
  },
  {
    key: 'tabs',
    label: 'Tabs',
    tsx: `import { Card, Tabs, Text } from '@atlas/ui'

export default function Widget({ data }) {
  const active = data?.range ?? '24h'
  return (
    <Card>
      <Tabs
        value={active}
        options={[
          { label: '24h', value: '24h' },
          { label: '7d', value: '7d' },
          { label: '30d', value: '30d' },
        ]}
      />
      <Text muted>Range: {active}</Text>
    </Card>
  )
}`,
  },
  {
    key: 'details',
    label: 'Details',
    tsx: `import { Card, Details, Text, Button, actions } from '@atlas/ui'

export default function Widget({ data }) {
  return (
    <Card>
      <Text muted>Payload preview</Text>
      <Details summary="Open details" title="Widget payload" data={data}>
        <Text mono size="12px">{JSON.stringify(data, null, 2)}</Text>
      </Details>
      <Button onClick={() => actions.openDrilldown({ title: 'Widget payload', data })}>
        Inspect payload
      </Button>
    </Card>
  )
}`,
  },
] as const

type DashboardClient = Pick<typeof api,
  'dashboard' |
  'dashboards' |
  'deleteDashboard' |
  'editDashboardDraft' |
  'commitDashboardDraft' |
  'refreshDashboard' |
  'refreshDashboardSource' |
  'resolveDashboardWidget' |
  'streamDashboardEvents' |
  'updateDashboardLayout' |
  'updateDashboardWidget'
>

type SourceHealth = 'loading' | 'ok' | 'stale' | 'error' | 'timeout'

const DashboardIcon = () => (
  <svg width="36" height="36" viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <rect x="5" y="5" width="11" height="11" rx="1.5" />
    <rect x="20" y="5" width="11" height="6" rx="1.5" />
    <rect x="20" y="15" width="11" height="16" rx="1.5" />
    <rect x="5" y="20" width="11" height="11" rx="1.5" />
  </svg>
)

function formatDate(iso?: string): string {
  if (!iso) return '—'
  try { return new Date(iso).toLocaleString() }
  catch { return iso }
}

// ── widget cell ───────────────────────────────────────────────────────────────

function inlineTextValue(ev: Event): string {
  return (ev.currentTarget as HTMLInputElement).value
}

interface WidgetCellProps {
  client: DashboardClient
  dashboardID: string
  widget: DashboardWidget
  /** Latest data for the source this widget is bound to (undefined if unbound). */
  sourceData: unknown
  sourceError?: string
  sourceAt?: string
  sourceHealth?: SourceHealth
  sourceKind?: string
  sourceDurationMs?: number
  sourceLastSuccessfulAt?: string
  sourceCacheAgeMs?: number
  canEdit?: boolean
  selected?: boolean
  layoutEditing?: boolean
  onSelect?: (widget: DashboardWidget) => void
  onEdit?: (widget: DashboardWidget) => void
  onInlineUpdate?: (widgetID: string, update: DashboardWidgetUpdate) => Promise<void>
  onAction?: (action: DashboardWidgetAction) => void
}

function WidgetCell({
  client,
  dashboardID,
  widget,
  sourceData,
  sourceError,
  sourceAt,
  sourceHealth,
  sourceKind,
  sourceDurationMs,
  sourceLastSuccessfulAt,
  sourceCacheAgeMs,
  canEdit,
  selected,
  layoutEditing,
  onSelect,
  onEdit,
  onInlineUpdate,
  onAction,
}: WidgetCellProps): JSX.Element {
  // If the widget has no binding we fall back to a one-shot resolve when
  // the cell mounts — this is the path for unbound widgets (e.g. static
  // markdown with inline `text` in options).
  const [fallback, setFallback] = useState<DashboardWidgetData | null>(null)
  const [fallbackErr, setFallbackErr] = useState<string | null>(null)
  const hasBinding = Array.isArray(widget.bindings) && widget.bindings.length > 0

  useEffect(() => {
    if (hasBinding) return
    let cancelled = false
    client.resolveDashboardWidget(dashboardID, widget.id)
      .then(r => { if (!cancelled) { setFallback(r); if (!r.success && r.error) setFallbackErr(r.error) } })
      .catch(e => { if (!cancelled) setFallbackErr(e instanceof Error ? e.message : 'Failed to load widget data.') })
    return () => { cancelled = true }
  }, [client, dashboardID, widget.id, hasBinding])

  const data  = hasBinding ? sourceData  : fallback?.data
  const error = hasBinding ? (sourceData === undefined ? sourceError : undefined) : (fallbackErr ?? (fallback && !fallback.success ? fallback.error : undefined))
  const at    = hasBinding ? sourceAt    : fallback?.resolvedAt
  const showLoading = hasBinding && !error && data === undefined && sourceHealth === 'loading'
  const showTimeout = hasBinding && !error && data === undefined && sourceHealth === 'timeout'

  const x = Math.max(0, widget.gridX ?? 0)
  const y = Math.max(0, widget.gridY ?? 0)
  const w = Math.max(1, Math.min(12, widget.gridW || 4))
  const h = Math.max(1, Math.min(12, widget.gridH || 3))
  const style: JSX.CSSProperties = {
    gridColumn: `${x + 1} / span ${w}`,
    gridRow:    `${y + 1} / span ${h}`,
  }
  const stackAttrs = layoutEditing
    ? {
        'gs-id': widget.id,
        'gs-x': String(x),
        'gs-y': String(y),
        'gs-w': String(w),
        'gs-h': String(h),
      }
    : {}

  // Size class lets CSS apply compact padding/font rules for narrow cards.
  const sizeClass = w <= 3 ? 'dw-size-quarter'
    : w <= 4 ? 'dw-size-third'
    : w <= 6 ? 'dw-size-half'
    : 'dw-size-full'

  // Charts need height:100% on the card so the canvas has a defined size.
  // All other presets use height:auto so the card shrinks to its content
  // (a 2-row table shouldn't be as tall as a 10-row table).
  const preset = widget.code?.preset || ''
  const isChart = preset === 'line_chart' || preset === 'area_chart' || preset === 'bar_chart' || preset === 'pie_chart'
  const denseHeader = preset === 'progress'
    || preset === 'gauge'
    || preset === 'status_grid'
    || preset === 'kpi_group'
    || preset === 'heatmap'
    || preset === 'timeline'
  const healthLabel = sourceHealth === 'loading'
    ? 'Loading'
    : sourceHealth === 'ok'
      ? 'OK'
      : sourceHealth === 'error'
        ? 'Failed'
        : sourceHealth === 'stale'
          ? 'Stale'
        : sourceHealth === 'timeout'
          ? 'Slow'
          : ''
  const showLiveMeta = !canEdit && !layoutEditing && !!(healthLabel || at)
  const provenance = [
    sourceKind ? `Kind: ${sourceKind}` : '',
    typeof sourceDurationMs === 'number' ? `Duration: ${sourceDurationMs}ms` : '',
    sourceLastSuccessfulAt ? `Last success: ${formatDate(sourceLastSuccessfulAt)}` : '',
    typeof sourceCacheAgeMs === 'number' ? `Cache age: ${Math.round(sourceCacheAgeMs / 1000)}s` : '',
    sourceError && sourceData !== undefined ? `Latest error: ${sourceError}` : '',
  ].filter(Boolean).join('\n')
  const canInlineEdit = !!canEdit && !!selected && !!onInlineUpdate

  return (
    <div
      class={`dw-cell ${sizeClass}${selected ? ' dw-cell-edit' : ''}${layoutEditing ? ' grid-stack-item dw-layout-item' : ''}`}
      style={layoutEditing ? undefined : style}
      onClick={() => {
        if (canEdit) onSelect?.(widget)
      }}
      {...stackAttrs}
    >
      <div class={`dashboard-widget-card${isChart ? ' dw-card-chart' : ''}${showLiveMeta ? ' dw-card-live-meta' : ''}${layoutEditing ? ' grid-stack-item-content dw-card-layout-editing' : ''}`}>
        {layoutEditing && (
          <div class="dw-layout-overlay">
            <span class="dw-layout-drag-handle" title="Move widget" aria-hidden="true">
              <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
                <circle cx="6" cy="6" r="1.2" />
                <circle cx="10" cy="6" r="1.2" />
                <circle cx="6" cy="10" r="1.2" />
                <circle cx="10" cy="10" r="1.2" />
              </svg>
            </span>
          </div>
        )}
        {(widget.title || widget.description || canEdit) && (
          <div class={`dashboard-widget-header${layoutEditing ? ' dashboard-widget-header-layout' : ''}${denseHeader ? ' dashboard-widget-header-dense' : ''}`}>
            <div class="dashboard-widget-header-left">
              {canInlineEdit ? (
                <input
                  key={`title-${widget.id}-${widget.title || ''}`}
                  class="dw-inline-title no-drag"
                  aria-label="Widget title"
                  defaultValue={widget.title || ''}
                  placeholder="Untitled widget"
                  onBlur={e => {
                    const next = inlineTextValue(e).trim()
                    if (next !== (widget.title || '')) void onInlineUpdate(widget.id, { title: next })
                  }}
                />
              ) : (
                <>
                  {widget.title && <h4>{widget.title}</h4>}
                  {widget.description && <span class="dashboard-widget-sub">{widget.description}</span>}
                </>
              )}
            </div>
            {canEdit && !layoutEditing && (
              <div class="dashboard-widget-meta">
                <button
                  class="dashboard-widget-edit"
                  type="button"
                  aria-label={`Edit ${widget.title || widget.id}`}
                  onClick={e => {
                    e.stopPropagation()
                    onEdit?.(widget)
                  }}
                >
                  Edit
                </button>
              </div>
            )}
          </div>
        )}
        <div class={`dashboard-widget-content${layoutEditing ? ' dw-no-interact' : ''}`}>
          {sourceError && sourceData !== undefined && (
            <div class="dashboard-widget-stale-note" title={sourceError}>
              Showing last good data. Latest refresh failed.
            </div>
          )}
          {showLoading
            ? <div class="dashboard-widget-body dashboard-empty">Loading source…</div>
            : showTimeout
              ? <div class="dashboard-widget-body dashboard-empty dashboard-empty-warning">Source is taking longer than expected.</div>
              : <WidgetRenderer widget={widget} data={data} error={error} onAction={onAction} />}
        </div>
        {showLiveMeta && (
          <div class="dashboard-widget-footer">
            {healthLabel && (
              <span class={`dashboard-widget-health dashboard-widget-status-dot ${sourceHealth}`} title={healthLabel} aria-label={healthLabel} />
            )}
            {at && (
              <span class="dashboard-widget-timestamp dashboard-widget-timestamp-footer" title={`Updated ${formatDate(at)}`}>
                {new Date(at).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

// ── detail view ───────────────────────────────────────────────────────────────

interface SourceEntry {
  data?: unknown
  error?: string
  at?: string
  health: SourceHealth
  requestedAt?: number
  sourceKind?: string
  durationMs?: number
  lastSuccessfulAt?: string
  cacheAgeMs?: number
}

interface DrilldownState {
  widgetTitle: string
  source?: string
  title: string
  data: unknown
}

function moveResizeHandlesIntoOverlay(root: HTMLElement) {
  root.querySelectorAll<HTMLElement>('.grid-stack-item').forEach(item => {
    const overlay = item.querySelector<HTMLElement>('.dw-layout-overlay')
    const handle = item.querySelector<HTMLElement>(':scope > .ui-resizable-se')
    if (!overlay || !handle || handle.parentElement === overlay) return
    overlay.appendChild(handle)
  })
}

function uniqueBoundSources(widgets: DashboardWidget[]): string[] {
  const seen = new Set<string>()
  for (const widget of widgets) {
    const name = widget.bindings?.[0]?.source
    if (name) seen.add(name)
  }
  return [...seen]
}

function seedSourceEntries(names: string[], previous: Record<string, SourceEntry>): Record<string, SourceEntry> {
  const requestedAt = Date.now()
  const next: Record<string, SourceEntry> = {}
  for (const name of names) {
    const existing = previous[name]
    next[name] = existing
      ? existing
      : { health: 'loading', requestedAt }
  }
  return next
}

function markSourcesLoading(previous: Record<string, SourceEntry>, names: string[]): Record<string, SourceEntry> {
  const requestedAt = Date.now()
  const next = { ...previous }
  for (const name of names) {
    const existing = previous[name]
    next[name] = {
      ...existing,
      health: 'loading',
      requestedAt,
    }
  }
  return next
}

function markTimedOut(previous: Record<string, SourceEntry>): Record<string, SourceEntry> {
  const now = Date.now()
  let changed = false
  const next: Record<string, SourceEntry> = {}
  for (const [name, entry] of Object.entries(previous)) {
    if (entry.health === 'loading' && entry.requestedAt && now-entry.requestedAt >= SOURCE_TIMEOUT_MS) {
      next[name] = { ...entry, health: 'timeout' }
      changed = true
    } else {
      next[name] = entry
    }
  }
  return changed ? next : previous
}

function applyRefreshPayload(previous: Record<string, SourceEntry>, payload: DashboardRefreshEvent): Record<string, SourceEntry> {
  const existing = previous[payload.source] ?? { requestedAt: Date.now(), health: 'loading' as SourceHealth }
  const hasFreshData = payload.data !== undefined && !payload.error
  const data = hasFreshData
    ? payload.data
    : payload.data !== undefined
      ? payload.data
      : existing.data
  const hasLastGood = data !== undefined
  const health: SourceHealth = payload.error
    ? (hasLastGood ? 'stale' : 'error')
    : 'ok'
  return {
    ...previous,
    [payload.source]: {
      ...existing,
      data,
      error: payload.error || undefined,
      at: payload.at,
      health,
      requestedAt: undefined,
      sourceKind: payload.sourceKind ?? existing.sourceKind,
      durationMs: payload.durationMs ?? existing.durationMs,
      lastSuccessfulAt: payload.lastSuccessfulAt ?? (hasFreshData ? (payload.resolvedAt ?? payload.at) : existing.lastSuccessfulAt),
      cacheAgeMs: payload.cacheAgeMs ?? existing.cacheAgeMs,
    },
  }
}

type BindingPathToken =
  | { kind: 'key'; key: string }
  | { kind: 'index'; index: number }
  | { kind: 'each' }

function parseBindingPath(path: string): BindingPathToken[] {
  const tokens: BindingPathToken[] = []
  for (let part of path.split('.')) {
    if (!part) continue
    while (part) {
      const bracket = part.indexOf('[')
      if (bracket === -1) {
        const index = Number(part)
        tokens.push(Number.isInteger(index) && String(index) === part
          ? { kind: 'index', index }
          : { kind: 'key', key: part })
        break
      }
      if (bracket > 0) tokens.push({ kind: 'key', key: part.slice(0, bracket) })
      const close = part.indexOf(']', bracket)
      if (close === -1) {
        tokens.push({ kind: 'key', key: part.slice(bracket) })
        break
      }
      const content = part.slice(bracket + 1, close)
      if (content === '') {
        tokens.push({ kind: 'each' })
      } else {
        const index = Number(content)
        tokens.push(Number.isInteger(index)
          ? { kind: 'index', index }
          : { kind: 'key', key: content })
      }
      part = part.slice(close + 1)
    }
  }
  return tokens
}

function valueAtBindingPath(data: unknown, path: string): unknown {
  if (!path) return data
  return evalBindingTokens(data, parseBindingPath(path))
}

function evalBindingTokens(data: unknown, tokens: BindingPathToken[]): unknown {
  let current = data
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]
    if (token.kind === 'each') {
      if (!Array.isArray(current)) return undefined
      const rest = tokens.slice(i + 1)
      return current.flatMap(item => {
        const value = evalBindingTokens(item, rest)
        return value === undefined ? [] : [value]
      })
    }
    if (token.kind === 'key') {
      if (!current || typeof current !== 'object' || !(token.key in current)) return undefined
      current = (current as Record<string, unknown>)[token.key]
    } else {
      if (!Array.isArray(current) || token.index < 0 || token.index >= current.length) return undefined
      current = current[token.index]
    }
  }
  return current
}

function projectSourceDataForWidget(widget: DashboardWidget, data: unknown): unknown {
  const path = widget.bindings?.[0]?.path
  if (!path || data === undefined) return data
  return valueAtBindingPath(data, path)
}

const DASHBOARD_PRESETS: DashboardPreset[] = ['metric', 'table', 'line_chart', 'area_chart', 'bar_chart', 'pie_chart', 'donut_chart', 'scatter_chart', 'stacked_chart', 'list', 'markdown', 'timeline', 'heatmap', 'progress', 'gauge', 'status_grid', 'kpi_group']
const DASHBOARD_SIZES: DashboardSize[] = ['quarter', 'third', 'half', 'tall', 'full']

function formatJSON(value: unknown): string {
  try { return JSON.stringify(value ?? {}, null, 2) }
  catch { return '{}' }
}

function parseOptionsJSON(text: string): Record<string, unknown> {
  const trimmed = text.trim()
  if (!trimmed) return {}
  const parsed = JSON.parse(trimmed)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('Options must be a JSON object.')
  }
  return parsed as Record<string, unknown>
}

function layoutFromGrid(grid: GridStack): DashboardLayoutUpdate {
  const saved = grid.save(false) as GridStackWidget[]
  return {
    widgets: saved
      .map(item => ({
        id: String(item.id ?? ''),
        gridX: Number(item.x ?? 0),
        gridY: Number(item.y ?? 0),
        gridW: Number(item.w ?? 1),
        gridH: Number(item.h ?? 1),
      }))
      .filter(item => item.id),
  }
}

interface WidgetInspectorProps {
  dashboard: DashboardDefinition
  widget: DashboardWidget | null
  saving: boolean
  error: string | null
  onSave: (widgetID: string, update: DashboardWidgetUpdate) => Promise<void>
  onClose: () => void
}

function WidgetInspector({ dashboard, widget, saving, error, onSave, onClose }: WidgetInspectorProps): JSX.Element {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [size, setSize] = useState<DashboardSize>('half')
  const [preset, setPreset] = useState<DashboardPreset>('metric')
  const [source, setSource] = useState('')
  const [bindingPath, setBindingPath] = useState('')
  const [optionsText, setOptionsText] = useState('{}')
  const [tsx, setTSX] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)

  useEffect(() => {
    setTitle(widget?.title ?? '')
    setDescription(widget?.description ?? '')
    setSize(widget?.size ?? 'half')
    setPreset((widget?.code?.preset as DashboardPreset | undefined) ?? 'metric')
    setSource(widget?.bindings?.[0]?.source ?? '')
    setBindingPath(widget?.bindings?.[0]?.path ?? '')
    setOptionsText(formatJSON(widget?.code?.options ?? {}))
    setTSX(widget?.code?.tsx ?? CODE_WIDGET_TEMPLATE)
    setLocalError(null)
  }, [widget?.id])

  if (!widget) {
    return (
      <aside class="dashboard-widget-inspector">
        <div class="dashboard-inspector-empty">Select a draft widget to customize it.</div>
      </aside>
    )
  }

  const isCode = widget.code?.mode === 'code'
  const canSave = !saving
  const savedTSX = widget.code?.tsx ?? CODE_WIDGET_TEMPLATE
  const codeDirty = isCode && tsx !== savedTSX
  const inspectorStatus = saving
    ? { tone: 'saving', label: 'Saving' }
    : localError
      ? { tone: 'error', label: 'Fix JSON' }
      : error
        ? { tone: 'error', label: 'Compile failed' }
        : codeDirty
          ? { tone: 'pending', label: 'Unsaved' }
          : widget.code?.hash
            ? { tone: 'ready', label: 'Compiled' }
            : { tone: 'idle', label: 'Ready' }

  async function submit(ev: Event) {
    ev.preventDefault()
    if (!widget) return
    setLocalError(null)
    let options: Record<string, unknown> | undefined
    try {
      options = isCode ? undefined : parseOptionsJSON(optionsText)
    } catch (e) {
      setLocalError(e instanceof Error ? e.message : 'Options JSON is invalid.')
      return
    }
    const bindings: DashboardDataSourceBinding[] = source ? [{ source, ...(bindingPath.trim() ? { path: bindingPath.trim() } : {}) }] : []
    await onSave(widget.id, {
      title,
      description,
      size,
      bindings,
      ...(isCode ? { tsx } : { preset, options }),
    })
  }

  return (
    <aside class="dashboard-widget-inspector">
      <div class="dashboard-inspector-header">
        <div>
          <h3>Widget</h3>
          <span>{isCode ? 'Code widget metadata' : 'Preset widget settings'}</span>
        </div>
        <span class={`dashboard-inspector-status ${inspectorStatus.tone}`}>{inspectorStatus.label}</span>
        <button class="btn btn-sm" type="button" onClick={onClose}>Close</button>
      </div>
      <form class="dashboard-inspector-form" onSubmit={submit}>
        <label>
          <span>Title</span>
          <input aria-label="Title" value={title} onInput={e => setTitle((e.currentTarget as HTMLInputElement).value)} />
        </label>
        <label>
          <span>Description</span>
          <input aria-label="Description" value={description} onInput={e => setDescription((e.currentTarget as HTMLInputElement).value)} />
        </label>
        <label>
          <span>Size</span>
          <select aria-label="Size" value={size} onChange={e => setSize((e.currentTarget as HTMLSelectElement).value as DashboardSize)}>
            {DASHBOARD_SIZES.map(value => <option key={value} value={value}>{value}</option>)}
          </select>
        </label>
        <label>
          <span>Source</span>
          <select aria-label="Source" value={source} onChange={e => setSource((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">No source</option>
            {dashboard.sources.map(src => <option key={src.name} value={src.name}>{src.name}</option>)}
          </select>
        </label>
        <label>
          <span>Binding path</span>
          <input
            aria-label="Binding path"
            value={bindingPath}
            placeholder="rows, rows[0], rows[].value"
            onInput={e => setBindingPath((e.currentTarget as HTMLInputElement).value)}
          />
        </label>
        <label>
          <span>Preset</span>
          <select
            aria-label="Preset"
            value={preset}
            disabled={isCode}
            onChange={e => setPreset((e.currentTarget as HTMLSelectElement).value as DashboardPreset)}
          >
            {DASHBOARD_PRESETS.map(value => <option key={value} value={value}>{value}</option>)}
          </select>
        </label>
        <label>
          <span>Options JSON</span>
          <textarea
            aria-label="Options JSON"
            value={isCode ? 'Code widget options are edited in TSX.' : optionsText}
            disabled={isCode}
            rows={10}
            spellcheck={false}
            onInput={e => setOptionsText((e.currentTarget as HTMLTextAreaElement).value)}
          />
        </label>
        {isCode && (
          <div class="dashboard-code-editor">
            <label>
              <span>Widget TSX</span>
              <textarea
                aria-label="Widget TSX"
                value={tsx}
                rows={16}
                spellcheck={false}
                onInput={e => setTSX((e.currentTarget as HTMLTextAreaElement).value)}
              />
            </label>
            <div class="dashboard-code-toolbar" aria-label="Code widget examples">
              <button class="btn btn-sm" type="button" onClick={() => setTSX(CODE_WIDGET_TEMPLATE)}>Use template</button>
              {CODE_WIDGET_SNIPPETS.map(snippet => (
                <button
                  key={snippet.key}
                  class="btn btn-sm"
                  type="button"
                  onClick={() => setTSX(snippet.tsx)}
                >
                  {snippet.label}
                </button>
              ))}
            </div>
            <div class="dashboard-code-meta-grid">
              <p class="dashboard-code-meta">
                Saved compile: {widget.code?.hash ? <code>{widget.code.hash.slice(0, 12)}</code> : 'Not compiled yet'}
              </p>
              <p class="dashboard-code-meta">
                Draft state: {codeDirty ? 'Unsaved changes' : 'In sync'}
              </p>
            </div>
          </div>
        )}
        {(localError || error) && (
          <div class="dashboard-inspector-diagnostics" role="alert">
            <p class="dashboard-inspector-error-label">{localError ? 'Validation' : 'Compile diagnostics'}</p>
            <pre class="dashboard-inspector-error">{localError || error}</pre>
          </div>
        )}
        <div class="dashboard-inspector-actions">
          <button class="btn btn-sm btn-primary" type="submit" disabled={!canSave}>{saving ? 'Saving…' : 'Save widget'}</button>
        </div>
      </form>
    </aside>
  )
}

export function DashboardDetail(
  {
    id,
    onBack,
    onDelete,
    onNavigate,
    onChanged,
    initialLayoutEditing,
    client = api,
  }: {
    id: string
    onBack: () => void
    onDelete: (id: string) => void
    onNavigate: (id: string, options?: { layoutEditing?: boolean }) => void
    onChanged: () => void
    initialLayoutEditing?: boolean
    client?: DashboardClient
  }
): JSX.Element {
  const [def, setDef]           = useState<DashboardDefinition | null>(null)
  const [loading, setLoading]   = useState(true)
  const [error, setError]       = useState<string | null>(null)
  const [loadError, setLoadError] = useState(false)
  const [sources, setSources]   = useState<Record<string, SourceEntry>>({})
  const [refreshing, setRefreshing] = useState(false)
  const [refreshError, setRefreshError] = useState(false)
  const [pendingDelete, setPendingDelete] = useState(false)
  const [editingWidgetID, setEditingWidgetID] = useState<string | null>(null)
  const [savingWidget, setSavingWidget] = useState(false)
  const [widgetSaveError, setWidgetSaveError] = useState<string | null>(null)
  const [layoutEditing, setLayoutEditing] = useState(false)
  const [savingLayout, setSavingLayout] = useState(false)
  const [layoutError, setLayoutError] = useState<string | null>(null)
  const [layoutDirty, setLayoutDirty] = useState(false)
  const [drafting, setDrafting] = useState(false)
  const [publishing, setPublishing] = useState(false)
  const [sourceFilter, setSourceFilter] = useState<string | null>(null)
  const [interactionContext, setInteractionContext] = useState<Record<string, unknown>>({})
  const [drilldown, setDrilldown] = useState<DrilldownState | null>(null)
  const [selectedWidgetID, setSelectedWidgetID] = useState<string | null>(null)
  const gridElRef = useRef<HTMLDivElement | null>(null)
  const gridRef = useRef<GridStack | null>(null)
  const layoutSaveTimerRef = useRef<number | null>(null)
  const latestDefRef = useRef<DashboardDefinition | null>(null)
  const esRef = useRef<EventSource | null>(null)

  // Load the dashboard definition. The SSE stream replays latest source
  // events on subscribe, so we don't need a separate initial data fetch.
  useEffect(() => {
    let cancelled = false
    setLoading(true); setError(null); setLoadError(false)
    client.dashboard(id)
      .then(d => { if (!cancelled) { setDef(d); setLoadError(false) } })
      .catch(e => { if (!cancelled) { setError(e instanceof Error ? e.message : 'Failed to load dashboard.'); setLoadError(true) } })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [client, id])

  const widgets = def?.widgets ?? []
  const boundSourceNames = useMemo(() => uniqueBoundSources(widgets), [widgets])
  const editingWidget = useMemo(
    () => widgets.find(w => w.id === editingWidgetID) ?? null,
    [widgets, editingWidgetID],
  )
  const visibleWidgets = useMemo(
    () => sourceFilter ? widgets.filter(w => w.bindings?.[0]?.source === sourceFilter) : widgets,
    [sourceFilter, widgets],
  )
  const isDraft = def?.status === 'draft'

  useEffect(() => {
    latestDefRef.current = def
  }, [def])

  useEffect(() => {
    if (!isDraft) setEditingWidgetID(null)
  }, [isDraft])

  useEffect(() => {
    if (!isDraft) setSelectedWidgetID(null)
  }, [isDraft])

  useEffect(() => {
    if (!isDraft) setLayoutEditing(false)
  }, [isDraft])

  const flushLayoutSave = useCallback(async () => {
    const active = gridRef.current
    const currentDef = latestDefRef.current
    if (!active || !currentDef || !layoutDirty) return true
    if (layoutSaveTimerRef.current !== null) {
      window.clearTimeout(layoutSaveTimerRef.current)
      layoutSaveTimerRef.current = null
    }
    setSavingLayout(true)
    setLayoutError(null)
    try {
      const updated = await client.updateDashboardLayout(currentDef.id, layoutFromGrid(active))
      setDef(updated)
      latestDefRef.current = updated
      setLayoutDirty(false)
      onChanged()
      return true
    } catch (e) {
      setLayoutError(e instanceof Error ? e.message : 'Failed to save layout.')
      return false
    } finally {
      setSavingLayout(false)
    }
  }, [client, layoutDirty, onChanged])

  useEffect(() => {
    if (isDraft && initialLayoutEditing) setLayoutEditing(true)
  }, [id, initialLayoutEditing, isDraft])

  useEffect(() => {
    if (!layoutEditing || !isDraft || !def || !gridElRef.current) return
    const grid = GridStack.init({
      column: Math.max(1, def.layout?.columns || 12),
      cellHeight: 100,
      margin: 10,
      float: true,
      animate: true,
      handle: '.dw-layout-drag-handle',
      draggable: { cancel: 'input,textarea,button,select,option,.dashboard-widget-edit' },
      resizable: { handles: 'se', autoHide: false },
    }, gridElRef.current)
    gridRef.current = grid
    moveResizeHandlesIntoOverlay(gridElRef.current)
    const rafID = window.requestAnimationFrame(() => {
      if (gridElRef.current) moveResizeHandlesIntoOverlay(gridElRef.current)
    })
    const scheduleSave = () => {
      if (layoutSaveTimerRef.current !== null) window.clearTimeout(layoutSaveTimerRef.current)
      layoutSaveTimerRef.current = window.setTimeout(async () => {
        await flushLayoutSave()
      }, 180)
    }
    const markDirty = () => {
      setLayoutDirty(true)
      scheduleSave()
    }
    grid.on('dragstop', markDirty)
    grid.on('resizestop', markDirty)
    return () => {
      if (layoutSaveTimerRef.current !== null) {
        window.clearTimeout(layoutSaveTimerRef.current)
        layoutSaveTimerRef.current = null
      }
      window.cancelAnimationFrame(rafID)
      grid.off('dragstop')
      grid.off('resizestop')
      grid.destroy(false)
      if (gridRef.current === grid) gridRef.current = null
    }
  }, [def?.id, flushLayoutSave, isDraft, layoutEditing])

  useEffect(() => {
    setSources(prev => seedSourceEntries(boundSourceNames, prev))
  }, [boundSourceNames])

  // Subscribe to the per-dashboard event stream. The coordinator synchronously
  // replays the latest cached event for every source when we subscribe, so the
  // grid paints on the first tick without a round-trip.
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => {
    let cancelled = false

    function connect() {
      if (cancelled) return
      const es = client.streamDashboardEvents(id)
      esRef.current = es
      es.onmessage = (ev: MessageEvent<string>) => {
        try {
          const payload = JSON.parse(ev.data) as DashboardRefreshEvent
          setSources(prev => applyRefreshPayload(prev, payload))
        } catch { /* ignore malformed frames */ }
      }
      es.onerror = () => {
        es.close()
        esRef.current = null
        if (!cancelled) {
          reconnectTimerRef.current = setTimeout(connect, 5000)
        }
      }
    }

    connect()
    return () => {
      cancelled = true
      if (reconnectTimerRef.current !== null) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
      esRef.current?.close()
      esRef.current = null
    }
  }, [client, id])

  useEffect(() => {
    const handle = window.setInterval(() => {
      setSources(prev => markTimedOut(prev))
    }, 250)
    return () => window.clearInterval(handle)
  }, [])

  async function handleRefresh() {
    setRefreshing(true)
    setRefreshError(false)
    setSources(prev => markSourcesLoading(prev, boundSourceNames))
    try { await client.refreshDashboard(id); setRefreshError(false) }
    catch (e) { setError(e instanceof Error ? e.message : 'Refresh failed.'); setRefreshError(true) }
    finally { setRefreshing(false) }
  }

  async function handleRefreshSource(source: string) {
    if (!def) return
    setSources(prev => markSourcesLoading(prev, [source]))
    try {
      const event = await client.refreshDashboardSource(def.id, source)
      setSources(prev => applyRefreshPayload(prev, event))
    } catch (e) {
      setError(e instanceof Error ? e.message : `Failed to refresh source ${source}.`)
    }
  }

  async function handleWidgetAction(widget: DashboardWidget, action: DashboardWidgetAction, sourceData: unknown) {
    if (!def) return
    switch (action.action) {
      case 'refresh-source': {
        const source = action.source || widget.bindings?.[0]?.source
        if (!source || !def.sources.some(item => item.name === source)) return
        await handleRefreshSource(source)
        return
      }
      case 'open-drilldown': {
        const source = action.source || widget.bindings?.[0]?.source
        setDrilldown({
          widgetTitle: widget.title || widget.id,
          source,
          title: action.title || widget.title || 'Widget drilldown',
          data: action.data !== undefined ? action.data : sourceData,
        })
        return
      }
      case 'set-filter': {
        if (action.filterKey === 'source') {
          const next = typeof action.value === 'string' && action.value ? action.value : null
          setSourceFilter(next)
          return
        }
        if (action.filterKey) {
          setInteractionContext(prev => ({ ...prev, [action.filterKey!]: action.value }))
        }
        return
      }
      case 'navigate-dashboard': {
        if (action.dashboardId) onNavigate(action.dashboardId)
        return
      }
    }
  }

  async function handleEditDraft() {
    if (!def) return
    setDrafting(true)
    setError(null)
    try {
      const draft = await client.editDashboardDraft(def.id)
      onChanged()
      if (draft.id !== def.id) {
        onNavigate(draft.id, { layoutEditing: true })
      } else {
        setDef(draft)
        setLayoutEditing(true)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to create editable draft.')
    } finally {
      setDrafting(false)
    }
  }

  async function handlePublishDraft() {
    if (!def || def.status !== 'draft') return
    const saved = await flushLayoutSave()
    if (!saved) return
    setPublishing(true)
    setError(null)
    try {
      const published = await client.commitDashboardDraft(def.id)
      setDef(published)
      setLayoutEditing(false)
      setSelectedWidgetID(null)
      setEditingWidgetID(null)
      onChanged()
      if (published.id !== def.id) onNavigate(published.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to publish dashboard.')
    } finally {
      setPublishing(false)
    }
  }

  async function handleWidgetSave(widgetID: string, update: DashboardWidgetUpdate) {
    if (!def) return
    setSavingWidget(true)
    setWidgetSaveError(null)
    try {
      const updated = await client.updateDashboardWidget(def.id, widgetID, update)
      setDef(updated)
      setEditingWidgetID(widgetID)
      setWidgetSaveError(null)
      onChanged()
    } catch (e) {
      setWidgetSaveError(e instanceof Error ? e.message : 'Failed to save widget.')
    } finally {
      setSavingWidget(false)
    }
  }

  async function handleToggleLayoutEditing() {
    if (!layoutEditing) {
      setLayoutEditing(true)
      return
    }
    const saved = await flushLayoutSave()
    if (saved) {
      setLayoutEditing(false)
      setSelectedWidgetID(null)
      setEditingWidgetID(null)
    }
  }

  async function confirmDelete() {
    if (!def) return
    setPendingDelete(false)
    try {
      await client.deleteDashboard(def.id)
      onDelete(def.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete dashboard.')
    }
  }

  return (
    <div class="screen">
      <PageHeader
        title={def?.name || 'Dashboard'}
        subtitle={
          def
            ? (def.description || (def.status === 'draft' ? 'Draft — not yet committed.' : `Updated ${formatDate(def.updatedAt)}`))
            : (loading ? 'Loading…' : '')
        }
        actions={
          <>
            <button class="btn btn-sm" onClick={onBack}>← Back</button>
            {def && def.status === 'live' && (
              <button class="btn btn-sm" onClick={handleEditDraft} disabled={drafting}>
                {drafting ? 'Preparing…' : 'Edit layout'}
              </button>
            )}
            {def && def.status === 'draft' && (
              <>
                <button class={`btn btn-sm${layoutEditing ? ' btn-primary' : ''}`} onClick={handleToggleLayoutEditing}>
                  {layoutEditing ? 'Done' : 'Arrange'}
                </button>
                <button class="btn btn-sm btn-primary" onClick={handlePublishDraft} disabled={publishing || savingLayout}>
                  {publishing ? 'Publishing…' : 'Publish'}
                </button>
              </>
            )}
            {def && def.status === 'live' && <button class={`btn btn-sm${refreshError ? ' btn-danger' : ''}`} onClick={handleRefresh} disabled={refreshing}>{refreshing ? 'Refreshing…' : refreshError ? 'Retry' : 'Refresh'}</button>}
            {def && def.status === 'live' && <button class="btn btn-sm" onClick={() => setPendingDelete(true)}>Delete</button>}
          </>
        }
      />
      {error && <p class="error-banner">{error}</p>}
      {loading && <p class="empty-state">Loading dashboard…</p>}
      {!loading && widgets.length === 0 && (
        <p class="empty-state">{loadError ? 'Failed to load widgets.' : 'This dashboard has no widgets yet. Ask Atlas in chat to add some.'}</p>
      )}
      {!loading && (sourceFilter || Object.keys(interactionContext).length > 0) && (
        <div class="dashboard-context-bar">
          {sourceFilter && (
            <button class="dashboard-context-chip" onClick={() => setSourceFilter(null)}>
              Source: {sourceFilter} ×
            </button>
          )}
          {Object.entries(interactionContext).map(([key, value]) => (
            <span key={key} class="dashboard-context-chip passive">{key}: {String(value ?? '—')}</span>
          ))}
        </div>
      )}
      {!loading && isDraft && widgets.length > 0 && (
        <p class="dw-edit-banner">
          {layoutEditing
            ? `Layout editing is enabled. Drag widgets by the grip, resize from the corner, and changes save when you drop a widget${savingLayout ? '…' : layoutDirty ? '. Unsaved changes are being applied.' : '.'}`
            : 'Draft editing is enabled. Select a widget, adjust its settings, then save explicitly.'}
        </p>
      )}
      {layoutError && <p class="error-banner">{layoutError}</p>}
      {!loading && visibleWidgets.length > 0 && (
        <div class={`dashboard-detail-layout${isDraft ? ' dashboard-detail-layout-editing' : ''}`}>
          <div
            key={`${def?.id || id}-${layoutEditing ? 'layout-edit' : 'layout-view'}`}
            ref={gridElRef}
            class={`dashboard-grid${layoutEditing ? ' dashboard-grid-stack grid-stack' : ''}`}
          >
            {visibleWidgets.map(w => {
              const srcName = w.bindings?.[0]?.source
              const src = srcName ? sources[srcName] : undefined
              const sourceData = projectSourceDataForWidget(w, src?.data)
              return (
                <WidgetCell
                  key={`${layoutEditing ? 'edit' : 'view'}-${w.id}`}
                  client={client}
                  dashboardID={id}
                  widget={w}
                  sourceData={sourceData}
                  sourceError={src?.error}
                  sourceAt={src?.at}
                  sourceHealth={src?.health}
                  sourceKind={src?.sourceKind}
                  sourceDurationMs={src?.durationMs}
                  sourceLastSuccessfulAt={src?.lastSuccessfulAt}
                  sourceCacheAgeMs={src?.cacheAgeMs}
                  canEdit={isDraft}
                  selected={selectedWidgetID === w.id || editingWidgetID === w.id}
                  layoutEditing={layoutEditing}
                  onSelect={(next) => { if (isDraft) setSelectedWidgetID(next.id) }}
                  onEdit={(next) => {
                    setSelectedWidgetID(next.id)
                    setEditingWidgetID(next.id)
                    setWidgetSaveError(null)
                  }}
                  onInlineUpdate={handleWidgetSave}
                  onAction={(action) => handleWidgetAction(w, action, sourceData)}
                />
              )
            })}
          </div>
        </div>
      )}
      {!loading && widgets.length > 0 && visibleWidgets.length === 0 && (
        <p class="empty-state">No widgets match the current source filter.</p>
      )}
      {pendingDelete && def && (
        <ConfirmDialog
          title={`Delete "${def.name}"?`}
          body="This dashboard will be permanently removed."
          confirmLabel="Delete"
          danger
          onConfirm={confirmDelete}
          onCancel={() => setPendingDelete(false)}
        />
      )}
      {drilldown && (
        <div class="dashboard-drilldown-backdrop" onClick={() => setDrilldown(null)}>
          <aside class="dashboard-drilldown" onClick={e => e.stopPropagation()}>
            <div class="dashboard-drilldown-header">
              <div>
                <h3>{drilldown.title}</h3>
                <span>{drilldown.source ? `${drilldown.widgetTitle} · ${drilldown.source}` : drilldown.widgetTitle}</span>
              </div>
              <button class="btn btn-sm" onClick={() => setDrilldown(null)}>Close</button>
            </div>
            <pre class="dashboard-drilldown-body">{JSON.stringify(drilldown.data ?? null, null, 2)}</pre>
          </aside>
        </div>
      )}
      {isDraft && def && editingWidget && (
        <div class="dashboard-widget-inspector-backdrop" onClick={() => setEditingWidgetID(null)}>
          <div class="dashboard-widget-inspector-modal" onClick={e => e.stopPropagation()}>
            <WidgetInspector
              dashboard={def}
              widget={editingWidget}
              saving={savingWidget}
              error={widgetSaveError}
              onSave={handleWidgetSave}
              onClose={() => setEditingWidgetID(null)}
            />
          </div>
        </div>
      )}
    </div>
  )
}

// ── list view ─────────────────────────────────────────────────────────────────

export function Dashboards({ client = api }: { client?: DashboardClient } = {}): JSX.Element {
  const [items, setItems]     = useState<DashboardSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError]     = useState<string | null>(null)
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [initialLayoutEditing, setInitialLayoutEditing] = useState(false)
  const [filter, setFilter]   = useState<DashboardStatus | 'all'>('all')

  async function load() {
    setLoading(true); setError(null)
    try {
      const list = await client.dashboards()
      setItems(list)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load dashboards.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [client])

  function handleDeleted(id: string) {
    setItems(prev => prev.filter(i => i.id !== id))
    setSelectedID(null)
    setInitialLayoutEditing(false)
  }

  function openDashboard(id: string, options?: { layoutEditing?: boolean }) {
    setInitialLayoutEditing(!!options?.layoutEditing)
    setSelectedID(id)
  }

  const shown = useMemo(() =>
    filter === 'all' ? items : items.filter(i => i.status === filter),
    [items, filter])

  if (selectedID) {
    return (
      <DashboardDetail
        id={selectedID}
        onBack={() => { setInitialLayoutEditing(false); setSelectedID(null) }}
        onDelete={handleDeleted}
        onNavigate={openDashboard}
        onChanged={load}
        initialLayoutEditing={initialLayoutEditing}
        client={client}
      />
    )
  }

  const draftCount = items.filter(i => i.status === 'draft').length
  const liveCount  = items.filter(i => i.status === 'live').length

  return (
    <div class="screen">
      <PageHeader
        title="Dashboards"
        subtitle="Live data dashboards. Ask Atlas in chat to design one — this screen is a viewer."
        actions={
          items.length > 0 ? (
            <div class="dashboard-filter-group">
              <button class={`btn btn-sm ${filter === 'all'   ? 'btn-primary' : ''}`} onClick={() => setFilter('all')}>All ({items.length})</button>
              <button class={`btn btn-sm ${filter === 'live'  ? 'btn-primary' : ''}`} onClick={() => setFilter('live')}>Live ({liveCount})</button>
              <button class={`btn btn-sm ${filter === 'draft' ? 'btn-primary' : ''}`} onClick={() => setFilter('draft')}>Drafts ({draftCount})</button>
            </div>
          ) : undefined
        }
      />

      {error && <p class="error-banner">{error}</p>}
      {loading && <PageSpinner />}

      {!loading && shown.length > 0 && (
        <div class="dashboard-list">
          {shown.map(item => (
            <button
              key={item.id}
              class="card dashboard-list-card"
              onClick={() => openDashboard(item.id)}
            >
              <div class="dashboard-list-icon"><DashboardIcon /></div>
              <div class="dashboard-list-meta">
                <strong>
                  {item.name}
                  <span class={`dashboard-status-badge ${item.status}`}>
                    {item.status === 'live' ? 'Live' : 'Draft'}
                  </span>
                </strong>
                <span class="dashboard-list-sub">{item.description || 'Custom dashboard'}</span>
                <span class="dashboard-list-stats">
                  {item.widgetCount} widget{item.widgetCount === 1 ? '' : 's'} · {item.sourceCount} source{item.sourceCount === 1 ? '' : 's'} · updated {formatDate(item.updatedAt)}
                </span>
              </div>
            </button>
          ))}
        </div>
      )}

      {!loading && items.length === 0 && (
        <EmptyState
          icon={<DashboardIcon />}
          title="No dashboards yet"
          body="Ask Atlas in chat to build a dashboard for you. Agents author widgets, wire data sources, and commit the layout — this screen shows the result."
        />
      )}

      {!loading && items.length > 0 && shown.length === 0 && (
        <p class="empty-state">No dashboards match this filter.</p>
      )}
    </div>
  )
}
