// Dashboards screen — list saved dashboards, open a detail view, render
// widgets in a 12-column grid. Authoring happens elsewhere (in chat via the
// dashboard.create skill); this screen is a viewer + template installer.
//
// Stage 4: list view, detail view, template picker, widget grid with
// per-widget data resolution. Custom HTML widgets fall through to a
// placeholder until stage 5.

import { useEffect, useState } from 'preact/hooks'
import { JSX } from 'preact'
import {
  api,
  type DashboardDefinition,
  type DashboardSummary,
  type DashboardTemplate,
  type DashboardWidget,
  type DashboardWidgetData,
} from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { WidgetRenderer } from './DashboardWidgets'

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
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

// ── widget cell ───────────────────────────────────────────────────────────────
//
// One cell in the grid. Owns its own fetch lifecycle so widgets resolve in
// parallel and a single failure doesn't poison the whole dashboard.

function WidgetCell({ dashboardID, widget }: { dashboardID: string; widget: DashboardWidget }): JSX.Element {
  const [resolved, setResolved] = useState<DashboardWidgetData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  async function resolve() {
    setLoading(true)
    setError(null)
    try {
      const r = await api.resolveDashboardWidget(dashboardID, widget.id)
      setResolved(r)
      if (!r.success && r.error) setError(r.error)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to resolve widget.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    // Widgets with no backend source render their own embedded content
    // (markdown text, custom_html mini-apps) — no resolve round-trip needed.
    if (!widget.source) {
      setLoading(false)
      return
    }
    resolve()
    if (widget.refreshIntervalSeconds && widget.refreshIntervalSeconds > 0) {
      const interval = window.setInterval(resolve, widget.refreshIntervalSeconds * 1000)
      return () => window.clearInterval(interval)
    }
    return undefined
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dashboardID, widget.id])

  const style: JSX.CSSProperties = {
    gridColumn: `span ${Math.max(1, Math.min(12, widget.gridW || 4))}`,
    gridRow: `span ${Math.max(1, Math.min(12, widget.gridH || 3))}`,
  }

  return (
    <div class="dashboard-widget-card" style={style}>
      {(widget.title || widget.description) && (
        <div class="dashboard-widget-header">
          {widget.title && <h4>{widget.title}</h4>}
          {widget.description && <span class="dashboard-widget-sub">{widget.description}</span>}
        </div>
      )}
      {loading
        ? <div class="dashboard-widget-body dashboard-empty">Loading…</div>
        : <WidgetRenderer widget={widget} data={resolved?.data} error={error ?? undefined} />}
      <div class="dashboard-widget-foot">
        <button class="btn btn-sm" onClick={resolve} disabled={loading} title="Refresh">
          {loading ? '…' : 'Refresh'}
        </button>
        {resolved?.resolvedAt && (
          <span class="dashboard-widget-time">Updated {formatDate(resolved.resolvedAt)}</span>
        )}
      </div>
    </div>
  )
}

// ── detail view ───────────────────────────────────────────────────────────────

