import { test, expect } from '@playwright/test';

const EVENT_ID = '33333333-3333-3333-3333-333333333333';
const ZONES_JSON = JSON.stringify([
  {
    id: 'z1',
    name: 'VIP',
    price: 1000000,
    quota: 10,
    polygon_points: '500,500 1500,500 1500,900 500,900',
    pov_image_url: 'https://placehold.co/400',
    distance: '10m',
  },
]);

test.describe('Journey 6: EO Custom Map Layout', () => {
  test('EO can upload layout and see live preview', async ({ page }) => {
    await page.goto(`/eo/events/${EVENT_ID}/layout`);

    await page
      .locator('input')
      .nth(0)
      .fill('https://placehold.co/1920x1080/202020/FFFFFF?text=My+Custom+Map');
    await page.locator('input[type="number"]').nth(0).fill('1920');
    await page.locator('input[type="number"]').nth(1).fill('1080');
    await page.locator('select').selectOption('ZONED');
    await page.locator('textarea').fill(ZONES_JSON);

    await expect(page.locator('polygon')).toBeVisible();

    const dialogPromise = page.waitForEvent('dialog');
    await page.getByRole('button', { name: 'Simpan Layout' }).click();
    const dialog = await dialogPromise;
    expect(dialog.message()).toContain('berhasil');
    await dialog.accept();
  });
});
