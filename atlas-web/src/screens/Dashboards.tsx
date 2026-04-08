// Dashboards screen — list saved dashboards, open a detail view, render
// widgets in a 12-column grid. Authoring happens elsewhere (in chat via the
// dashboard.create skill); this screen is a viewer + template installer.
//
// Stage 4: list view, detail view, template picker, widget grid with
// per-widget data resolution. Custom HTML widgets fall through to a
// placeholder until stage 5.

import { useEffect, useRef, useState } from 'preact/hooks'
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

interface WidgetCellProps {
  dashboardID: string
  widget: DashboardWidget
  editMode?: boolean
  onDragStart?: (e: PointerEvent, widget: DashboardWidget) => void
  onResizeStart?: (e: PointerEvent, widget: DashboardWidget) => void
}

function WidgetCell({ dashboardID, widget, editMode, onDragStart, onResizeStart }: WidgetCellProps): JSX.Element {
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
    if (!widget.source) { setLoading(false); return }
    resolve()
    if (widget.refreshIntervalSeconds && widget.refreshIntervalSeconds > 0) {
      const interval = window.setInterval(resolve, widget.refreshIntervalSeconds * 1000)
      return () => window.clearInterval(interval)
    }
    return undefined
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dashboardID, widget.id])

  const x = Math.max(0, widget.gridX ?? 0)
  const y = Math.max(0, widget.gridY ?? 0)
  const w = Math.max(1, Math.min(12, widget.gridW || 4))
  const h = Math.max(1, Math.min(12, widget.gridH || 3))

  const style: JSX.CSSProperties = {
    gridColumn: `${x + 1} / span ${w}`,
    gridRow:    `${y + 1} / span ${h}`,
  }

  return (
    <div class={`dw-cell${editMode ? ' dw-cell-edit' : ''}`} style={style}>
      <div class={`dashboard-widget-card${editMode ? ' dw-edit-mode' : ''}`}>
        <div class="dashboard-widget-header">
          <div class="dashboard-widget-header-left">
            {widget.title && <h4>{widget.title}</h4>}
            {widget.description && <span class="dashboard-widget-sub">{widget.description}</span>}
          </div>
          {!editMode && (
            <button class="dashboard-widget-refresh" onClick={resolve} disabled={loading}
              title={resolved?.resolvedAt ? `Updated ${formatDate(resolved.resolvedAt)}` : 'Refresh'}>
              {loading ? '⟳' : '↺'}
            </button>
          )}
        </div>
        <div class={`dashboard-widget-content${editMode ? ' dw-no-interact' : ''}`}>
          {loading
            ? <div class="dashboard-widget-body dashboard-empty">Loading…</div>
            : <WidgetRenderer widget={widget} data={resolved?.data} error={error ?? undefined} />}
        </div>
      </div>
      {editMode && (
        <div
          class="dw-drag-handle"
          onPointerDown={onDragStart ? (e) => { e.stopPropagation(); onDragStart(e as unknown as PointerEvent, widget) } : undefined}
          title="Drag to move"
        >
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true">
            <circle cx="2" cy="2" r="1.4" fill="currentColor"/>
            <circle cx="8" cy="2" r="1.4" fill="currentColor"/>
            <circle cx="2" cy="8" r="1.4" fill="currentColor"/>
            <circle cx="8" cy="8" r="1.4" fill="currentColor"/>
          </svg>
        </div>
      )}
      {editMode && (
        <div
          class="dw-resize-handle"
          onPointerDown={onResizeStart ? (e) => { e.stopPropagation(); onResizeStart(e as unknown as PointerEvent, widget) } : undefined}
          title="Drag to resize"
        />
      )}
    </div>
  )
}

// ── drag/resize constants ──────────────────────────────────────────────────────

const GRID_COLS = 12
const ROW_H     = 82  // grid-auto-rows (72px) + gap (10px)

function minSize(kind: string): { w: number; h: number } {
  switch (kind) {
    case 'metric':     return { w: 2, h: 2 }
    case 'line_chart':
    case 'bar_chart':  return { w: 3, h: 4 }
    case 'table':      return { w: 3, h: 3 }
    case 'list':       return { w: 2, h: 3 }
    case 'news':       return { w: 3, h: 3 }
    case 'markdown':   return { w: 2, h: 2 }
    default:           return { w: 2, h: 2 }
  }
}

type Rect = { gridX: number; gridY: number; gridW: number; gridH: number }

function rectsOverlap(a: Rect, b: Rect): boolean {
  return a.gridX          < b.gridX + b.gridW &&
         a.gridX + a.gridW > b.gridX &&
         a.gridY          < b.gridY + b.gridH &&
         a.gridY + a.gridH > b.gridY
}

