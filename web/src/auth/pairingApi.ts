import { getDeviceToken } from "./deviceToken";

// Thin wrappers around the §6.1/§6.2/§7.3 pairing and device-management
// endpoints. Every error response follows §7.1's envelope
// ({"error":{"code","message"}}); PairingApiError surfaces the machine
// code and HTTP status so callers can map it to a localized message (§4)
// rather than showing the server's English text.
export class PairingApiError extends Error {
  status: number;
  code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function parseJson<T>(response: Response): Promise<T> {
  if (!response.ok) {
    let code = "UNKNOWN_ERROR";
    let message = response.statusText;
    try {
      const body = await response.json();
      if (body?.error?.code) code = body.error.code;
      if (body?.error?.message) message = body.error.message;
    } catch {
      // Non-JSON error body (e.g. a network-level failure page) — fall
      // back to the generic code/message set above.
    }
    throw new PairingApiError(response.status, code, message);
  }
  return response.json() as Promise<T>;
}

// Attaches this device's token (if it has one) as a bearer credential.
// Safe to call before pairing too: a localhost caller doesn't need a
// token at all (§6.2), and the server simply ignores the header's absence.
function authFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const token = getDeviceToken();
  const headers = new Headers(init.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  return fetch(path, { ...init, headers });
}

export interface BootstrapResult {
  id: string;
  name: string;
  token: string;
  approved_at: number;
}

// POST /api/pairing/bootstrap (§6.1, §7.3).
export async function bootstrap(code: string): Promise<BootstrapResult> {
  const response = await fetch("/api/pairing/bootstrap", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  return parseJson<BootstrapResult>(response);
}

export interface RequestPairingResult {
  code: string;
  expires_at: number;
}

// POST /api/pairing/request (§6.2, §7.3).
export async function requestPairing(): Promise<RequestPairingResult> {
  const response = await fetch("/api/pairing/request", { method: "POST" });
  return parseJson<RequestPairingResult>(response);
}

export interface PollPairingResult {
  status: "pending" | "approved";
  token?: string;
}

// GET /api/pairing/request/:code (§6.2, §7.3): the waiting device's side of
// the approval loop.
export async function pollPairing(code: string): Promise<PollPairingResult> {
  const response = await fetch(`/api/pairing/request/${encodeURIComponent(code)}`);
  return parseJson<PollPairingResult>(response);
}

// POST /api/pairing/approve (§6.2, §7.3): called by an already-trusted
// device (or localhost) to confirm another device's pairing code.
export async function approvePairing(code: string): Promise<BootstrapResult> {
  const response = await authFetch("/api/pairing/approve", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  return parseJson<BootstrapResult>(response);
}

export interface DeviceRecord {
  id: string;
  name: string;
  approved_at: number;
  last_seen_at: number | null;
  revoked_at: number | null;
}

// GET /api/devices (§6.2, §7.3).
export async function listDevices(): Promise<DeviceRecord[]> {
  const response = await authFetch("/api/devices");
  return parseJson<DeviceRecord[]>(response);
}

// PATCH /api/devices/:id (§6.2, §7.3).
export async function renameDevice(id: string, name: string): Promise<DeviceRecord> {
  const response = await authFetch(`/api/devices/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  return parseJson<DeviceRecord>(response);
}

// DELETE /api/devices/:id (§6.2, §7.3) — a 204 with no body, so this
// doesn't go through parseJson's success path.
export async function revokeDevice(id: string): Promise<void> {
  const response = await authFetch(`/api/devices/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
  if (response.ok) return;
  await parseJson(response); // always throws for a non-ok response
}
