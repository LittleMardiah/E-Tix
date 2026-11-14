import { createHmac } from 'node:crypto';
import { test, expect } from '@playwright/test';

// Fixed IDs from backend/tests/fixtures/seed_ci.sql.
const TICKET_ID = '99999999-9999-9999-9999-999999999999';
const OWNER_ID = '11111111-1111-1111-1111-111111111111';
const QR_SECRET = 'e2e-qr-secret-99999999-9999-9999-9999-999999999999';
const QR_BLOCK_SECONDS = 30;

/**
 * Mirrors ticket.GenerateQRData (backend/internal/ticket/qr_generator.go):
 *   payload   = ticketID|ownerID|unix/30
 *   signature = base64url(HMAC-SHA256(secret, payload))
 *   QR        = base64url(ticketID|signature)
 * Go's base64.URLEncoding keeps the padding, so Node's base64 is converted to
 * URL-safe characters instead of using base64url (which strips '=').
 */
function buildQR(): string {
  const urlSafe = (value: string) =>
    Buffer.from(value)
      .toString('base64')
      .replace(/\+/g, '-')
      .replace(/\//g, '_');
  const block = Math.floor(Date.now() / 1000 / QR_BLOCK_SECONDS);
  const signature = createHmac('sha256', QR_SECRET)
    .update(`${TICKET_ID}|${OWNER_ID}|${block}`)
    .digest('base64')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return urlSafe(`${TICKET_ID}|${signature}`);
}

// /gate/* is gate_staff-only (RBAC), so this journey cannot reuse the
// organizer session used by the other specs.
test.use({ storageState: 'e2e/.auth/gate.json' });

test.describe('Journey 4: Gate Scanner', () => {
  test('Staff can scan QR and get valid result', async ({ page }) => {
    await page.goto('/gate/scanner');

    await page.getByLabel('Ticket ID').fill(TICKET_ID);
    await page.getByLabel('QR Data (payload)').fill(buildQR());
    await page.getByRole('button', { name: 'Validate' }).click();

    await expect(page.getByText('VALID', { exact: true })).toBeVisible();
  });
});
