import { test, expect } from "@playwright/test";

test("User can register and verify OTP", async ({ page }) => {
  await page.goto("/auth/register");

  await page.getByLabel("Full Name").fill("Test User");
  await page.getByLabel("Email").fill(`test${Date.now()}@example.com`);
  await page.getByLabel("Phone").fill("+628123456789");
  await page.getByLabel("Password").fill("Password123!");

  await page.getByRole("button", { name: "Sign Up" }).click();

  await expect(page).toHaveURL(/\/auth\/verify-otp$/);
  await expect(
    page.getByRole("heading", { name: "Verify Your Email" })
  ).toBeVisible();
});
