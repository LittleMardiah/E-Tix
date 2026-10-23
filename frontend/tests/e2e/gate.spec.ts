import { test, expect } from "@playwright/test";

test("Gate staff can switch scan modes and open offline sync", async ({
  page,
}) => {
  await page.goto("/gate/scanner");

  await expect(
    page.getByRole("heading", { name: "Gate Scanner" })
  ).toBeVisible();
  await expect(page.getByLabel("Ticket ID")).toBeVisible();

  await page.getByRole("button", { name: "PIN Entry" }).click();
  await expect(page.getByLabel("Fallback PIN (12 characters)")).toBeVisible();

  await page.getByRole("button", { name: "QR Scan" }).click();
  await page.getByRole("button", { name: "Offline Sync" }).click();

  await expect(page).toHaveURL(/\/gate\/sync$/);
  await expect(
    page.getByRole("heading", { name: "Offline Sync" })
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Sync Now" })).toBeVisible();
});
