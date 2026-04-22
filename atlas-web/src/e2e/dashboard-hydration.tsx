import { render } from 'preact'
import { Dashboards } from '../screens/Dashboards'
import type {
  DashboardAIWidgetCreateRequest,
  DashboardCodeWidgetCreateRequest,
  DashboardCreateRequest,
  DashboardDefinition,
  DashboardLayoutUpdate,
  DashboardRefreshEvent,
  DashboardSourceCreateRequest,
  DashboardSummary,
  DashboardWidgetCreateRequest,
  DashboardWidgetData,
  DashboardWidgetUpdate,
} from '../api/client'
import {
  dashboardID,
  definition,
  draftDashboardID,
  draftDefinition,
  draftSummary,
  initialEvents,
  refreshedEvents,
  summary,
} from './dashboard-fixtures'
import '../styles.css'

const activeEventSources = new Set<MockDashboardEventSource>()
let editableDraftDefinition: DashboardDefinition = structuredClone(draftDefinition)

function compileDraftCodeWidget(tsx: string): { compiled: string; hash: string } {
  if (tsx.includes('not valid')) {
    throw new Error('esbuild: Expected identifier but found "not" (widget.tsx:1:6)\n1 | this is not valid ::::: typescript\n         ^')
  }
  const compiledLabel = tsx.includes('Metric') ? 'Total' : 'Compiled OK'
  return {
    hash: `draft-${tsx.length}`,
    compiled: `import { h } from 'preact'
import { Card, Text } from '@atlas/ui'
export default function Widget() {
  return h(Card, { title: 'Draft code widget' }, h(Text, null, '${compiledLabel}'))
}`,
  }
}

class MockDashboardEventSource {
  onmessage: ((this: EventSource, ev: MessageEvent<string>) => unknown) | null = null
  onerror: ((this: EventSource, ev: Event) => unknown) | null = null
  readyState = 1
  url = `/dashboards/${dashboardID}/events`
  withCredentials = false
  private timers: number[] = []

  constructor() {
    activeEventSources.add(this)
    this.scheduleMany(initialEvents, 350)
  }

  close(): void {
    this.readyState = 2
    activeEventSources.delete(this)
    for (const timer of this.timers) window.clearTimeout(timer)
    this.timers = []
  }

  addEventListener(): void {}
  removeEventListener(): void {}
  dispatchEvent(): boolean { return true }

  scheduleMany(events: DashboardRefreshEvent[], initialDelayMs: number): void {
    events.forEach((event, index) => this.schedule(event, initialDelayMs + index * 125))
  }

  private schedule(payload: DashboardRefreshEvent, delayMs: number): void {
    const timer = window.setTimeout(() => {
      if (!this.onmessage) return
      this.onmessage.call(this as unknown as EventSource, new MessageEvent<string>('message', {
        data: JSON.stringify(payload),
      }))
    }, delayMs)
    this.timers.push(timer)
  }
}

