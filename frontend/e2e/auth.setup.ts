import { test as setup, expect } from '@playwright/test';

const authFile = 'e2e/.auth/user.json';

setup('authenticate', async ({ page, request }) => {
  const res = await request.post('http://localhost:8080/auth/login', {
    data: { email: 'm.arif250602@gmail.com', password: 'password123' },
  });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  const token = body.data.access_token;
  expect(token).toBeTruthy();

  // Backend mengirim access_token di body.data; frontend membacanya dari
  // localStorage 'access_token' (frontend/lib/api/client.ts:17,53).
  // addInitScript jalan di setiap navigasi, jadi token sudah ada saat
  // storageState() diambil setelah page.goto().
  await page.addInitScript((t) => {
    window.localStorage.setItem('access_token', t);
  }, token);

  await page.goto('http://localhost:3000');
  await page.context().storageState({ path: authFile });
});
