import { beforeEach, describe, expect, it } from "vitest";

import { clearDeviceToken, getDeviceToken, setDeviceToken } from "./deviceToken";

beforeEach(() => {
  clearDeviceToken();
});

describe("deviceToken", () => {
  it("starts with no token", () => {
    expect(getDeviceToken()).toBeNull();
  });

  it("persists a set token to localStorage and reflects it immediately", () => {
    setDeviceToken("abc");

    expect(getDeviceToken()).toBe("abc");
    expect(localStorage.getItem("argos.device_token")).toBe("abc");
  });

  it("clears the token from both memory and localStorage", () => {
    setDeviceToken("abc");
    clearDeviceToken();

    expect(getDeviceToken()).toBeNull();
    expect(localStorage.getItem("argos.device_token")).toBeNull();
  });
});
