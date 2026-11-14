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

// 1x1 transparent PNG: enough to exercise the multipart upload endpoint.
const PNG_BUFFER = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64'
);

test.describe('Journey 6: EO Custom Map Layout', () => {
  test('EO can upload layout and see live preview', async ({ page }) => {
    await page.goto(`/eo/events/${EVENT_ID}/layout`);

    // V10: the URL field was replaced by a real upload to
    // POST /eo/events/:id/upload-map.
    await page.locator('input[type="file"]').setInputFiles({
      name: 'map.png',
      mimeType: 'image/png',
      buffer: PNG_BUFFER,
    });
    await expect(page.getByText('/uploads/maps/', { exact: false })).toBeVisible();

    await page.locator('input[type="number"]').nth(0).fill('1920');
    await page.locator('input[type="number"]').nth(1).fill('1080');
    await page.locator('select').selectOption('ZONED');
    await page.locator('textarea').fill(ZONES_JSON);

    await expect(page.locator('polygon')).toBeVisible();

    // Bagian 2: saving reports through an in-page toast, never window.alert.
    await page.getByRole('button', { name: 'Simpan Layout' }).click();
    await expect(page.getByText('Layout berhasil disimpan!')).toBeVisible();
  });
});
