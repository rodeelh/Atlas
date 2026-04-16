// Dashboards screen (v2) — list saved dashboards and view widgets live.
//
// Authoring is agent-driven through the dashboard.* skills in chat; this
// screen is a viewer. Widgets render in a 12-column grid at positions the
// backend packer committed. Data is driven by per-dashboard SSE: the
// coordinator replays the latest event for every source on subscribe and
// pushes an event each time a source refreshes. Widgets bind to a source
// by name via `widget.bindings[0].source`.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { JSX } from 'preact'
import {
  api,
  type DashboardDefinition,
  type DashboardRefreshEvent,
  type DashboardStatus,
  type DashboardSummary,
  type DashboardWidget,
  type DashboardWidgetData,
} from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { EmptyState } from '../components/EmptyState'
import { WidgetRenderer } from './DashboardWidgets'
import { ConfirmDialog } from '../components/ConfirmDialog'

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

interface WidgetCellProps {
  dashboardID: string
  widget: DashboardWidget
  /** Latest data for the source this widget is bound to (undefined if unbound). */
  sourceData: unknown
  sourceError?: string
  sourceAt?: string
}

function WidgetCell({ dashboardID, widget, sourceData, sourceError, sourceAt }: WidgetCellProps): JSX.Element {
  // If the widget has no binding we fall back to a one-shot resolve when
  // the cell mounts — this is the path for unbound widgets (e.g. static
  // markdown with inline `text` in options).
  const [fallback, setFallback] = useState<DashboardWidgetData | null>(null)
  const [fallbackErr, setFallbackErr] = useState<string | null>(null)
  const hasBinding = Array.isArray(widget.bindings) && widget.bindings.length > 0

  useEffect(() => {
    if (hasBinding) return
    let cancelled = false
    api.resolveDashboardWidget(dashboardID, widget.id)
      .then(r => { if (!cancelled) { setFallback(r); if (!r.success && r.error) setFallbackErr(r.error) } })
      .catch(e => { if (!cancelled) setFallbackErr(e instanceof Error ? e.message : 'Failed to load widget data.') })
    return () => { cancelled = true }
  }, [dashboardID, widget.id, hasBinding])

  const data  = hasBinding ? sourceData  : fallback?.data
  const error = hasBinding ? sourceError : (fallbackErr ?? (fallback && !fallback.success ? fallback.error : undefined))
  const at    = hasBinding ? sourceAt    : fallback?.resolvedAt

  const x = Math.max(0, widget.gridX ?? 0)
  const y = Math.max(0, widget.gridY ?? 0)
  const w = Math.max(1, Math.min(12, widget.gridW || 4))
  const h = Math.max(1, Math.min(12, widget.gridH || 3))
  const style: JSX.CSSProperties = {
    gridColumn: `${x + 1} / span ${w}`,
    gridRow:    `${y + 1} / span ${h}`,
  }

  return (
    <div class="dw-cell" style={style}>
      <div class="dashboard-widget-card">
        {(widget.title || widget.description) && (
          <div class="dashboard-widget-header">
            <div class="dashboard-widget-header-left">
              {widget.title && <h4>{widget.title}</h4>}
              {widget.description && <span class="dashboard-widget-sub">{widget.description}</span>}
            </div>
            {at && (
              <span class="dashboard-widget-timestamp" title={`Updated ${formatDate(at)}`}>
                {new Date(at).toLocaleTimeString()}
              </span>
            )}
          </div>
        )}
        <div class="dashboard-widget-content">
          <WidgetRenderer widget={widget} data={data} error={error} />
        </div>
      </div>
    </div>
  )
}

// ── detail view ───────────────────────────────────────────────────────────────

interface SourceEntry { data?: unknown; error?: string; at?: string }

