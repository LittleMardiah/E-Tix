import { test, expect } from '@playwright/test';

test('EO can view attendance report', async ({ page }) => {
  await page.goto('/eo/attendance/dummy-id');

  await expect(
    page.getByRole('heading', { name: 'Attendance Report' })
  ).toBeVisible();
  await expect(page.getByRole('link', { name: 'Analytics' })).toBeVisible();
});
