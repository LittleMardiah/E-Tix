import { test, expect } from '@playwright/test';

const TICKET_ID = '55555555-5555-5555-5555-555555555555';

test.describe('Journey 3: Resale Market', () => {
  test('User can list a ticket for resale', async ({ page }) => {
    await page.goto(`/customer/resale/list/${TICKET_ID}`);

    await page.getByLabel('Harga Resale (Rp)').fill('1050000');
    await page.getByRole('button', { name: 'Daftarkan' }).click();

    await expect(page).toHaveURL(/\/customer\/my-tickets$/);
  });
});
