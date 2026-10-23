import { test, expect } from '@playwright/test';

test('Customer can open ticket detail page', async ({ page }) => {
  await page.goto('/customer/tickets/dummy-id');

  await expect(
    page.getByRole('link', { name: /Tiket Saya/ })
  ).toBeVisible();
  await expect(page.locator('body')).toBeVisible();
});
