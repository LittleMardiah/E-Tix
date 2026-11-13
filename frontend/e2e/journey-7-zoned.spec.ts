import { test, expect } from '@playwright/test';

const EVENT_ID = '33333333-3333-3333-3333-333333333333';

test.describe('Journey 7: Zoned Checkout (General Admission)', () => {
  test('Customer can click zone and buy ticket', async ({ page }) => {
    await page.goto(`/customer/map/${EVENT_ID}`);

    await expect(page.getByAltText('Venue Map')).toBeVisible();
    await page.locator('polygon').first().click();

    await expect(page.getByText('VIP', { exact: true }).first()).toBeVisible();
    await expect(
      page.getByText('Rp 1.000.000', { exact: true }).first()
    ).toBeVisible();

    const buyButton = page.getByRole('button', {
      name: 'Beli Tiket Zona Ini',
    });
    await expect(buyButton).toBeEnabled();
    await buyButton.click();
  });
});
