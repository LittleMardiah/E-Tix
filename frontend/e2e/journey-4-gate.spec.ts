import { test, expect } from '@playwright/test';

test.describe('Journey 4: Gate Scanner', () => {
  test('Staff can scan QR and get valid result', async ({ page }) => {
    await page.goto('/gate/scanner');

    await page.getByLabel('Ticket ID').fill('dummy-qr-data');
    await page.getByRole('button', { name: 'Validate' }).click();

    await expect(page.getByText('VALID', { exact: true })).toBeVisible();
  });
});
