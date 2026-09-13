import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./engine", () => ({ runSync: vi.fn() }));

import { runSync } from "./engine";
import { startSyncWorker } from "./worker";

let stop: (() => void) | undefined;

beforeEach(() => {
  vi.mocked(runSync).mockClear();
  Object.defineProperty(document, "visibilityState", {
    value: "visible",
    configurable: true,
  });
});

afterEach(() => {
  stop?.();
  stop = undefined;
  vi.useRealTimers();
});

describe("startSyncWorker", () => {
  it("syncs immediately when already online", () => {
    Object.defineProperty(navigator, "onLine", { value: true, configurable: true });

    stop = startSyncWorker();

    expect(runSync).toHaveBeenCalledTimes(1);
  });

  it("does not sync immediately when offline, but does on the next online event", () => {
    Object.defineProperty(navigator, "onLine", { value: false, configurable: true });

    stop = startSyncWorker();
    expect(runSync).not.toHaveBeenCalled();

    window.dispatchEvent(new Event("online"));
    expect(runSync).toHaveBeenCalledTimes(1);
  });

  it("polls periodically while online, without needing a page reload", () => {
    vi.useFakeTimers();
    Object.defineProperty(navigator, "onLine", { value: true, configurable: true });

    stop = startSyncWorker();
    expect(runSync).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(10_000);
    expect(runSync).toHaveBeenCalledTimes(2);

    vi.advanceTimersByTime(10_000);
    expect(runSync).toHaveBeenCalledTimes(3);
  });

  it("does not poll while offline", () => {
    vi.useFakeTimers();
    Object.defineProperty(navigator, "onLine", { value: false, configurable: true });

    stop = startSyncWorker();
    vi.advanceTimersByTime(30_000);

    expect(runSync).not.toHaveBeenCalled();
  });

  it("syncs when the tab regains visibility", () => {
    Object.defineProperty(navigator, "onLine", { value: true, configurable: true });
    stop = startSyncWorker();
    vi.mocked(runSync).mockClear();

    Object.defineProperty(document, "visibilityState", {
      value: "hidden",
      configurable: true,
    });
    document.dispatchEvent(new Event("visibilitychange"));
    expect(runSync).not.toHaveBeenCalled();

    Object.defineProperty(document, "visibilityState", {
      value: "visible",
      configurable: true,
    });
    document.dispatchEvent(new Event("visibilitychange"));
    expect(runSync).toHaveBeenCalledTimes(1);
  });

  it("stops listening and polling once the returned cleanup runs", () => {
    vi.useFakeTimers();
    Object.defineProperty(navigator, "onLine", { value: false, configurable: true });
    stop = startSyncWorker();

    stop();
    stop = undefined;
    window.dispatchEvent(new Event("online"));
    document.dispatchEvent(new Event("visibilitychange"));
    vi.advanceTimersByTime(60_000);

    expect(runSync).not.toHaveBeenCalled();
  });
});
