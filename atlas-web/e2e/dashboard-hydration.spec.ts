import { expect, test } from '@playwright/test'

test('dashboard hydration shows loading first, then success and failure states', async ({ page }) => {
  await page.goto('/web/dashboard-hydration.e2e.html')

  await page.getByRole('button', { name: /Hydration Smoke/i }).click()

  await expect(page.getByText('Orlando Weather')).toBeVisible()
  await expect(page.getByText('AI News')).toBeVisible()
  await expect(page.getByText('Loading source…')).toHaveCount(2)
  await expect(page.locator('.dashboard-widget-health.loading')).toHaveCount(2)

  await expect(page.getByText('Sunny, 78F, light breeze.')).toBeVisible()
  await expect(page.locator('.dashboard-widget-health.ok')).toHaveText('OK')

  await expect(page.getByText(/returned 401/i)).toBeVisible()
  await expect(page.locator('.dashboard-widget-health.error')).toHaveText('Failed')
})
