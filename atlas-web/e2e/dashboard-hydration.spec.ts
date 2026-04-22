import { expect, test, type Page } from '@playwright/test'

async function openWidgetAction(page: Page, widgetTitle: string, actionLabel: 'Edit widget' | 'Delete widget') {
  const widget = page.locator('.dw-cell').filter({ hasText: widgetTitle })
  await widget.getByRole('button', { name: new RegExp(`Widget actions for ${widgetTitle}`, 'i') }).click()
  await page.getByRole('button', { name: actionLabel, exact: true }).click()
}

async function openWidgetMenu(page: Page, widgetTitle: string) {
  const widget = page.locator('.dw-cell').filter({ hasText: widgetTitle })
  await widget.getByRole('button', { name: new RegExp(`Widget actions for ${widgetTitle}`, 'i') }).click()
}

test('dashboard hydration renders presets, source errors, and refresh recovery', async ({ page }) => {
  await page.goto('/web/dashboard-hydration.e2e.html')

  await page.getByRole('button', { name: /Hydration Smoke/i }).click()

  await expect(page.getByText('Active conversations')).toBeVisible()
  await expect(page.getByText('Memory rows')).toBeVisible()
  await expect(page.getByText('Agents')).toBeVisible()
  await expect(page.getByText('Memories recorded')).toBeVisible()
  await expect(page.getByText('Token usage per day')).toBeVisible()
  await expect(page.getByText('Operations note')).toBeVisible()
  await expect(page.getByText('AI News')).toBeVisible()
  await expect(page.getByText('System load')).toBeVisible()
  await expect(page.getByText('Usage gauge')).toBeVisible()
  await expect(page.getByText('Agent status')).toBeVisible()
  await expect(page.getByText('Usage trend')).toBeVisible()
  await expect(page.getByText('Usage mix')).toBeVisible()
  await expect(page.getByText('Team KPIs')).toBeVisible()
  await expect(page.getByText('Usage donut')).toBeVisible()
  await expect(page.getByText('Latency spread')).toBeVisible()
  await expect(page.getByText('Queue balance')).toBeVisible()
  await expect(page.getByText('Ops timeline')).toBeVisible()
  await expect(page.getByText('Daily intensity')).toBeVisible()
  await expect(page.getByText('Loading source…')).toHaveCount(19)
  await expect(page.locator('.dashboard-widget-health.loading')).toHaveCount(19)

  await expect(page.locator('.dw-metric-value').getByText('3', { exact: true })).toBeVisible()
  await expect(page.getByText('preferences')).toBeVisible()
  const listWidget = page.locator('.dashboard-widget-list')
  await expect(listWidget.getByText('agent-alpha')).toBeVisible()
  await expect(listWidget.getByText('agent-gamma')).toHaveCount(0)
  await page.getByRole('button', { name: 'Show 1 more' }).click()
  await expect(listWidget.getByText('agent-gamma')).toBeVisible()
  await expect(page.getByLabel('Memory rows search')).toBeVisible()
  await expect(page.getByText('workflow')).toHaveCount(0)
  await page.getByRole('button', { name: 'Next' }).click()
  await expect(page.getByText('workflow')).toBeVisible()
  await page.getByLabel('Memory rows search').fill('projects')
  await expect(page.getByText('projects')).toBeVisible()
  await expect(page.getByText('preferences')).toHaveCount(0)
  await expect(page.getByText('All dashboard presets hydrated from source events.')).toBeVisible()
  await expect(page.locator('.dw-chart-wrap canvas')).toHaveCount(7)
  await expect(page.getByText('3 points')).toHaveCount(4)
  await expect(page.getByText('3 segments')).toHaveCount(2)
  await expect(page.getByRole('progressbar', { name: 'Active capacity' })).toBeVisible()
  await expect(page.locator('.dw-gauge-value').getByText('3 chats', { exact: true })).toBeVisible()
  await expect(page.locator('.dw-status-grid')).toBeVisible()
  await expect(page.locator('.dw-kpi-value').getByText('18', { exact: true })).toBeVisible()
  await expect(page.locator('.dw-timeline')).toBeVisible()
  await expect(page.locator('.dw-heatmap')).toBeVisible()
  await expect(page.locator('.dashboard-widget-health.ok')).toHaveCount(18)

  await expect(page.getByText('News source unavailable')).toBeVisible()
  await expect(page.locator('.dashboard-widget-health.error')).toHaveCount(1)

  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByRole('button', { name: 'Refreshing…' })).toBeVisible()
  await expect(page.locator('.dashboard-widget-health.loading')).toHaveCount(19)
  await expect(page.locator('.dw-metric-value').getByText('3', { exact: true })).toBeVisible()

  await expect(page.locator('.dw-metric-value').getByText('4', { exact: true })).toBeVisible()
  await expect(page.locator('.dw-gauge-value').getByText('4 chats', { exact: true })).toBeVisible()
  await expect(listWidget.getByText('agent-epsilon')).toBeVisible()
  await expect(page.getByText('Needs review')).toBeVisible()
  await expect(page.getByText('All dashboard presets hydrated from source events.')).toBeVisible()
  await expect(page.getByText('Showing last good data. Latest refresh failed.')).toBeVisible()
  await expect(page.locator('.dashboard-widget-health.stale')).toHaveCount(1)
  await expect(page.locator('.dw-kpi-value').getByText('21', { exact: true })).toBeVisible()
  await expect(page.locator('.dashboard-widget-health.ok')).toHaveCount(17)
})

