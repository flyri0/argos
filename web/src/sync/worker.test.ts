import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./engine", () => ({ runSync: vi.fn() }));

import { runSync } from "./engine";
import { startSyncWorker } from "./worker";

let stop: (() => void) | undefined;

beforeEach(() => {
  vi.mocked(runSync).mockClear();
});

afterEach(() => {
  stop?.();
  stop = undefined;
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

  it("stops listening once the returned cleanup runs", () => {
    Object.defineProperty(navigator, "onLine", { value: false, configurable: true });
    stop = startSyncWorker();

    stop();
    stop = undefined;
    window.dispatchEvent(new Event("online"));

    expect(runSync).not.toHaveBeenCalled();
  });
});
