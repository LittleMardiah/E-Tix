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

    // Zoned checkout (/bookings/zoned) is BULK + PAID and has NO invoice or
    // "Simulate Payment Success" step: the UI redirects to My Tickets and the
    // ticket itself is generated asynchronously by the zoned batch worker.
    await expect(page).toHaveURL(/\/customer\/my-tickets/);
    await expect(
      page.getByRole('heading', { name: 'My Tickets', level: 1 })
    ).toBeVisible();
    await expect(page.getByText('VIP', { exact: true }).first()).toBeVisible({
      timeout: 20_000,
    });
    await expect(
      page.getByRole('heading', { name: 'Test Concert' }).first()
    ).toBeVisible();
  });
});
