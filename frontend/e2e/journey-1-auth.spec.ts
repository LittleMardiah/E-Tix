import { test, expect } from '@playwright/test';

// The dashboard each role must land on, keyed by pathname. This mirrors
// ROLE_HOME in app/auth/login/page.tsx, so the test fails if a role is sent
// to the wrong dashboard instead of merely "some dashboard".
const ROLE_DASHBOARD: Record<string, string> = {
  '/customer/dashboard': 'Explore Events',
  '/eo/dashboard': 'Dashboard EO',
  '/admin/dashboard': 'Dashboard Admin',
};

test.describe('Journey 1: Authentication', () => {
  test('User can login and land on the dashboard for their role', async ({
    page,
  }) => {
    await page.goto('/auth/login');

    await page.getByLabel('Email').fill('m.arif250602@gmail.com');
    await page.getByLabel('Password').fill('password123');
    await page.getByRole('button', { name: 'Login' }).click();

    await expect(page).toHaveURL(/\/(customer|eo|admin)\/dashboard$/);

    const pathname = new URL(page.url()).pathname;
    const heading = ROLE_DASHBOARD[pathname];
    expect(heading, `unexpected dashboard for path ${pathname}`).toBeTruthy();

    // Assert the dashboard really rendered for that role.
    await expect(
      page.getByRole('heading', { name: heading, level: 1 })
    ).toBeVisible();
  });
});
