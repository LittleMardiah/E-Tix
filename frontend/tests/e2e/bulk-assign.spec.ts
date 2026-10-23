import { test, expect } from '@playwright/test';

test('Customer can view bulk assign form', async ({ page }) => {
  await page.goto('/customer/bulk-assign/dummy-id');

  await expect(
    page.getByRole('heading', { name: 'Bulk Ticket Assignment' })
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Assign Tiket' })
  ).toBeVisible();
  await expect(page.getByLabel('Ticket ID (UUID)')).toBeVisible();
  await expect(page.getByLabel('User ID Anggota (UUID)')).toBeVisible();
});
