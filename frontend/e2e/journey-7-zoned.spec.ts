import { test, expect } from '@playwright/test';

const EVENT_ID = '33333333-3333-3333-3333-333333333333';

test.describe('Journey 7: Zoned Checkout (General Admission)', () => {
  test('Customer can zoom the map, refresh quota, click a zone and buy', async ({
    page,
  }) => {
    await page.goto(`/customer/map/${EVENT_ID}`);

    await expect(page.getByAltText('Venue Map')).toBeVisible();

    // Bagian 3: manual zoom controls + manual quota refresh (no polling).
    await expect(
      page.getByRole('button', { name: 'Refresh Kuota' })
    ).toBeVisible();
    await page.getByRole('button', { name: 'Zoom in' }).click();
    await expect(page.getByText('125%')).toBeVisible();
    await page.getByRole('button', { name: 'Reset' }).click();

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