function DashboardDetail(
  { id, onBack, onDelete }: { id: string; onBack: () => void; onDelete: (id: string) => void }
): JSX.Element {
  const [def, setDef] = useState<DashboardDefinition | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    async function load() {
      setLoading(true)
      setError(null)
      try {
        const d = await api.dashboard(id)
        if (!cancelled) setDef(d)
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load dashboard.')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [id])

  async function handleDelete() {
    if (!def) return
    if (!confirm(`Delete dashboard "${def.name}"?`)) return
    try {
      await api.deleteDashboard(def.id)
      onDelete(def.id)
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Failed to delete dashboard.')
    }
  }

  return (
    <div class="screen">
      <PageHeader
        title={def?.name || 'Dashboard'}
        subtitle={def?.description || (loading ? 'Loading…' : '')}
        actions={
          <>
            <button class="btn btn-sm" onClick={onBack}>← Back</button>
            {def && <button class="btn btn-sm" onClick={handleDelete}>Delete</button>}
          </>
        }
      />
      {error && <p class="error-banner">{error}</p>}
      {loading && <p class="empty-state">Loading dashboard…</p>}
      {def && def.widgets.length === 0 && <p class="empty-state">This dashboard has no widgets.</p>}
      {def && def.widgets.length > 0 && (
        <div class="dashboard-grid">
          {def.widgets.map(w => (
            <WidgetCell key={w.id} dashboardID={def.id} widget={w} />
          ))}
        </div>
      )}
    </div>
  )
}

// ── list view ─────────────────────────────────────────────────────────────────

export function Dashboards(): JSX.Element {
  const [items, setItems] = useState<DashboardSummary[]>([])
  const [templates, setTemplates] = useState<DashboardTemplate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [installing, setInstalling] = useState<string | null>(null)

  async function load() {
    setLoading(true)
    setError(null)
    try {
      const [list, tmpls] = await Promise.all([
        api.dashboards(),
        api.dashboardTemplates().catch(() => [] as DashboardTemplate[]),
      ])
      setItems(list)
      setTemplates(tmpls)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load dashboards.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  async function handleInstallTemplate(tmpl: DashboardTemplate) {
    setInstalling(tmpl.id)
    try {
      const created = await api.createDashboard({ template: tmpl.id })
      await load()
      setSelectedID(created.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to install template.')
    } finally {
      setInstalling(null)
    }
  }

  function handleDeleted(id: string) {
    setItems(prev => prev.filter(i => i.id !== id))
    setSelectedID(null)
  }

  if (selectedID) {
    return <DashboardDetail id={selectedID} onBack={() => setSelectedID(null)} onDelete={handleDeleted} />
  }

  return (
    <div class="screen">
      <PageHeader
        title="Dashboards"
        subtitle="Live data dashboards. Ask Atlas in chat to create one, or pick a starter template below."
      />

      {error && <p class="error-banner">{error}</p>}
      {loading && <p class="empty-state">Loading dashboards…</p>}

      {!loading && items.length > 0 && (
        <div class="dashboard-list">
          {items.map(item => (
            <button
              key={item.id}
              class="card dashboard-list-card"
              onClick={() => setSelectedID(item.id)}
            >
              <div class="dashboard-list-icon"><DashboardIcon /></div>
              <div class="dashboard-list-meta">
                <strong>{item.name}</strong>
                <span class="dashboard-list-sub">{item.description || 'Custom dashboard'}</span>
                <span class="dashboard-list-stats">
                  {item.widgetCount} widget{item.widgetCount === 1 ? '' : 's'} · updated {formatDate(item.updatedAt)}
                </span>
              </div>
            </button>
          ))}
        </div>
      )}

      {!loading && items.length === 0 && (
        <div class="empty-state">
          <DashboardIcon />
          <h3>No dashboards yet</h3>
          <p>Ask Atlas in chat — try “make me a dashboard showing my last 7 days of token spend” — or pick a starter:</p>
        </div>
      )}

      {!loading && templates.length > 0 && (
        <div class="dashboard-templates">
          <h3 class="dashboard-templates-title">Starter templates</h3>
          <div class="dashboard-list">
            {templates.map(tmpl => (
              <button
                key={tmpl.id}
                class="card dashboard-list-card"
                onClick={() => handleInstallTemplate(tmpl)}
                disabled={installing === tmpl.id}
              >
                <div class="dashboard-list-icon"><DashboardIcon /></div>
                <div class="dashboard-list-meta">
                  <strong>{tmpl.name}</strong>
                  <span class="dashboard-list-sub">{tmpl.description}</span>
                  <span class="dashboard-list-stats">
                    {tmpl.definition.widgets.length} widget{tmpl.definition.widgets.length === 1 ? '' : 's'} · {installing === tmpl.id ? 'installing…' : 'click to install'}
                  </span>
                </div>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
