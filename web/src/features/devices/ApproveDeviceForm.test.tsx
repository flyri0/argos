import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApproveDeviceForm } from "./ApproveDeviceForm";

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn());
});

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
}

describe("ApproveDeviceForm", () => {
  it("rejects an empty code without calling the server", async () => {
    const user = userEvent.setup();
    render(<ApproveDeviceForm onApproved={() => {}} onCancel={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Enter the pairing code.");
    expect(fetch).not.toHaveBeenCalled();
  });

  it("calls onApproved after a successful approval", async () => {
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(201, { id: "d2", name: "Unnamed device", token: "tok", approved_at: 1 }),
    );
    const onApproved = vi.fn();
    const user = userEvent.setup();
    render(<ApproveDeviceForm onApproved={onApproved} onCancel={() => {}} />);

    await user.type(screen.getByLabelText("Pairing code"), "4821");
    await user.click(screen.getByRole("button", { name: "Approve" }));

    await vi.waitFor(() => expect(onApproved).toHaveBeenCalled());
    expect(fetch).toHaveBeenCalledWith(
      "/api/pairing/approve",
      expect.objectContaining({ body: JSON.stringify({ code: "4821" }) }),
    );
  });

  it("shows an invalid-code message on a rejected code", async () => {
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(429, { error: { code: "PAIRING_RATE_LIMITED", message: "slow down" } }),
    );
    const user = userEvent.setup();
    render(<ApproveDeviceForm onApproved={() => {}} onCancel={() => {}} />);

    await user.type(screen.getByLabelText("Pairing code"), "0000");
    await user.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Too many attempts. Wait a moment and try again.",
    );
  });
});
