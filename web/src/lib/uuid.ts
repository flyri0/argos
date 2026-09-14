// crypto.randomUUID() is restricted to secure contexts (HTTPS, or the
// browser's own localhost/127.0.0.1 exception) — it throws in any other
// context, including exactly the plain-HTTP LAN address (e.g.
// http://192.168.1.20:8080) that §6.2 explicitly means for Argos to be
// reachable at from another device on the network. crypto.getRandomValues()
// carries no such restriction, so build a standard v4 UUID from it by hand
// whenever randomUUID isn't available, rather than every call site crashing
// the instant it's reached over LAN.
export function generateUUID(): string {
  if (typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }

  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
  bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10

  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
