import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { clearDeviceToken, setDeviceToken } from "../../auth";
import i18n from "../../i18n";
import { SettingsScreen } from "./SettingsScreen";

// Same override DevicesScreen.test.tsx uses: jsdom's default test origin is
// itself "localhost", which §6.2 would trust implicitly — overridden here
// so the device-management half renders deterministically.
vi.mock("../../auth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../auth")>();
  return { ...actual, isLocalOrigin: () => false };
});

beforeEach(() => {
  clearDeviceToken();
  setDeviceToken("tok");
  localStorage.removeItem("argos.language");
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => [] }));
});

describe("SettingsScreen", () => {
  it("shows the language selector alongside device management", async () => {
    render(<SettingsScreen />);

    expect(screen.getByText("Language")).toBeInTheDocument();
    expect(screen.getByRole("combobox")).toHaveValue("en");
    expect(await screen.findByText("Devices")).toBeInTheDocument();
  });

  it("persists a manual language override when changed", async () => {
    const user = userEvent.setup();
    render(<SettingsScreen />);

    await user.selectOptions(screen.getByRole("combobox"), "en");

    expect(localStorage.getItem("argos.language")).toBe("en");
    expect(i18n.language).toBe("en");
  });
});
