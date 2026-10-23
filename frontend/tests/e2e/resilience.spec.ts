import { test, expect } from "@playwright/test";

test("Health check endpoint is alive", async ({ request }) => {
  const backendUrl =
    process.env.API_BASE_URL ||
    process.env.NEXT_PUBLIC_API_URL ||
    "http://localhost:8080";

  const res = await request.get(`${backendUrl}/healthz/liveness`);

  expect(res.status()).toBe(200);
  await expect(res).toBeOK();
  expect(await res.json()).toMatchObject({ status: "alive" });
});
