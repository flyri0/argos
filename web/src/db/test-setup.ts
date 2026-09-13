// Polyfills `indexedDB`/`IDBKeyRange` so Dexie can run against an in-memory
// store under Node, with no real browser needed.
import "fake-indexeddb/auto";
