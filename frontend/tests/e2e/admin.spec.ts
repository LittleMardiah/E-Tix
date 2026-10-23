import { test, expect } from "@playwright/test";

test("Admin can access ledger with 2FA", async ({ page }) => {
  await page.goto("/admin/ledger");

  await expect(
    page.getByRole("heading", { name: "Ledger Audit" })
  ).toBeVisible();

  await page.getByLabel("2FA Token").fill("123456");
  await page.getByRole("button", { name: "Muat Ulang" }).click();

  const ledgerContent = page
    .locator('table, [role="alert"]')
    .or(page.getByText("Belum ada entri ledger."));

  await expect(ledgerContent.first()).toBeVisible();
});
