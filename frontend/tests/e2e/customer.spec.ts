import { test, expect } from "@playwright/test";

test("Customer can view events and seats", async ({ page }) => {
  await page.goto("/customer/dashboard");

  await expect(
    page.getByRole("heading", { name: "Upcoming Events" })
  ).toBeVisible();

  const firstEvent = page.locator('a[href^="/customer/events/"]').first();
  if ((await firstEvent.count()) > 0) {
    await firstEvent.click();
    await expect(page).toHaveURL(/\/customer\/events\/.+/);
    await expect(page.getByRole("heading").first()).toBeVisible();
  }
});
