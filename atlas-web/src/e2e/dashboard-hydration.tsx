import { render } from 'preact'
import { Dashboards } from '../screens/Dashboards'
import type {
  DashboardDefinition,
  DashboardRefreshEvent,
  DashboardSummary,
  DashboardWidgetData,
} from '../api/client'
import '../styles.css'

const dashboardID = 'dashboard-hydration-e2e'

const summary: DashboardSummary = {
  id: dashboardID,
  name: 'Hydration Smoke',
  description: 'Browser harness for dashboard hydration states.',
  status: 'live',
  widgetCount: 2,
  sourceCount: 2,
  createdAt: '2026-04-16T10:00:00Z',
  updatedAt: '2026-04-16T10:00:00Z',
  committedAt: '2026-04-16T10:00:00Z',
}

const definition: DashboardDefinition = {
  schemaVersion: 2,
  id: dashboardID,
  name: 'Hydration Smoke',
  description: 'Browser harness for dashboard hydration states.',
  status: 'live',
  layout: { columns: 12 },
  createdAt: '2026-04-16T10:00:00Z',
  updatedAt: '2026-04-16T10:00:00Z',
  committedAt: '2026-04-16T10:00:00Z',
  sources: [
    { name: 'weather', kind: 'skill', config: {}, refresh: { mode: 'manual' } },
    { name: 'aiNews', kind: 'runtime', config: {}, refresh: { mode: 'manual' } },
  ],
  widgets: [
    {
      id: 'weather-widget',
      title: 'Orlando Weather',
      size: 'half',
      bindings: [{ source: 'weather' }],
      code: { mode: 'preset', preset: 'markdown', options: { path: 'text' } },
      gridX: 0,
      gridY: 0,
      gridW: 6,
      gridH: 2,
    },
    {
      id: 'news-widget',
      title: 'AI News',
      size: 'half',
      bindings: [{ source: 'aiNews' }],
      code: { mode: 'preset', preset: 'markdown', options: { path: 'headline' } },
      gridX: 6,
      gridY: 0,
      gridW: 6,
      gridH: 2,
    },
  ],
}

class MockDashboardEventSource {
  onmessage: ((this: EventSource, ev: MessageEvent<string>) => unknown) | null = null
  onerror: ((this: EventSource, ev: Event) => unknown) | null = null
  readyState = 1
  url = `/dashboards/${dashboardID}/events`
  withCredentials = false
  private timers: number[] = []

  constructor() {
    this.schedule({
      dashboardId: dashboardID,
      source: 'weather',
      data: { text: 'Sunny, 78F, light breeze.' },
      at: '2026-04-16T10:00:01Z',
    }, 650)
    this.schedule({
      dashboardId: dashboardID,
      source: 'aiNews',
      error: 'runtime endpoint /usage/summary returned 401',
      at: '2026-04-16T10:00:02Z',
    }, 950)
  }

  close(): void {
    this.readyState = 2
    for (const timer of this.timers) window.clearTimeout(timer)
    this.timers = []
  }

  addEventListener(): void {}
  removeEventListener(): void {}
  dispatchEvent(): boolean { return true }

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
  dashboards: async (): Promise<DashboardSummary[]> => [summary],
  dashboard: async (): Promise<DashboardDefinition> => definition,
  deleteDashboard: async (): Promise<void> => {},
  refreshDashboard: async (): Promise<DashboardRefreshEvent[]> => [],
  resolveDashboardWidget: async (_dashboardID: string, widgetID: string): Promise<DashboardWidgetData> => ({
    widgetId: widgetID,
    success: true,
    resolvedAt: '2026-04-16T10:00:00Z',
    durationMs: 1,
  }),
  streamDashboardEvents: (): EventSource => new MockDashboardEventSource() as unknown as EventSource,
}

render(<Dashboards client={client} />, document.getElementById('app')!)
