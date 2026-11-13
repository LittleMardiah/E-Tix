import { test, expect } from '@playwright/test';

test.describe('Journey 1: Authentication', () => {
  test('User can login and land on dashboard', async ({ page }) => {
    await page.goto('/auth/login');

    await page.getByLabel('Email').fill('m.arif250602@gmail.com');
    await page.getByLabel('Password').fill('password123');
    await page.getByRole('button', { name: 'Login' }).click();

    await expect(page).toHaveURL(/\/customer\/dashboard|\/eo\/dashboard/);
    await expect(page.getByText(/Halo,/)).toBeVisible();
  });
});
