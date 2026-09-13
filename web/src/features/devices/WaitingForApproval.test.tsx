import { render, screen } from "@testing-library/react";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { clearDeviceToken, getDeviceToken } from "../../auth";
import { WaitingForApproval } from "./WaitingForApproval";

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
}

beforeEach(() => {
  clearDeviceToken();
  vi.stubGlobal("fetch", vi.fn());
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("WaitingForApproval", () => {
  it("requests a code on mount and displays it", async () => {
    vi.mocked(fetch).mockResolvedValue(jsonResponse(201, { code: "4821", expires_at: Date.now() / 1000 + 600 }));

    render(<WaitingForApproval onSwitchToBootstrap={() => {}} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(screen.getByText("4821")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith("/api/pairing/request", { method: "POST" });
  });

  it("stores the token once a poll reports approved", async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(201, { code: "4821", expires_at: Date.now() / 1000 + 600 }))
      .mockResolvedValueOnce(jsonResponse(200, { status: "pending" }))
      .mockResolvedValueOnce(jsonResponse(200, { status: "approved", token: "won-token" }));

    render(<WaitingForApproval onSwitchToBootstrap={() => {}} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0); // resolve the initial request
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000); // first poll: pending
    });
    expect(getDeviceToken()).toBeNull();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000); // second poll: approved
    });
    expect(getDeviceToken()).toBe("won-token");
  });

  it("offers a new code once the poll reports the code has expired", async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(201, { code: "4821", expires_at: Date.now() / 1000 + 600 }))
      .mockResolvedValueOnce(
        jsonResponse(404, { error: { code: "PAIRING_CODE_NOT_FOUND", message: "gone" } }),
      );

    render(<WaitingForApproval onSwitchToBootstrap={() => {}} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });

    expect(screen.getByRole("alert")).toHaveTextContent("This code has expired.");

    vi.mocked(fetch).mockResolvedValueOnce(
      jsonResponse(201, { code: "9999", expires_at: Date.now() / 1000 + 600 }),
    );
    await act(async () => {
      screen.getByRole("button", { name: "Request a new code" }).click();
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(screen.getByText("9999")).toBeInTheDocument();
  });
});
