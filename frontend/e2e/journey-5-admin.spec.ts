import { test, expect } from '@playwright/test';

const ADMIN_2FA_TOKEN = process.env.ADMIN_2FA_TOKEN ?? 'dummy-2fa-token';

// /admin/* is admin-only (backend RBAC + admin layout guard), so this spec
// must not inherit the organizer session used by the other journeys.
test.use({ storageState: 'e2e/.auth/admin.json' });

test.describe('Journey 5: Admin Tools', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript((token: string) => {
      window.sessionStorage.setItem('e-tix-flash:admin-2fa-token', token);
    }, ADMIN_2FA_TOKEN);
  });

  test('Admin can page the ledger and suspend user', async ({ page }) => {
    await page.goto('/admin/ledger');
    await expect(
      page.getByRole('heading', { name: 'Ledger Audit' })
    ).toBeVisible();
    await expect(page.locator('table')).toBeVisible();

    // Bagian 6: pagination controls are always rendered so the admin can
    // navigate even when the current page is empty.
    await expect(
      page.getByRole('button', { name: 'Sebelumnya' })
    ).toBeDisabled();
    await expect(
      page.getByRole('button', { name: 'Berikutnya' })
    ).toBeVisible();
    await expect(page.getByText('Halaman 1')).toBeVisible();

    await page.goto('/admin/users');
    await expect(
      page.getByRole('heading', { name: 'User Management' })
    ).toBeVisible();

    // The suspend action is guarded by admin 2FA; the TEST_MODE bypass in
    // backend/internal/admin/middleware.go accepts the dummy token installed
    // by beforeEach, so the click must actually change the badge.
    await expect(page.getByLabel('Suspend Reason')).toBeVisible();
    await page
      .getByRole('button', { name: 'Suspend', exact: true })
      .first()
      .click();

    await expect(page.getByText('SUSPENDED').first()).toBeVisible();
  });
});