const client = {
  dashboards: async (): Promise<DashboardSummary[]> => [summary, draftSummary],
  createDashboardDraft: async (input: DashboardCreateRequest): Promise<DashboardDefinition> => ({
    ...structuredClone(draftDefinition),
    id: `draft-${input.name.toLowerCase().replace(/\s+/g, '-')}`,
    name: input.name,
    description: input.description,
    widgets: [],
    status: 'draft',
  }),
  dashboard: async (id: string): Promise<DashboardDefinition> => id === draftDashboardID ? editableDraftDefinition : definition,
  deleteDashboard: async (): Promise<void> => {},
  deleteDashboardWidget: async (_dashboardID: string, widgetID: string): Promise<DashboardDefinition> => {
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      widgets: editableDraftDefinition.widgets.filter(widget => widget.id !== widgetID),
    }
    return editableDraftDefinition
  },
  upsertDashboardSource: async (_dashboardID: string, input: DashboardSourceCreateRequest): Promise<DashboardDefinition> => {
    const nextSource = {
      name: input.name,
      kind: input.kind,
      config: input.config ?? {},
      refresh: { mode: input.refreshMode ?? 'manual', intervalSeconds: input.intervalSeconds },
    }
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      sources: [...editableDraftDefinition.sources.filter(source => source.name !== input.name), nextSource],
    }
    return editableDraftDefinition
  },
  deleteDashboardSource: async (_dashboardID: string, sourceName: string): Promise<DashboardDefinition> => {
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      sources: editableDraftDefinition.sources.filter(source => source.name !== sourceName),
    }
    return editableDraftDefinition
  },
  editDashboardDraft: async (dashboardID: string): Promise<DashboardDefinition> => {
    if (dashboardID === draftDashboardID) return editableDraftDefinition
    editableDraftDefinition = {
      ...structuredClone(definition),
      id: draftDashboardID,
      baseDashboardId: dashboardID,
      status: 'draft',
      name: 'Draft Customizer Smoke',
      description: 'Browser harness for draft widget editing.',
      committedAt: undefined,
    }
    return editableDraftDefinition
  },
  commitDashboardDraft: async (dashboardID: string): Promise<DashboardDefinition> => {
    const published = {
      ...editableDraftDefinition,
      id: editableDraftDefinition.baseDashboardId || dashboardID,
      baseDashboardId: undefined,
      status: 'live' as const,
      committedAt: '2026-04-16T10:03:00Z',
    }
    editableDraftDefinition = structuredClone(draftDefinition)
    return published
  },
  createDashboardWidget: async (_dashboardID: string, input: DashboardWidgetCreateRequest): Promise<DashboardDefinition> => {
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      widgets: [...editableDraftDefinition.widgets, {
        id: `draft-widget-${editableDraftDefinition.widgets.length + 1}`,
        title: input.title,
        description: input.description,
        size: input.size,
        bindings: input.bindings ?? [],
        code: { mode: 'preset', preset: input.preset, options: input.options ?? {} },
        gridX: 0,
        gridY: editableDraftDefinition.widgets.length + 1,
        gridW: input.size === 'quarter' ? 3 : input.size === 'third' ? 4 : input.size === 'full' ? 12 : 6,
        gridH: input.size === 'tall' ? 4 : input.size === 'quarter' || input.size === 'third' ? 1 : 2,
      }],
    }
    return editableDraftDefinition
  },
  createDashboardCodeWidget: async (_dashboardID: string, input: DashboardCodeWidgetCreateRequest): Promise<DashboardDefinition> => {
    const built = compileDraftCodeWidget(input.tsx)
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      widgets: [...editableDraftDefinition.widgets, {
        id: `draft-code-${editableDraftDefinition.widgets.length + 1}`,
        title: input.title,
        description: input.description,
        size: input.size,
        bindings: input.bindings ?? [],
        code: { mode: 'code', tsx: input.tsx, compiled: built.compiled, hash: built.hash },
        gridX: 0,
        gridY: editableDraftDefinition.widgets.length + 1,
        gridW: input.size === 'quarter' ? 3 : input.size === 'third' ? 4 : input.size === 'full' ? 12 : 6,
        gridH: input.size === 'tall' ? 4 : input.size === 'quarter' || input.size === 'third' ? 1 : 2,
      }],
    }
    return editableDraftDefinition
  },
  createDashboardAIWidget: async (_dashboardID: string, input: DashboardAIWidgetCreateRequest): Promise<DashboardDefinition> => {
    const useCode = /custom|interactive|tabs|button|tsx/i.test(input.prompt)
    if (useCode) {
      const tsx = `import { Card, Text } from '@atlas/ui'

export default function Widget({ data }) {
  return (
    <Card title="${input.title || 'AI Widget'}">
      <Text>${input.prompt.replace(/"/g, '\\"')}</Text>
    </Card>
  )
}`
      const built = compileDraftCodeWidget(tsx)
      editableDraftDefinition = {
        ...editableDraftDefinition,
        updatedAt: '2026-04-16T10:02:00Z',
        widgets: [...editableDraftDefinition.widgets, {
          id: `draft-ai-code-${editableDraftDefinition.widgets.length + 1}`,
          title: input.title || 'AI Widget',
          description: 'Generated from prompt',
          size: input.size ?? 'half',
          bindings: input.source ? [{ source: input.source }] : [],
          code: { mode: 'code', tsx, compiled: built.compiled, hash: built.hash },
          gridX: 0,
          gridY: editableDraftDefinition.widgets.length + 1,
          gridW: 6,
          gridH: 2,
        }],
      }
      return editableDraftDefinition
    }
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      widgets: [...editableDraftDefinition.widgets, {
        id: `draft-ai-widget-${editableDraftDefinition.widgets.length + 1}`,
        title: input.title || 'AI Summary',
        description: 'Generated from prompt',
        size: input.size ?? 'half',
        bindings: input.source ? [{ source: input.source }] : [],
        code: { mode: 'preset', preset: 'markdown', options: { text: `## ${input.title || 'AI Summary'}\n\n${input.prompt}` } },
        gridX: 0,
        gridY: editableDraftDefinition.widgets.length + 1,
        gridW: 6,
        gridH: 2,
      }],
    }
    return editableDraftDefinition
  },
  updateDashboardLayout: async (_dashboardID: string, update: DashboardLayoutUpdate): Promise<DashboardDefinition> => {
    const positions = new Map(update.widgets.map(item => [item.id, item]))
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      widgets: editableDraftDefinition.widgets.map(widget => {
        const next = positions.get(widget.id)
        return next ? { ...widget, gridX: next.gridX, gridY: next.gridY, gridW: next.gridW, gridH: next.gridH } : widget
      }),
    }
    return editableDraftDefinition
  },
  updateDashboardWidget: async (_dashboardID: string, widgetID: string, update: DashboardWidgetUpdate): Promise<DashboardDefinition> => {
    editableDraftDefinition = {
      ...editableDraftDefinition,
      updatedAt: '2026-04-16T10:02:00Z',
      widgets: editableDraftDefinition.widgets.map(widget => {
        if (widget.id !== widgetID) return widget
        const size = update.size ?? widget.size
        if (widget.code.mode === 'code' && update.tsx !== undefined) {
          const built = compileDraftCodeWidget(update.tsx)
          return {
            ...widget,
            title: update.title ?? widget.title,
            description: update.description ?? widget.description,
            size,
            bindings: update.bindings ?? widget.bindings,
            code: {
              ...widget.code,
              tsx: update.tsx,
              compiled: built.compiled,
              hash: built.hash,
            },
            gridW: size === 'half' ? 6 : widget.gridW,
            gridH: size === 'half' ? 2 : widget.gridH,
          }
        }
        return {
          ...widget,
          title: update.title ?? widget.title,
          description: update.description ?? widget.description,
          size,
          bindings: update.bindings ?? widget.bindings,
          code: {
            ...widget.code,
            preset: update.preset ?? widget.code.preset,
            options: update.options ?? widget.code.options,
          },
          gridW: size === 'half' ? 6 : widget.gridW,
          gridH: size === 'half' ? 2 : widget.gridH,
        }
      }),
    }
    return editableDraftDefinition
  },
  refreshDashboard: async (): Promise<DashboardRefreshEvent[]> => {
    for (const source of activeEventSources) source.scheduleMany(refreshedEvents, 650)
    await new Promise(resolve => window.setTimeout(resolve, 750))
    return refreshedEvents
  },
  refreshDashboardSource: async (_dashboardID: string, source: string): Promise<DashboardRefreshEvent> => {
    const event = refreshedEvents.find(item => item.source === source) ?? refreshedEvents[0]
    for (const es of activeEventSources) es.scheduleMany([event], 150)
    await new Promise(resolve => window.setTimeout(resolve, 180))
    return event
  },
  resolveDashboardWidget: async (_dashboardID: string, widgetID: string): Promise<DashboardWidgetData> => ({
    widgetId: widgetID,
    success: true,
    resolvedAt: '2026-04-16T10:00:00Z',
    durationMs: 1,
  }),
  streamDashboardEvents: (): EventSource => new MockDashboardEventSource() as unknown as EventSource,
}

render(<Dashboards client={client} />, document.getElementById('app')!)
