# Board continuity

`NewManager()` intentionally keeps Slate's legacy process-local behavior: the
WebSocket relay works with no database, and local/unit tests do not need AWS.
For a server that should survive process restarts, inject a
`BoardSnapshotStore` at startup:

```go
manager := boards.NewManagerWithPersistence(
	store.NewBoardStore(backend),
	boards.DefaultSaveDebounce,
)
```

Accepted element batches update the in-memory relay immediately and schedule a
debounced snapshot save. `SaveSnapshot`, `Flush`, and `FlushAll` are explicit
hooks for request-driven saves and graceful shutdown. A joining session calls
`Resume` before sending the existing `init` frame, so reconnects receive the
latest saved elements without a second WebSocket protocol. The `init` frame
adds a small `room` metadata object (`room_id`, `version`, timestamps, and live
participant count); existing fields and message types are unchanged.

The DynamoDB adapter uses the existing single table with `BOARD#{roomID}` as
the partition key, a `CURRENT` item, and `VERSION#...` history items. History
listing and restore are manager methods (`ListVersions` and `Restore`), and
`ExportSnapshotJSON` emits a peer-free, versioned JSON document suitable for
files or external tooling. A persistence read failure returns the existing
error-frame shape with `PERSISTENCE_ERROR`; the connection stays open so the
client can retry `join`. A missing snapshot is treated as a new room.
