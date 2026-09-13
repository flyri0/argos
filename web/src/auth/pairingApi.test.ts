import { beforeEach, describe, expect, it, vi } from "vitest";

import { clearDeviceToken, setDeviceToken } from "./deviceToken";
import {
  approvePairing,
  bootstrap,
  listDevices,
  PairingApiError,
  pollPairing,
  renameDevice,
  requestPairing,
  revokeDevice,
} from "./pairingApi";

beforeEach(() => {
  clearDeviceToken();
  vi.stubGlobal("fetch", vi.fn());
});

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "",
    json: async () => body,
  } as Response;
}

describe("bootstrap", () => {
  it("posts the code and returns the minted token", async () => {
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(201, { id: "d1", name: "First device", token: "tok", approved_at: 1 }),
    );

    const result = await bootstrap("abc123");

    expect(fetch).toHaveBeenCalledWith(
      "/api/pairing/bootstrap",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ code: "abc123" }),
      }),
    );
    expect(result.token).toBe("tok");
  });

  it("throws a PairingApiError carrying the server's error code", async () => {
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(410, { error: { code: "BOOTSTRAP_CLOSED", message: "closed" } }),
    );

    await expect(bootstrap("wrong")).rejects.toMatchObject({
      status: 410,
      code: "BOOTSTRAP_CLOSED",
    });
  });
});

describe("requestPairing / pollPairing", () => {
  it("requests a code with no body", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(201, { code: "4821", expires_at: 123 }));

    const result = await requestPairing();

    expect(fetch).toHaveBeenCalledWith("/api/pairing/request", { method: "POST" });
    expect(result.code).toBe("4821");
  });

  it("polls the code and reports pending vs approved", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(200, { status: "pending" }));

    const result = await pollPairing("4821");

    expect(fetch).toHaveBeenCalledWith("/api/pairing/request/4821");
    expect(result.status).toBe("pending");
  });

  it("surfaces a 404 poll as a PairingApiError with status 404", async () => {
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(404, { error: { code: "PAIRING_CODE_NOT_FOUND", message: "gone" } }),
    );

    await expect(pollPairing("0000")).rejects.toBeInstanceOf(PairingApiError);
    await expect(pollPairing("0000")).rejects.toMatchObject({ status: 404 });
  });
});

describe("authenticated device endpoints", () => {
  it("attaches the stored token as a bearer credential", async () => {
    setDeviceToken("secret-token");
    vi.mocked(fetch).mockResolvedValue(jsonResponse(200, []));

    await listDevices();

    const [, init] = vi.mocked(fetch).mock.calls[0];
    const headers = new Headers((init as RequestInit).headers);
    expect(headers.get("Authorization")).toBe("Bearer secret-token");
  });

  it("omits the Authorization header when there's no stored token", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(200, []));

    await listDevices();

    const [, init] = vi.mocked(fetch).mock.calls[0];
    const headers = new Headers((init as RequestInit)?.headers);
    expect(headers.has("Authorization")).toBe(false);
  });

  it("renames a device via PATCH", async () => {
    setDeviceToken("tok");
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(200, { id: "d1", name: "Kitchen tablet", approved_at: 1, last_seen_at: null, revoked_at: null }),
    );

    const result = await renameDevice("d1", "Kitchen tablet");

    expect(fetch).toHaveBeenCalledWith(
      "/api/devices/d1",
      expect.objectContaining({ method: "PATCH", body: JSON.stringify({ name: "Kitchen tablet" }) }),
    );
    expect(result.name).toBe("Kitchen tablet");
  });

  it("revokes a device via DELETE and resolves on a 204 with no body", async () => {
    setDeviceToken("tok");
    vi.mocked(fetch).mockResolvedValue({ ok: true, status: 204, statusText: "" } as Response);

    await expect(revokeDevice("d1")).resolves.toBeUndefined();
    expect(fetch).toHaveBeenCalledWith("/api/devices/d1", expect.objectContaining({ method: "DELETE" }));
  });

  it("approves a pending code", async () => {
    setDeviceToken("tok");
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(201, { id: "d2", name: "Unnamed device", token: "other-tok", approved_at: 1 }),
    );

    const result = await approvePairing("4821");

    expect(fetch).toHaveBeenCalledWith(
      "/api/pairing/approve",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ code: "4821" }) }),
    );
    expect(result.name).toBe("Unnamed device");
  });
});