// Gravity-compact: slide every widget (except the pinned one) as far up as
// possible without overlapping anything already placed. Pinned widget is
// treated as an immovable obstacle so dragging / resizing doesn't pull it.
function compact(widgets: DashboardWidget[], pinnedId?: string): DashboardWidget[] {
  const pinned  = widgets.find(w => w.id === pinnedId)
  const free    = widgets.filter(w => w.id !== pinnedId)
  // Process top-to-bottom, left-to-right so earlier widgets don't block later ones unnecessarily
  const sorted  = [...free].sort((a, b) => a.gridY !== b.gridY ? a.gridY - b.gridY : a.gridX - b.gridX)
  const placed: DashboardWidget[] = pinned ? [pinned] : []

  for (const w of sorted) {
    // Walk upward from y=0; take the first row where the widget fits
    let bestY = w.gridY
    for (let tryY = 0; tryY <= w.gridY; tryY++) {
      const candidate = { ...w, gridY: tryY }
      if (!placed.some(p => rectsOverlap(candidate, p))) {
        bestY = tryY
        break
      }
    }
    placed.push({ ...w, gridY: bestY })
  }
  // Restore original order so React keys stay stable
  const order = new Map(widgets.map((w, i) => [w.id, i]))
  return placed.sort((a, b) => (order.get(a.id) ?? 0) - (order.get(b.id) ?? 0))
}

function centerOf(w: Rect) {
  return { cx: w.gridX + w.gridW / 2, cy: w.gridY + w.gridH / 2 }
}

// During a drag: if the dragged widget's center lands inside another widget,
// swap positions. Otherwise just place and compact.
function applyDrag(
  prev: DashboardWidget[],
  updated: DashboardWidget,
  origX: number,
  origY: number,
): DashboardWidget[] {
  const { cx, cy } = centerOf(updated)
  const swapTarget = prev.find(w =>
    w.id !== updated.id &&
    cx > w.gridX && cx < w.gridX + w.gridW &&
    cy > w.gridY && cy < w.gridY + w.gridH,
  )
  if (swapTarget) {
    // Swap: move target to dragged widget's original position
    const swapped = prev.map(w => {
      if (w.id === updated.id) return updated
      if (w.id === swapTarget.id) return { ...w, gridX: origX, gridY: origY }
      return w
    })
    return compact(swapped, updated.id)
  }
  // No swap — push any overlapping widgets down, then compact
  const pushed = prev.map(w => {
    if (w.id === updated.id) return updated
    if (rectsOverlap(updated, w)) return { ...w, gridY: updated.gridY + updated.gridH }
    return w
  })
  return compact(pushed, updated.id)
}

// Apply a resize to the active widget, push overlapping widgets, compact.
function applyResize(
  prev: DashboardWidget[],
  updated: DashboardWidget,
): DashboardWidget[] {
  const pushed = prev.map(w => {
    if (w.id === updated.id) return updated
    if (rectsOverlap(updated, w)) return { ...w, gridY: updated.gridY + updated.gridH }
    return w
  })
  return compact(pushed, updated.id)
}

// ── detail view ───────────────────────────────────────────────────────────────