function DashboardDetail(
  { id, onBack, onDelete }: { id: string; onBack: () => void; onDelete: (id: string) => void }
): JSX.Element {
  const [def, setDef]           = useState<DashboardDefinition | null>(null)
  const [loading, setLoading]   = useState(true)
  const [error, setError]       = useState<string | null>(null)
  const [sources, setSources]   = useState<Record<string, SourceEntry>>({})
  const [refreshing, setRefreshing] = useState(false)
  const [pendingDelete, setPendingDelete] = useState(false)
  const esRef = useRef<EventSource | null>(null)

  // Load the dashboard definition. The SSE stream replays latest source
  // events on subscribe, so we don't need a separate initial data fetch.
  useEffect(() => {
    let cancelled = false
    setLoading(true); setError(null)
    api.dashboard(id)
      .then(d => { if (!cancelled) setDef(d) })
      .catch(e => { if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load dashboard.') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [id])

  // Subscribe to the per-dashboard event stream. The coordinator synchronously
  // replays the latest cached event for every source when we subscribe, so the
  // grid paints on the first tick without a round-trip.
  useEffect(() => {
    const es = api.streamDashboardEvents(id)
    esRef.current = es
    es.onmessage = (ev: MessageEvent<string>) => {
      try {
        const payload = JSON.parse(ev.data) as DashboardRefreshEvent
        setSources(prev => ({
          ...prev,
          [payload.source]: { data: payload.data, error: payload.error || undefined, at: payload.at },
        }))
      } catch { /* ignore malformed frames */ }
    }
    es.onerror = () => { /* browser auto-reconnects; nothing to do */ }
    return () => {
      es.close()
      esRef.current = null
    }
  }, [id])

  async function handleRefresh() {
    setRefreshing(true)
    try { await api.refreshDashboard(id) }
    catch (e) { setError(e instanceof Error ? e.message : 'Refresh failed.') }
    finally { setRefreshing(false) }
  }

  async function confirmDelete() {
    if (!def) return
    setPendingDelete(false)
    try {
      await api.deleteDashboard(def.id)
      onDelete(def.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete dashboard.')
    }
  }

  const widgets = def?.widgets ?? []

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
            {def && (
              <span class={`dashboard-status-badge ${def.status}`} title={def.committedAt ? `Committed ${formatDate(def.committedAt)}` : undefined}>
                {def.status === 'live' ? 'Live' : 'Draft'}
              </span>
            )}
            {def && <button class="btn btn-sm" onClick={handleRefresh} disabled={refreshing}>{refreshing ? 'Refreshing…' : 'Refresh'}</button>}
            {def && <button class="btn btn-sm" onClick={() => setPendingDelete(true)}>Delete</button>}
          </>
        }
      />
      {error && <p class="error-banner">{error}</p>}
      {loading && <p class="empty-state">Loading dashboard…</p>}
      {!loading && widgets.length === 0 && (
        <p class="empty-state">This dashboard has no widgets yet. Ask Atlas in chat to add some.</p>
      )}
      {!loading && widgets.length > 0 && (
        <div class="dashboard-grid">
          {widgets.map(w => {
            const srcName = w.bindings?.[0]?.source
            const src = srcName ? sources[srcName] : undefined
            return (
              <WidgetCell
                key={w.id}
                dashboardID={id}
                widget={w}
                sourceData={src?.data}
                sourceError={src?.error}
                sourceAt={src?.at}
              />
            )
          })}
        </div>
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
    </div>
  )
}

// ── list view ─────────────────────────────────────────────────────────────────

export function Dashboards(): JSX.Element {
  const [items, setItems]     = useState<DashboardSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError]     = useState<string | null>(null)
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [filter, setFilter]   = useState<DashboardStatus | 'all'>('all')

  async function load() {
    setLoading(true); setError(null)
    try {
      const list = await api.dashboards()
      setItems(list)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load dashboards.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  function handleDeleted(id: string) {
    setItems(prev => prev.filter(i => i.id !== id))
    setSelectedID(null)
  }

  const shown = useMemo(() =>
    filter === 'all' ? items : items.filter(i => i.status === filter),
    [items, filter])

  if (selectedID) {
    return <DashboardDetail id={selectedID} onBack={() => setSelectedID(null)} onDelete={handleDeleted} />
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
              onClick={() => setSelectedID(item.id)}
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