test('draft dashboards expose a widget inspector and save explicit edits', async ({ page }) => {
  await page.goto('/web/dashboard-hydration.e2e.html')

  await page.getByRole('button', { name: /Draft Customizer Smoke/i }).click()

  await expect(page.getByText('Draft editing is enabled.')).toBeVisible()

  await openWidgetAction(page, 'Draft metric', 'Edit widget')
  await expect(page.getByRole('heading', { name: 'Widget' })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue('Draft metric')

  await page.getByRole('textbox', { name: 'Title', exact: true }).fill('Edited metric')
  await page.getByRole('combobox', { name: 'Size' }).selectOption('half')
  await page.getByRole('combobox', { name: 'Preset' }).selectOption('markdown')
  await page.getByRole('textbox', { name: 'Binding path' }).fill('items[0]')
  await page.getByRole('textbox', { name: 'Options JSON' }).fill('{"path":"title"}')
  await page.getByRole('button', { name: 'Save widget' }).click()

  await expect(page.getByRole('textbox', { name: 'Widget title' })).toHaveValue('Edited metric')
  await expect(page.getByText('agent-alpha')).toBeVisible()
  await expect(page.locator('.dw-cell.dw-size-half').filter({ has: page.getByRole('textbox', { name: 'Widget title' }) })).toHaveCount(1)
})

test('draft code widgets surface compile errors and save valid TSX', async ({ page }) => {
  await page.goto('/web/dashboard-hydration.e2e.html')

  await page.getByRole('button', { name: /Draft Customizer Smoke/i }).click()
  await openWidgetAction(page, 'Draft code', 'Edit widget')
  await expect(page.getByText('Code widget metadata')).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Widget TSX' })).toBeVisible()

  await page.getByRole('textbox', { name: 'Widget TSX' }).fill('this is not valid ::::: typescript')
  await page.getByRole('button', { name: 'Save widget' }).click()
  await expect(page.getByText(/esbuild:/i)).toBeVisible()
  await expect(page.getByText(/widget\.tsx:1:6/i)).toBeVisible()
  await expect(page.getByText('Compile failed')).toBeVisible()

  await page.getByRole('button', { name: 'Metric', exact: true }).click()
  await expect(page.getByText('Unsaved')).toBeVisible()
  await page.getByRole('button', { name: 'Save widget' }).click()
  await expect(page.getByText(/esbuild:/i)).toHaveCount(0)
  await expect(page.getByText('Compiled')).toBeVisible()
  await expect(page.frameLocator('iframe[title="widget"]').locator('body')).toContainText(/Total|Compiled OK/)
})

test('live dashboards open an editable snap-grid draft layout', async ({ page }) => {
  await page.goto('/web/dashboard-hydration.e2e.html')

  await page.getByRole('button', { name: /Hydration Smoke/i }).click()
  await page.getByRole('button', { name: 'Edit layout' }).click()

  await expect(page.getByText('Layout editing is enabled.')).toBeVisible()
  await expect(page.locator('.dashboard-grid-stack.grid-stack')).toBeVisible()
  await expect(page.locator('.dw-layout-drag-handle').first()).toBeVisible()
  await expect(page.getByRole('button', { name: 'Done' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Publish' })).toBeVisible()

  await page.getByRole('button', { name: 'Done' }).click()
  await expect(page.locator('.dashboard-grid-stack.grid-stack')).toHaveCount(0)
})

test('draft authoring shortcuts open source management and widget actions stay available from the widget menu', async ({ page }) => {
  await page.goto('/web/dashboard-hydration.e2e.html')

  await page.getByRole('button', { name: /Draft Customizer Smoke/i }).click()
  await page.getByRole('button', { name: 'Add widget' }).click()
  await expect(page.getByRole('heading', { name: 'Add Widget' })).toBeVisible()
  await page.locator('.dashboard-widget-inspector-modal').getByRole('button', { name: 'Sources', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Sources' })).toBeVisible()
  await page.getByRole('button', { name: 'Close' }).click()

  await page.getByRole('button', { name: 'Add widget' }).click()
  await page.locator('.dashboard-widget-inspector-modal .dashboard-list-card').filter({ hasText: 'AI Widget' }).click()
  await expect(page.getByRole('heading', { name: 'AI Widget' })).toBeVisible()
  await page.locator('.dashboard-widget-inspector-modal').getByRole('button', { name: 'Sources', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Sources' })).toBeVisible()
  await page.getByRole('button', { name: 'Close' }).click()

  await openWidgetMenu(page, 'Draft metric')
  await expect(page.getByRole('button', { name: 'Edit widget', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Delete widget', exact: true })).toBeVisible()
})

test('code widgets can request safe dashboard interactions', async ({ page }) => {
  await page.goto('/web/dashboard-hydration.e2e.html')

  await page.getByRole('button', { name: /Hydration Smoke/i }).click()
  const widgetFrame = page.frameLocator('iframe[title="widget"]')

  await widgetFrame.getByRole('button', { name: 'Filter status widgets' }).click()
  await expect(page.getByRole('button', { name: 'Source: status ×' })).toBeVisible()
  await expect(page.getByText('Memory rows')).toHaveCount(0)

  await widgetFrame.locator('summary').getByText('Open raw payload').click()
  await expect(page.getByRole('heading', { name: 'Code widget payload' })).toBeVisible()
  await expect(page.getByText('"activeConversationCount": 3')).toBeVisible()
  await page.getByRole('button', { name: 'Close' }).click()

  await widgetFrame.getByRole('button', { name: 'Refresh status' }).click()
  await expect(page.locator('.dw-gauge-value').getByText('4 chats', { exact: true })).toBeVisible()

  await widgetFrame.getByRole('button', { name: 'Open draft' }).click()
  await expect(page.getByText('Draft Customizer Smoke')).toBeVisible()
})