function DashboardDetail(
  { id, onBack, onDelete }: { id: string; onBack: () => void; onDelete: (id: string) => void }
): JSX.Element {
  const [def, setDef] = useState<DashboardDefinition | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editMode, setEditMode] = useState(false)
  const [localWidgets, setLocalWidgets] = useState<DashboardWidget[]>([])
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const gridRef = useRef<HTMLDivElement>(null)

  // Drag and resize state — stored in refs to avoid triggering re-renders on
  // every pointermove. Only setLocalWidgets (React state) triggers re-renders
  // when the snapped grid position actually changes.
  const dragRef  = useRef<{ id: string; startX: number; startY: number; origX: number; origY: number; origW: number; colW: number } | null>(null)
  const resizeRef = useRef<{ id: string; startX: number; startY: number; origW: number; origH: number; origX: number; colW: number } | null>(null)
  // Snapshot of widget positions at the moment drag/resize started — used as
  // the immutable base for every frame calculation so mutations don't compound.
  const snapWidgets = useRef<DashboardWidget[]>([])
  // Track last snapped value to skip redundant setLocalWidgets calls
  const lastSnapRef = useRef<{ x?: number; y?: number; w?: number; h?: number }>({})

  useEffect(() => {
    let cancelled = false
    async function load() {
      setLoading(true); setError(null)
      try {
        const d = await api.dashboard(id)
        if (!cancelled) { setDef(d); setLocalWidgets(d.widgets) }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load dashboard.')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [id])

  // Attach pointer listeners to window while edit mode is active
  useEffect(() => {
    if (!editMode) return
    function onMove(e: PointerEvent) {
      if (dragRef.current) {
        const { id: wid, startX, startY, origX, origY, origW, colW } = dragRef.current
        const dx = Math.round((e.clientX - startX) / colW)
        const dy = Math.round((e.clientY - startY) / ROW_H)
        const newX = Math.max(0, Math.min(GRID_COLS - origW, origX + dx))
        const newY = Math.max(0, origY + dy)
        if (newX !== lastSnapRef.current.x || newY !== lastSnapRef.current.y) {
          lastSnapRef.current = { x: newX, y: newY }
          // Always compute from the pre-drag snapshot so each frame is independent
          const base = snapWidgets.current
          const dragged = base.find(w => w.id === wid)
          if (!dragged) return
          setLocalWidgets(applyDrag(base, { ...dragged, gridX: newX, gridY: newY }, origX, origY))
        }
        return
      }
      if (resizeRef.current) {
        const { id: wid, startX, startY, origW, origH, origX, colW } = resizeRef.current
        const dx = Math.round((e.clientX - startX) / colW)
        const dy = Math.round((e.clientY - startY) / ROW_H)
        const resized = snapWidgets.current.find(w => w.id === wid)
        const min = resized ? minSize(resized.kind) : { w: 2, h: 2 }
        const newW = Math.max(min.w, Math.min(GRID_COLS - origX, origW + dx))
        const newH = Math.max(min.h, origH + dy)
        if (newW !== lastSnapRef.current.w || newH !== lastSnapRef.current.h) {
          lastSnapRef.current = { w: newW, h: newH }
          const base = snapWidgets.current
          const resized = base.find(w => w.id === wid)
          if (!resized) return
          setLocalWidgets(applyResize(base, { ...resized, gridW: newW, gridH: newH }))
        }
      }
    }
    function onUp() {
      dragRef.current = null
      resizeRef.current = null
      lastSnapRef.current = {}
      // Commit: update snapshot to whatever was last rendered
      snapWidgets.current = []
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
  }, [editMode])

  function handleDragStart(e: PointerEvent, widget: DashboardWidget) {
    if (!gridRef.current) return
    e.preventDefault()
    const colW = (gridRef.current.getBoundingClientRect().width + 10) / GRID_COLS
    dragRef.current = { id: widget.id, startX: e.clientX, startY: e.clientY, origX: widget.gridX, origY: widget.gridY, origW: widget.gridW, colW }
    snapWidgets.current = localWidgets.map(w => ({ ...w }))
    lastSnapRef.current = {}
  }

  function handleResizeStart(e: PointerEvent, widget: DashboardWidget) {
    if (!gridRef.current) return
    e.preventDefault()
    const colW = (gridRef.current.getBoundingClientRect().width + 10) / GRID_COLS
    resizeRef.current = { id: widget.id, startX: e.clientX, startY: e.clientY, origW: widget.gridW, origH: widget.gridH, origX: widget.gridX, colW }
    snapWidgets.current = localWidgets.map(w => ({ ...w }))
    lastSnapRef.current = {}
  }

  function handleEditToggle() {
    if (editMode) {
      // Cancel — revert to saved def
      if (def) setLocalWidgets(def.widgets)
      setEditMode(false)
      setSaveError(null)
    } else {
      setEditMode(true)
    }
  }

  async function handleSave() {
    if (!def) return
    setSaving(true); setSaveError(null)
    try {
      const updated = await api.updateDashboard({ ...def, widgets: localWidgets })
      setDef(updated)
      setLocalWidgets(updated.widgets)
      setEditMode(false)
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : 'Failed to save layout.')
    } finally {
      setSaving(false)
    }
  }

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

  const displayWidgets = editMode ? localWidgets : (def?.widgets ?? [])

  return (
    <div class="screen">
      <PageHeader
        title={def?.name || 'Dashboard'}
        subtitle={def?.description || (loading ? 'Loading…' : '')}
        actions={
          <>
            <button class="btn btn-sm" onClick={onBack}>← Back</button>
            {def && !editMode && <button class="btn btn-sm" onClick={handleEditToggle}>Edit Layout</button>}
            {editMode && <button class="btn btn-sm" onClick={handleEditToggle}>Cancel</button>}
            {editMode && <button class="btn btn-sm btn-primary" onClick={handleSave} disabled={saving}>{saving ? 'Saving…' : 'Save Layout'}</button>}
            {def && !editMode && <button class="btn btn-sm" onClick={handleDelete}>Delete</button>}
          </>
        }
      />
      {(error || saveError) && <p class="error-banner">{error || saveError}</p>}
      {editMode && <p class="dw-edit-banner">Drag widgets to reposition · drag the ◢ handle to resize · click Save when done</p>}
      {loading && <p class="empty-state">Loading dashboard…</p>}
      {!loading && displayWidgets.length === 0 && <p class="empty-state">This dashboard has no widgets.</p>}
      {!loading && displayWidgets.length > 0 && (
        <div class="dashboard-grid" ref={gridRef}>
          {displayWidgets.map(w => (
            <WidgetCell
              key={w.id}
              dashboardID={def!.id}
              widget={w}
              editMode={editMode}
              onDragStart={handleDragStart}
              onResizeStart={handleResizeStart}
            />
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
