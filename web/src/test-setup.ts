// Polyfills `indexedDB`/`IDBKeyRange` so Dexie can run under jsdom, which
// (unlike a real browser) doesn't implement IndexedDB itself.
import "fake-indexeddb/auto";
import "@testing-library/jest-dom/vitest";
// Normally initialized by main.tsx, which tests never import.
import "./i18n";

import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// vitest.config doesn't set test.globals, so @testing-library/react's own
// auto-cleanup (which looks for a global afterEach) never registers —
// without this, each component test's render() output piles up in jsdom's
// shared document instead of unmounting after the test.
afterEach(cleanup);
