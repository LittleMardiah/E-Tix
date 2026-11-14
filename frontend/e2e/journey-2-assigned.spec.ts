import { test, expect } from '@playwright/test';

const EVENT_ID = '33333333-3333-3333-3333-333333333333';
const SECTION_ID = '44444444-4444-4444-4444-444444444444';
const QUEUE_TOKEN_KEY = `e-tix-flash:queue-token:${EVENT_ID}`;
const API_URL = process.env.API_URL || 'http://localhost:8080';

test.describe('Journey 2: Assigned Seating (Flash Sale)', () => {
  test('Customer can select seat, choose split pay, and checkout', async ({ page }) => {
    // A fake token is rejected by LockSeats (it verifies the signed queue JWT),
    // so fetch a real one from GET /queue/status using the stored access token.
    await page.goto(`/customer/seat-map/${SECTION_ID}?eventId=${EVENT_ID}`);
    const queueToken = await page.evaluate(
      async ({ api, eventId }) => {
        const accessToken = window.localStorage.getItem('access_token');
        const response = await fetch(
          `${api}/queue/status?event_id=${eventId}`,
          { headers: { Authorization: `Bearer ${accessToken}` } }
        );
        const body = await response.json();
        return body?.data?.queue_token as string | undefined;
      },
      { api: API_URL, eventId: EVENT_ID }
    );
    expect(queueToken).toBeTruthy();

    await page.evaluate(
      (value: { key: string; token: string }) => {
        window.sessionStorage.setItem(value.key, value.token);
      },
      { key: QUEUE_TOKEN_KEY, token: queueToken as string }
    );
    await page.reload();

    // Seats are labelled by lib/format.ts seatLabel(): row A = A1..A12.
    // A1 is already CONFIRMED in the CI seed, so target the first AVAILABLE
    // seat of row A instead of hard-coding A1 (which would be flaky).
    const availableSeat = page.getByRole('button', {
      name: /^Kursi A\d+ - AVAILABLE$/,
    });
    await expect(availableSeat.first()).toBeVisible();
    await availableSeat.first().click();
    await page.getByRole('button', { name: /Split Pay/ }).click();

    await expect(page.getByText(/Terpilih:/)).toContainText(/A\d+/);

    await page.getByRole('button', { name: /Kunci .* Kursi/ }).click();

    await expect(page).toHaveURL(/\/customer\/payment\//);
  });
});
