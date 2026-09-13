import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { clearDeviceToken, setDeviceToken } from "../../auth";
import { DevicesScreen } from "./DevicesScreen";

// jsdom's default test origin is itself "localhost" (§6.2 would trust it
// implicitly for real), so the no-token/untrusted case is only exercisable
// by overriding isLocalOrigin for that one test — everything else in the
// module stays real.
vi.mock("../../auth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../auth")>();
  return { ...actual, isLocalOrigin: () => false };
});

beforeEach(() => {
  clearDeviceToken();
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => [] }));
});

describe("DevicesScreen", () => {
  it("shows the pairing flow when this device has no token and isn't on localhost", async () => {
    render(<DevicesScreen />);

    expect(await screen.findByText("Pair this device")).toBeInTheDocument();
  });

  it("shows device management once this device has a token", async () => {
    setDeviceToken("tok");

    render(<DevicesScreen />);

    expect(await screen.findByText("Devices")).toBeInTheDocument();
  });
});
