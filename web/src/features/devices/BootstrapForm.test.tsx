import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { clearDeviceToken, getDeviceToken } from "../../auth";
import { BootstrapForm } from "./BootstrapForm";

beforeEach(() => {
  clearDeviceToken();
  vi.stubGlobal("fetch", vi.fn());
});

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
}

describe("BootstrapForm", () => {
  it("rejects submitting an empty code without calling the server", async () => {
    const user = userEvent.setup();
    render(<BootstrapForm onSwitchToWaiting={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Pair this device" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Enter the setup code.");
    expect(fetch).not.toHaveBeenCalled();
  });

  it("stores the returned token on a successful bootstrap", async () => {
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(201, { id: "d1", name: "First device", token: "tok-1", approved_at: 1 }),
    );
    const user = userEvent.setup();
    render(<BootstrapForm onSwitchToWaiting={() => {}} />);

    await user.type(screen.getByLabelText("Setup code"), "abc123");
    await user.click(screen.getByRole("button", { name: "Pair this device" }));

    await vi.waitFor(() => expect(getDeviceToken()).toBe("tok-1"));
  });

  it("shows a specific message when bootstrap is already closed", async () => {
    vi.mocked(fetch).mockResolvedValue(
      jsonResponse(410, { error: { code: "BOOTSTRAP_CLOSED", message: "closed" } }),
    );
    const user = userEvent.setup();
    render(<BootstrapForm onSwitchToWaiting={() => {}} />);

    await user.type(screen.getByLabelText("Setup code"), "abc123");
    await user.click(screen.getByRole("button", { name: "Pair this device" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Setup is already complete on this server. Request access from an already-paired device instead.",
    );
    expect(getDeviceToken()).toBeNull();
  });

  it("switches to the waiting flow", async () => {
    const onSwitch = vi.fn();
    const user = userEvent.setup();
    render(<BootstrapForm onSwitchToWaiting={onSwitch} />);

    await user.click(
      screen.getByRole("button", { name: "This isn't the first device — request access instead" }),
    );

    expect(onSwitch).toHaveBeenCalled();
  });
});
