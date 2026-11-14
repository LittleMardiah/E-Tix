import { test as setup, expect, type APIRequestContext, type Page } from '@playwright/test';

const USER_AUTH_FILE = 'e2e/.auth/user.json';
const ADMIN_AUTH_FILE = 'e2e/.auth/admin.json';
const GATE_AUTH_FILE = 'e2e/.auth/gate.json';
const BASE_URL = process.env.BASE_URL || 'http://localhost:3000';
const API_URL = process.env.API_URL || 'http://localhost:8080';
const PASSWORD = 'password123';

// Backend mengirim access_token di body.data; frontend membacanya dari
// localStorage 'access_token' (frontend/lib/api/client.ts:17,53).
// addInitScript jalan di setiap navigasi, jadi token sudah ada saat
// storageState() diambil setelah page.goto().
async function loginAndSave(
  page: Page,
  request: APIRequestContext,
  email: string,
  authFile: string
) {
  const res = await request.post(`${API_URL}/auth/login`, {
    data: { email, password: PASSWORD },
  });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  const token = body.data.access_token;
  expect(token).toBeTruthy();

  await page.addInitScript((t) => {
    window.localStorage.setItem('access_token', t);
  }, token);

  await page.goto(BASE_URL);
  await page.context().storageState({ path: authFile });
}

// Event organizer: journey 1, 2, 3, 6.
setup('authenticate as event organizer', async ({ page, request }) => {
  await loginAndSave(page, request, 'm.arif250602@gmail.com', USER_AUTH_FILE);
});

// Admin: journey 5. Needed because /admin/* is admin-only now (RBAC + UI guard).
setup('authenticate as admin', async ({ page, request }) => {
  await loginAndSave(page, request, 'admin@e-tix.local', ADMIN_AUTH_FILE);
});

// Gate staff: journey 4. /gate/* is RBAC gate_staff only.
setup('authenticate as gate staff', async ({ page, request }) => {
  await loginAndSave(page, request, 'gate@e-tix.local', GATE_AUTH_FILE);
});
