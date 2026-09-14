import { afterEach, describe, expect, it } from "vitest";

import { generateUUID } from "./uuid";

const UUID_V4_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

describe("generateUUID", () => {
  const originalRandomUUID = crypto.randomUUID;

  afterEach(() => {
    crypto.randomUUID = originalRandomUUID;
  });

  it("returns a v4 UUID via crypto.randomUUID when it's available", () => {
    expect(generateUUID()).toMatch(UUID_V4_PATTERN);
  });

  it("falls back to crypto.getRandomValues when randomUUID is unavailable", () => {
    // Simulates an insecure context (§6.2's plain-HTTP LAN access), where
    // crypto.randomUUID doesn't exist at all.
    // @ts-expect-error deliberately removing it to exercise the fallback
    crypto.randomUUID = undefined;

    expect(generateUUID()).toMatch(UUID_V4_PATTERN);
  });

  it("produces unique values across repeated calls in the fallback path", () => {
    // @ts-expect-error deliberately removing it to exercise the fallback
    crypto.randomUUID = undefined;

    const ids = new Set(Array.from({ length: 50 }, () => generateUUID()));
    expect(ids.size).toBe(50);
  });
});
