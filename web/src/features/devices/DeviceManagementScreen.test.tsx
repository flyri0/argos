import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { setDeviceToken } from "../../auth";
import { DeviceManagementScreen } from "./DeviceManagementScreen";

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
}

const deviceA = { id: "d1", name: "Kitchen tablet", approved_at: 1, last_seen_at: 1_700_000_000, revoked_at: null };
const deviceB = { id: "d2", name: "Phone", approved_at: 2, last_seen_at: null, revoked_at: null };

beforeEach(() => {
  setDeviceToken("tok");
  vi.stubGlobal("fetch", vi.fn());
});

describe("DeviceManagementScreen", () => {
  it("shows the empty state when there are no devices", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(200, []));

    render(<DeviceManagementScreen />);

    expect(await screen.findByText("No devices paired yet.")).toBeInTheDocument();
  });

  it("lists paired devices with their last-seen time", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(200, [deviceA, deviceB]));

    render(<DeviceManagementScreen />);

    expect(await screen.findByText("Kitchen tablet")).toBeInTheDocument();
    expect(screen.getByText("Phone")).toBeInTheDocument();
    expect(screen.getByText("Never")).toBeInTheDocument();
  });

  it("renames a device and refreshes the list", async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(200, [deviceA]))
      .mockResolvedValueOnce(jsonResponse(200, { ...deviceA, name: "Living room tablet" }))
      .mockResolvedValueOnce(jsonResponse(200, [{ ...deviceA, name: "Living room tablet" }]));

    const user = userEvent.setup();
    render(<DeviceManagementScreen />);
    await screen.findByText("Kitchen tablet");

    await user.click(screen.getByRole("button", { name: "Rename" }));
    const input = screen.getByLabelText("Name");
    await user.clear(input);
    await user.type(input, "Living room tablet");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Living room tablet")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith(
      "/api/devices/d1",
      expect.objectContaining({ method: "PATCH", body: JSON.stringify({ name: "Living room tablet" }) }),
    );
  });

  it("revokes a device after confirming", async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(200, [deviceA]))
      .mockResolvedValueOnce({ ok: true, status: 204, statusText: "" } as Response)
      .mockResolvedValueOnce(jsonResponse(200, [{ ...deviceA, revoked_at: 1_700_000_100 }]));

    const user = userEvent.setup();
    render(<DeviceManagementScreen />);
    await screen.findByText("Kitchen tablet");

    await user.click(screen.getByRole("button", { name: "Revoke" }));
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    const row = (await screen.findByText("Kitchen tablet (Revoked)")).closest("tr")!;
    expect(within(row).queryByRole("button", { name: "Revoke" })).not.toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith("/api/devices/d1", expect.objectContaining({ method: "DELETE" }));
  });

  it("approving a new device refreshes the list", async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(200, [deviceA]))
      .mockResolvedValueOnce(
        jsonResponse(201, { id: "d3", name: "Unnamed device", token: "t3", approved_at: 3 }),
      )
      .mockResolvedValueOnce(
        jsonResponse(200, [deviceA, { id: "d3", name: "Unnamed device", approved_at: 3, last_seen_at: null, revoked_at: null }]),
      );

    const user = userEvent.setup();
    render(<DeviceManagementScreen />);
    await screen.findByText("Kitchen tablet");

    await user.click(screen.getByRole("button", { name: "Approve a device" }));
    await user.type(screen.getByLabelText("Pairing code"), "4821");
    await user.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByText("Unnamed device")).toBeInTheDocument();
  });
});
