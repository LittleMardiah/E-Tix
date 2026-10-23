import { test, expect } from '@playwright/test';

test('Customer can browse resale market', async ({ page }) => {
  await page.goto('/customer/resale/market/dummy-id');

  await expect(
    page.getByRole('heading', { name: 'Resale Market' })
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Muat Ulang' })).toBeVisible();
});
