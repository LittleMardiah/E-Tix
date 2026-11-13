import { test, expect } from '@playwright/test';

const EVENT_ID = '33333333-3333-3333-3333-333333333333';
const SECTION_ID = '44444444-4444-4444-4444-444444444444';
const QUEUE_TOKEN_KEY = `e-tix-flash:queue-token:${EVENT_ID}`;

test.describe('Journey 2: Assigned Seating (Flash Sale)', () => {
  test('Customer can select seat, choose split pay, and checkout', async ({ page }) => {
    await page.addInitScript(
      (value: { key: string; token: string }) => {
        window.sessionStorage.setItem(value.key, value.token);
      },
      { key: QUEUE_TOKEN_KEY, token: 'e2e-queue-token' }
    );

    await page.goto(`/customer/seat-map/${SECTION_ID}?eventId=${EVENT_ID}`);

    await page
      .getByRole('button', { name: /Kursi .* - AVAILABLE/ })
      .first()
      .click();
    await page.getByRole('button', { name: /Split Pay/ }).click();

    await expect(page.getByText(/Terpilih:/)).toContainText('1');

    await page.getByRole('button', { name: /Kunci .* Kursi/ }).click();

    await expect(page).toHaveURL(/\/customer\/payment\//);
  });
});
