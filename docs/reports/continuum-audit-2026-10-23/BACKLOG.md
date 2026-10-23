# Continuum quality backlog

Baseline: `1ec26fb0b59cb4704ccce28b93fc4046da5a35f4` · 23 October 2026.

120 distinct items. Defects, risks, policy decisions and polish are labeled separately. Source review is not a claim of runtime reproduction. The full project context and verification results are in the LaTeX report.

## CQ-001 · Known development secrets are accepted at startup

**P1 · Hardening · Source review**

Source: [internal/config/config.go:66](../../../internal/config/config.go:66)

Starting the API without explicit secrets uses a published admin token and JWT signing key. An accidentally exposed development instance therefore has predictable credentials.

**Fix:** Require explicit strong secrets outside an explicit local development mode.

**Acceptance:** Production-mode startup rejects missing, default and empty secrets; isolated local mode remains convenient.

## CQ-002 · Signing out leaves the administrator token behind

**P1 · Defect · Source review**

Source: [web/src/lib/auth.ts:107](../../../web/src/lib/auth.ts:107)

clearAuth removes login state but leaves slate.admin-token in localStorage. On a shared browser the next person can retain cockpit authority after a visible sign-out.

**Fix:** Clear or separately lock the admin session on sign-out; use a short-lived scoped operator session.

**Acceptance:** Sign out with an admin token present; reopening /ops requires operator authentication.

## CQ-003 · A temporary refresh outage destroys the login session

**P1 · Defect · Source review**

Source: [web/src/lib/api.ts:91](../../../web/src/lib/api.ts:91)

Every non-success refresh response clears authentication, including 503 and 429. A backend recovery or throttling event signs learners out even though their refresh token may remain valid.

**Fix:** Clear the session only for definitive token rejection; retain it on transient failures.

**Acceptance:** Force refresh to return 503 and 429; stored authentication remains and a later request recovers.

## CQ-004 · Refresh tokens remain reusable until expiry

**P2 · Hardening · Source review**

Source: [internal/httpapi/server.go:391](../../../internal/httpapi/server.go:391)

Refresh issues replacement tokens without consuming the old refresh token. A copied token can be replayed repeatedly during its lifetime unless a password change increments the session version.

**Fix:** Add rotating refresh sessions with reuse detection and revocation.

**Acceptance:** Refresh once, replay the old token, and verify the old token is rejected without invalidating unrelated sessions.

## CQ-005 · Email-bound invitations trust unverified addresses

**P1 · Hardening · Source review**

Source: [internal/httpapi/server.go:309](../../../internal/httpapi/server.go:309)

Registration immediately trusts the submitted email and returns a session. Possession of an invitation code plus a newly claimed matching email is enough to join; mailbox ownership is never established.

**Fix:** Verify mailbox ownership before accepting email-bound invitations.

**Acceptance:** A newly registered unverified address cannot consume an invitation until verification completes.

## CQ-006 · Credential recovery and password changes have no route limits

**P1 · Hardening · Source review**

Source: [internal/middleware/middleware.go:219](../../../internal/middleware/middleware.go:219)

The limiter registers only login and registration. Forgot-password, reset-password and change-password can repeatedly trigger email delivery, token records or expensive bcrypt work.

**Fix:** Apply bounded per-origin and per-identity limits to recovery and password endpoints.

**Acceptance:** Repeated recovery and password requests receive 429 while normal sign-in and exam traffic continue.

## CQ-007 · Proxy traffic shares the same login-rate bucket

**P1 · Defect · Source review**

Source: [internal/middleware/middleware.go:226](../../../internal/middleware/middleware.go:226)

Rate limiting keys off the immediate remote host. The public router forwards requests from itself, so many learners reaching one backend can share a small login bucket. The Gateway adapter also omits source identity.

**Fix:** Propagate a verified client identity through the trusted router and enforce limits at the appropriate boundary.

**Acceptance:** Several independent clients can each use their allowance through the router; spoofed forwarded headers do not bypass limits.

## CQ-008 · Setting a rate limit to zero panics

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/middleware/middleware.go:178](../../../internal/middleware/middleware.go:178)

MaxHits equal to zero enters the rejection branch with an empty hit slice and reads hits[0]. The focused probe reproduced an index-out-of-range panic.

**Fix:** Define zero semantics explicitly and validate configuration before constructing the limiter.

**Acceptance:** Zero configured hits produce a controlled rejection or startup error without a panic.

## CQ-009 · Negative security settings silently disable protection

**P2 · Defect · Reproduced (isolated memory API)**

Source: [internal/config/config.go:153](../../../internal/config/config.go:153)

Numeric parsing accepts negative login and registration limits; Allow interprets a negative maximum as unlimited. Other durations and thresholds also lack range validation.

**Fix:** Validate semantic ranges and offer a named, explicit development override for unlimited limits.

**Acceptance:** Negative limits, nonpositive token lifetimes and invalid threshold values fail configuration loading.

## CQ-010 · Registration counts name bytes while profile editing counts characters

**P2 · Defect · Reproduced (isolated memory API)**

Source: [internal/httpapi/server.go:2146](../../../internal/httpapi/server.go:2146)

Registration rejects names over 60 UTF-8 bytes, while UpdateName allows 60 Unicode code points. The probe rejected a name containing only 21 CJK characters.

**Fix:** Use one character-count policy across creation, profile changes and UI hints.

**Acceptance:** The same 60-character Unicode name can be registered and subsequently edited.

## CQ-011 · The standalone API has no request-body budget

**P1 · Hardening · Source review**

Source: [internal/httpapi/server.go:2110](../../../internal/httpapi/server.go:2110)

JSON decoding reads an unbounded body and the direct server sets only header and idle timeouts. The Continuum router has a size limit, but standalone API deployments bypass it.

**Fix:** Limit request bodies and body-read time at the API boundary, with separate intentional board limits.

**Acceptance:** Oversized JSON returns 413 and a stalled body times out without retaining an unbounded handler.

## CQ-012 · Sensitive API responses omit explicit no-store policy

**P2 · Hardening · Reproduced (isolated memory API)**

Source: [internal/httpapi/server.go:2126](../../../internal/httpapi/server.go:2126)

Identity, results, roster and token responses use the generic writer without Cache-Control. The focused identity probe observed an empty cache policy. Browser or intermediary defaults are left to decide retention.

**Fix:** Emit an explicit no-store policy for identity, credentials and private assessment data.

**Acceptance:** Sensitive responses consistently carry the intended no-store header through local and cloud routing.

## CQ-013 · A single malformed availability boundary is accepted

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/httpapi/server.go:2194](../../../internal/httpapi/server.go:2194)

createExam validates timestamps only when both are supplied. The probe created an exam with opens_at set to garbage; EnforceWindow later ignores invalid stored boundaries.

**Fix:** Validate each nonempty timestamp independently before comparing the pair.

**Acceptance:** Malformed opening-only and closing-only payloads fail with a field-level 4xx response.

## CQ-014 · Creation and editing enforce different text limits

**P2 · Defect · Source review**

Source: [internal/httpapi/server.go:2153](../../../internal/httpapi/server.go:2153)

Direct exam creation does not apply the title, instruction and feedback limits used by other authoring routes or updates. A newly created value can be impossible to save unchanged later.

**Fix:** Centralize the assessment validation contract for all creation and update paths.

**Acceptance:** Identical title, instruction and feedback boundaries are accepted or rejected consistently across endpoints.

## CQ-015 · Direct exam creation can exceed the transaction budget

**P1 · Defect · Source review**

Source: [internal/store/store.go:402](../../../internal/store/store.go:402)

EnsureExam emits one metadata write plus one per question, and routed writes add a fencing condition. The direct create route does not bound question count, so an accepted payload can exceed DynamoDB's transaction limit.

**Fix:** Enforce the effective atomic question budget before persistence and return a useful validation message.

**Acceptance:** At and above the supported question boundary, memory and DynamoDB paths return equivalent outcomes.

## CQ-016 · Noncanonical question keys validate but grade incorrectly

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/exams/exams.go:450](../../../internal/exams/exams.go:450)

validateAnswers uses Atoi, accepting keys such as 00 or +0, while grading looks up canonical decimal keys. The probe submitted the correct choice under 00 and received zero points.

**Fix:** Reject noncanonical keys or canonicalize with collision detection before validation and grading.

**Acceptance:** Alternate numeric spellings never silently become an accepted but ungraded answer.

## CQ-017 · Invalid answer payloads look like service outages

**P2 · Defect · Reproduced (isolated memory API)**

Source: [internal/httpapi/server.go:2070](../../../internal/httpapi/server.go:2070)

validateAnswers returns ordinary formatted errors, while writeExamError maps unknown errors to 503. The probe sent an out-of-range choice and received data layer unavailable.

**Fix:** Return a typed validation error and map it to 400 or 422.

**Acceptance:** Invalid question keys, invalid choices and negative save versions produce clear nonretryable client errors.

## CQ-018 · A timed assessment accepts submission without a started attempt

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/exams/exams.go:226](../../../internal/exams/exams.go:226)

The deadline check rejects only a non-nil existing deadline. A learner can call the final submit endpoint before the start route; the focused timed-exam probe committed successfully.

**Fix:** Require an existing started attempt for timed submissions and bind its identity at commit.

**Acceptance:** Submission without a start is rejected; a legitimately started attempt retains the same deadline.

## CQ-019 · Teachers can enter the learner submission pipeline

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/exams/exams.go:175](../../../internal/exams/exams.go:175)

Submission requires a user but does not require the student role. A teacher's direct submit request committed a scored submission in the probe, potentially contaminating learner analytics.

**Fix:** Restrict learner save/start/submit routes to students or provide a separate explicitly excluded preview mode.

**Acceptance:** Teacher preview does not create attempts or grade rows counted as learner submissions.

## CQ-020 · Editing duration changes deadlines for attempts already running

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/store/store.go:2938](../../../internal/store/store.go:2938)

Deadlines are recomputed from current exam duration rather than stored with the attempt. The probe shortened duration from 15 to 1 minute and observed the active deadline move by 14 minutes.

**Fix:** Persist each attempt's authoritative deadline and define explicit rules for permitted extensions.

**Acceptance:** Shortening an exam's default duration does not retroactively shorten existing attempts.

## CQ-021 · Close does not stop an assessment that allows late work

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/httpapi/server.go:1625](../../../internal/httpapi/server.go:1625)

closeExam sets closes_at to now but leaves allow_late enabled. EnforceWindow still admits late work; the focused probe could reopen the learner delivery route after Close succeeded.

**Fix:** Make the close action explicitly stop acceptance, or rename it and provide a separate hard-close action.

**Acceptance:** After a hard close, new starts and final submissions are rejected even when late work was previously enabled.

## CQ-022 · Immediate answer review can reveal answers before retakes

**P1 · Policy gap · Source review**

Source: [internal/httpapi/server.go:955](../../../internal/httpapi/server.go:955)

Every result exposes correct answers and feedback immediately. An exam with multiple or unlimited attempts lets a learner review the answer key and then start another scored attempt.

**Fix:** Add a teacher-controlled review-release policy and explain how it interacts with retakes.

**Acceptance:** With review delayed, learners can see their receipt and permitted score without receiving the answer key.

## CQ-023 · Losing classroom access also removes receipt access

**P2 · Policy gap · Source review**

Source: [internal/httpapi/server.go:934](../../../internal/httpapi/server.go:934)

Result access rechecks current enrollment and current classroom assignment. A removed learner or a learner whose exam is reassigned loses access to an already committed result.

**Fix:** Authorize historical receipts by submission ownership while enforcing current membership for new work.

**Acceptance:** Remove a submitted learner from a class; their own existing receipt remains readable.

## CQ-024 · Earlier attempt receipts have no learner retrieval route

**P2 · Missing capability · Source review**

Source: [internal/httpapi/server.go:937](../../../internal/httpapi/server.go:937)

Retakes archive previous submission snapshots, but /result reads only the latest submission and accepts no attempt selector. The UI promise that earlier receipts remain recorded has no history browsing path.

**Fix:** Expose a user-scoped attempt history and receipt route, then link it from results.

**Acceptance:** After two completed attempts, both receipts and their answer reviews can be retrieved independently.

## CQ-025 · Result review silently disappears on a question-read error

**P2 · Defect · Source review**

Source: [internal/httpapi/server.go:946](../../../internal/httpapi/server.go:946)

The result handler discards GetExam errors and returns 200 with an empty review. A data-layer failure becomes indistinguishable from a successful receipt lacking question detail.

**Fix:** Preserve the confirmed receipt but explicitly expose review availability and a retry path.

**Acceptance:** Inject a question read failure; the client sees a confirmed receipt plus a clear incomplete-review state.

## CQ-026 · Concurrent direct creates can overwrite an assessment

**P1 · Defect · Source review**

Source: [internal/httpapi/server.go:1431](../../../internal/httpapi/server.go:1431)

The create route checks absence before EnsureExam, whose metadata and question puts are unconditional. Two requests can pass the check together; one can replace the other's owner and content.

**Fix:** Use a conditional create transaction, mapping the loser to 409.

**Acceptance:** Concurrent creates of the same normalized ID produce exactly one winner and no mixed question set.

## CQ-027 · Draft updates have no cross-tab version check

**P1 · Defect · Source review**

Source: [internal/store/store.go:795](../../../internal/store/store.go:795)

UpdateQuizDraft conditions on owner and status but not a draft revision. Serialized autosave within one component cannot prevent another tab or device from overwriting newer edits.

**Fix:** Add a draft revision and require the version being edited on every update.

**Acceptance:** Two editors save from the same revision; the second receives a recoverable conflict instead of overwriting content.

## CQ-028 · Publishing can use a stale draft snapshot

**P1 · Defect · Source review**

Source: [internal/store/store.go:843](../../../internal/store/store.go:843)

Publish reads the draft and later conditions only on owner/status. A concurrent autosave can update questions while status remains draft, allowing an older read to be published and marked current.

**Fix:** Tie publish to a draft revision or content digest in the same transaction.

**Acceptance:** Hold publication after its read, save a newer draft, then resume; publication rejects the stale version.

## CQ-029 · A 98-question cloud draft cannot be published

**P2 · Defect · Source review**

Source: [internal/store/store.go:788](../../../internal/store/store.go:788)

Updates allow up to 98 questions, but PublishQuizDraft rejects more than 97. Memory-backed publishing follows a different path, so local success can conceal this cloud-only boundary.

**Fix:** Align authoring, storage and publishing limits and display the supported maximum.

**Acceptance:** Save and publish the largest allowed draft on both backends; the next question receives the same validation error.

## CQ-030 · Autosave deduplication does not bind the answer snapshot

**P2 · Defect · Reproduced (isolated memory API)**

Source: [internal/exams/exams.go:361](../../../internal/exams/exams.go:361)

Reusing the last save request ID returns duplicate before comparing payloads. The probe reused an ID with changed answers and received a successful duplicate response containing the older answers.

**Fix:** Store a payload digest and reject request-ID reuse with different answers.

**Acceptance:** Same-ID same-payload retry is idempotent; same-ID changed-payload retry returns a conflict.

## CQ-031 · Deadline validation is separated from the final write

**P1 · Hardening · Source review**

Source: [internal/exams/exams.go:231](../../../internal/exams/exams.go:231)

The service checks time before calling the store; the submission transaction fences the writer but does not check an immutable deadline or schedule revision. A delayed request can cross a closing boundary after validation.

**Fix:** Define commit-time deadline semantics and enforce a stored deadline or validated revision in the atomic write path.

**Acceptance:** A deliberately delayed final write crossing the allowed boundary has a deterministic documented outcome.

## CQ-032 · Assessment deletion leaves related data without a lifecycle policy

**P2 · Hardening · Source review**

Source: [internal/store/store.go:1423](../../../internal/store/store.go:1423)

DeleteExam removes only EXAM# rows. Attempts, submissions, request receipts, audit/outbox data and published draft records use other partitions and remain after the UI reports deletion.

**Fix:** Define archival and erasure semantics, then implement indexed cleanup or explicitly label archive behavior.

**Acceptance:** Delete or archive a populated assessment and verify each related data family follows the documented retention policy.

## CQ-033 · A concurrent metadata patch can resurrect a deleted exam

**P1 · Defect · Source review**

Source: [internal/store/store.go:977](../../../internal/store/store.go:977)

UpdateExamWindow uses an update without an existence condition. After staff authorization reads metadata, a concurrent delete can remove it and a later update can recreate a partial META row without questions.

**Fix:** Require metadata existence and a revision in the update condition.

**Acceptance:** Race deletion with a metadata patch; no partial assessment reappears.

## CQ-034 · The catalog scans globally and performs serial per-exam reads

**P2 · Performance · Source review**

Source: [internal/httpapi/server.go:970](../../../internal/httpapi/server.go:970)

listExams requests every exam, checks visibility one by one and loads attempt/submission state per learner. Response cost grows with the entire catalog, including exams the caller cannot see.

**Fix:** Query owner/member-visible indexes, paginate, and batch attempt enrichment.

**Acceptance:** Measure catalog queries with many unrelated classes; work scales with the requested visible page.

## CQ-035 · Analytics silently stop after 1,000 learners

**P2 · Defect · Source review**

Source: [internal/httpapi/server.go:1657](../../../internal/httpapi/server.go:1657)

examResults requests at most 1,000 latest submission rows and reports count and averages as if the list were complete. No continuation cursor or truncation signal reaches the teacher.

**Fix:** Paginate results and compute aggregates over the intended full population.

**Acceptance:** More than 1,000 submissions produce complete aggregates or a clearly labeled partial page.

## CQ-036 · Board history and export URLs do not match the API

**P1 · Defect · Reproduced (isolated memory API)**

Source: [web/src/lib/api.ts:216](../../../web/src/lib/api.ts:216)

The client calls /boards under BASE. The Vite proxy removes /api, while the server mounts history/export/restore only at /api/boards. Cloud BASE also leads to /boards. The focused client-path probe returned 404.

**Fix:** Choose one canonical board resource prefix and support it consistently in the client, proxy and API.

**Acceptance:** History, export and restore succeed through both the documented local frontend and deployed API base.

## CQ-037 · Any signed-in learner can read a known room's history

**P1 · Hardening · Reproduced (isolated memory API)**

Source: [internal/httpapi/server.go:1007](../../../internal/httpapi/server.go:1007)

History and export require only a current user, without room ownership, membership or an explicit room capability. The focused unrelated-learner probe read another room's content and version list.

**Fix:** Define the room sharing boundary and enforce membership or a deliberate share capability on read operations.

**Acceptance:** An unrelated signed-in learner cannot enumerate versions or export content from a private class board.

## CQ-038 · Any teacher can restore any known board

**P1 · Hardening · Reproduced (isolated memory API)**

Source: [internal/httpapi/server.go:1042](../../../internal/httpapi/server.go:1042)

Restore requires the global staff role, without checking which teacher owns the room. The focused unrelated-teacher probe restored a room successfully. Room IDs therefore act as broad administrative capabilities.

**Fix:** Associate rooms with owners/classes and authorize destructive history operations against that relationship.

**Acceptance:** A teacher from another class gets 403 on restore; the owning teacher can restore normally.

## CQ-039 · Drawing permissions are controlled by any teacher in a room

**P1 · Hardening · Source review**

Source: [internal/boards/protocol.go:154](../../../internal/boards/protocol.go:154)

The WebSocket permission branch checks only the teacher role, and Identity carries no stable user or class identity. Any teacher who knows a room code can enable or disable its learner drawing.

**Fix:** Include stable authenticated identity and verify the room's controlling teacher for permission changes.

**Acceptance:** An unrelated teacher can participate only with their granted room role and cannot change room permissions.

## CQ-040 · Restore returns capitalized element fields that the client does not read

**P1 · Defect · Reproduced (isolated memory API)**

Source: [internal/boards/manager.go:61](../../../internal/boards/manager.go:61)

RoomSnapshot exports Peers and Elements without JSON tags. The REST restore probe returned Elements, but api.boardRestore and BoardHistoryDialog expect elements. The callback therefore receives undefined.

**Fix:** Return a tagged response DTO matching the client contract and validate it at the boundary.

**Acceptance:** Restore returns a lowercase elements array, and the restoring client's canvas updates even without a socket event.

## CQ-041 · Connected board sessions survive token expiry and password revocation

**P1 · Hardening · Source review**

Source: [internal/boards/http.go:105](../../../internal/boards/http.go:105)

Authentication is performed once at the WebSocket handshake. Subsequent frames use the original identity indefinitely, so token expiry or a password change does not revoke an already connected board socket.

**Fix:** Bound socket authorization lifetime and revalidate or close sessions after revocation.

**Acceptance:** Revoke a learner's session while connected; further board mutations are refused within the defined interval.

## CQ-042 · Board bearer tokens travel in the URL

**P2 · Hardening · Source review**

Source: [web/src/lib/api.ts:385](../../../web/src/lib/api.ts:385)

The access token is appended to the WebSocket query string. URLs can be retained by infrastructure request logs and debugging tools, increasing the credential exposure surface.

**Fix:** Exchange the session for a short-lived room ticket and redact sensitive query fields in logs.

**Acceptance:** Connection URLs contain only short-lived scoped tickets, and logs do not retain reusable access tokens.

## CQ-043 · One slow socket can block unrelated board rooms

**P1 · Hardening · Source review**

Source: [internal/boards/manager.go:454](../../../internal/boards/manager.go:454)

Broadcast writes to peers serially. The socket has no write deadline, and element handling holds the manager-wide control lock while broadcasting. A blocked recipient can stall later traffic across rooms.

**Fix:** Use bounded per-peer send queues, write deadlines and room-level serialization.

**Acceptance:** Stall one peer's reads; healthy peers in that and another room continue receiving updates.

## CQ-044 · Element and room growth are not bounded

**P2 · Hardening · Source review**

Source: [internal/boards/manager.go:279](../../../internal/boards/manager.go:279)

Per-frame byte limits do not bound the total number of accepted element IDs, room size or room creation. A signed-in client can accumulate many valid batches and exhaust process memory.

**Fix:** Add room, element, byte and per-identity quotas with clear rejection signals.

**Acceptance:** Many small valid frames eventually hit a controlled room limit without exhausting memory or affecting other rooms.

## CQ-045 · Full board snapshots can outgrow a DynamoDB item

**P1 · Hardening · Source review**

Source: [internal/store/board_store.go:102](../../../internal/store/board_store.go:102)

Each complete board is serialized into snapshot_json in both current and history records. There is no preflight size limit or blob-storage path when a scene approaches DynamoDB's 400 KB item maximum.

**Fix:** Keep large payloads in object storage or chunk them, and surface limits before accepting unsavable scenes.

**Acceptance:** Save a scene near and above the item-size boundary; durability remains explicit and predictable.

## CQ-046 · Board persistence can overwrite newer history with an older revision

**P1 · Hardening · Source review**

Source: [internal/store/board_store.go:117](../../../internal/store/board_store.go:117)

Current and history are unconditional puts. Serialization exists only inside one Manager; separate processes or out-of-order explicit saves can replace a newer current pointer or overwrite the same history key.

**Fix:** Add monotonic current-version conditions and immutable history conditions.

**Acceptance:** Competing saves of older/newer or equal versions cannot roll back current state or rewrite an existing history record.

## CQ-047 · Peers receive raw element values that the server did not accept

**P2 · Defect · Source review**

Source: [internal/boards/protocol.go:138](../../../internal/boards/protocol.go:138)

The server filters maps and absorbs accepted versions, then broadcasts the original values list if any element changed. Mixed batches can forward invalid or rejected elements that are absent from authoritative server state.

**Fix:** Broadcast the normalized accepted element set or the authoritative merged result.

**Acceptance:** Mixed valid/invalid/stale batches leave sender, peers and a reloaded snapshot consistent.

## CQ-048 · A transient snapshot failure has no automatic recovery attempt

**P2 · Hardening · Source review**

Source: [internal/boards/persistence.go:562](../../../internal/boards/persistence.go:562)

Failed snapshots are retained without a timer. This avoids retrying stale fenced epochs, but a transient failure on a valid writer can remain unsaved indefinitely unless another edit or explicit flush occurs.

**Fix:** Retry eligible transient errors with bounded backoff while stopping retries for fenced writers.

**Acceptance:** Fail one persistence call, stop drawing, restore storage, and verify the latest valid snapshot becomes durable.

## CQ-049 · Board live status does not acknowledge durable saves

**P2 · Missing capability · Source review**

Source: [internal/boards/persistence.go:198](../../../internal/boards/persistence.go:198)

Async persistence errors are stored only in Manager state and are not sent to the browser. A live connection can continue displaying collaborative edits while their snapshots fail to persist.

**Fix:** Expose separate connection and persistence status with last durable revision and recovery feedback.

**Acceptance:** Inject a snapshot failure; users see unsaved status and later see the acknowledged durable revision.

## CQ-050 · Leaving a board cancels the last local save and outgoing batch

**P2 · Defect · Source review**

Source: [web/src/components/board/board-canvas.tsx:171](../../../web/src/components/board/board-canvas.tsx:171)

Cleanup clears the 100 ms outgoing timer and 800 ms local-save timer without flushing either buffer. Navigating away immediately after a stroke can lose both transmission and local recovery of that stroke.

**Fix:** Flush pending local data synchronously and hand queued mutations to a durable outbox before unmount.

**Acceptance:** Draw and navigate away within both debounce windows; reopening restores the final stroke.

## CQ-051 · Image elements are synchronized without their binary files

**P2 · Defect · Source review**

Source: [web/src/components/board/board-canvas.tsx:112](../../../web/src/components/board/board-canvas.tsx:112)

The canvas handles only elements and appState. Excalidraw image file data is neither stored, exported nor relayed, so image elements can reference unavailable files after reload or for another participant.

**Fix:** Support an authorized binary-file store/relay, or disable unsupported image tools with clear messaging.

**Acceptance:** Insert an image and verify its pixels remain visible after reload and in a second session.

## CQ-052 · Reconnect initialization replaces locally buffered board work

**P1 · Defect · Source review**

Source: [web/src/hooks/use-board-sync.ts:115](../../../web/src/hooks/use-board-sync.ts:115)

On init, remoteSnapshot replaces the local scene. The outbox is drained before the authoritative init arrives, so offline edits can be sent and then temporarily or permanently replaced by an older init snapshot.

**Fix:** Reconcile initialization with retained local mutations and clear them only after acknowledgment.

**Acceptance:** Draw offline, reconnect to an older server snapshot, and verify every local edit survives initialization and reload.

## CQ-053 · Board status becomes live before join succeeds

**P1 · Defect · Source review**

Source: [web/src/hooks/use-board-sync.ts:83](../../../web/src/hooks/use-board-sync.ts:83)

onopen sets live and drains drawing work immediately after sending join. A persistence error during join leaves the session unjoined, but the client has already advertised live and may have sent mutations that are refused.

**Fix:** Introduce an initializing state and wait for a successful init frame before draining work or enabling controls.

**Acceptance:** Make join fail once; the UI remains unready and queued edits are replayed after a successful init.

## CQ-054 · Outgoing board work is forgotten without acknowledgment

**P1 · Hardening · Source review**

Source: [web/src/hooks/use-board-sync.ts:91](../../../web/src/hooks/use-board-sync.ts:91)

drainOutbox removes the whole queue when the transport opens, and send removes responsibility as soon as socket.send is called. A disconnect before processing can lose work without a server acknowledgment.

**Fix:** Use mutation IDs and acknowledgments, retaining unconfirmed work through reconnect.

**Acceptance:** Drop the connection after send but before application processing; reconnect replays exactly the unacknowledged mutations.

## CQ-055 · Long offline sessions silently discard older drawing batches

**P2 · Defect · Source review**

Source: [web/src/hooks/use-board-sync.ts:58](../../../web/src/hooks/use-board-sync.ts:58)

Once the outbox reaches 200 batches, oldest batches are removed without compaction or a user warning. Earlier independently created elements may never reach collaborators.

**Fix:** Compact by element/revision or persist the queue and display a meaningful capacity state.

**Acceptance:** Exceed 200 offline batches; all final element states survive or the user receives an explicit export/recovery action.

## CQ-056 · The heartbeat does not detect a silent half-open connection

**P2 · Hardening · Source review**

Source: [web/src/hooks/use-board-sync.ts:94](../../../web/src/hooks/use-board-sync.ts:94)

The client sends ping regularly but ignores pong and has no response deadline. A socket that stays OPEN while application responses stop can continue to appear live.

**Fix:** Track the last pong or application response and reconnect after a bounded silence.

**Acceptance:** Suppress server replies while keeping the transport open; the UI detects loss and begins recovery.

## CQ-057 · Rejected board tokens reconnect forever with the shortest backoff

**P2 · Defect · Source review**

Source: [web/src/hooks/use-board-sync.ts:153](../../../web/src/hooks/use-board-sync.ts:153)

onclose ignores the close code. onopen resets the attempt counter even when authentication is immediately rejected, so expired tokens can loop roughly every 500 ms without a refresh or sign-in action.

**Fix:** Treat auth/origin/payload close codes explicitly and reset backoff only after a successful joined session.

**Acceptance:** Expired-token and forbidden-origin closures stop retry storms and show an actionable recovery state.

## CQ-058 · A vertical bar in a display name corrupts the join identity

**P2 · Defect · Source review**

Source: [web/src/hooks/use-board-sync.ts:207](../../../web/src/hooks/use-board-sync.ts:207)

The hook concatenates name and role with a vertical bar and later splits the string. A valid display name containing that character is truncated and shifts the role field in the emitted join frame.

**Fix:** Keep name and role as separate effect dependencies and values.

**Acceptance:** Join with a display name containing a vertical bar; the complete display name reaches peers.

## CQ-059 · Board snapshots and room history are shared between local users

**P1 · Hardening · Source review**

Source: [web/src/components/board/board-canvas.tsx:33](../../../web/src/components/board/board-canvas.tsx:33)

Board storage keys contain only room ID, and recent-room/history stores are also browser-global. Signing out and signing in as another user can reveal the previous user's local scene or room identifiers.

**Fix:** Scope local board data to the authenticated user and provide an explicit shared-device cleanup policy.

**Acceptance:** Switch between two users in one browser; private board snapshots and recent history remain isolated.

## CQ-060 · Moving between room routes can retain the previous canvas state

**P2 · Defect · Source review**

Source: [web/src/components/board/board-canvas.tsx:73](../../../web/src/components/board/board-canvas.tsx:73)

Room changes update initialElements, but element refs and the mounted Excalidraw instance are not reset or keyed by room. Before a new init, outgoing data or snapshots can still refer to the old room.

**Fix:** Key/reset the canvas and all timers, refs and buffers by room and user identity.

**Acceptance:** Navigate directly between two room routes during a pending save; no scene or mutation crosses rooms.

## CQ-061 · Room normalization silently maps different input codes to one room

**P2 · Defect · Source review**

Source: [web/src/lib/room-code.ts:9](../../../web/src/lib/room-code.ts:9)

Punctuation is removed and long inputs are truncated. Distinct pasted codes can silently join the same normalized room, while the server itself accepts raw room IDs.

**Fix:** Validate a canonical room format and reject invalid input instead of silently rewriting meaningful characters.

**Acceptance:** Invalid punctuation and overlong codes receive a clear error; equivalent links use exactly one canonical ID.

## CQ-062 · Export can replace the newest local scene with an older server scene

**P2 · Defect · Source review**

Source: [web/src/routes/board.$roomId.tsx:133](../../../web/src/routes/board.$roomId.tsx:133)

exportBoard first reads local elements, then overwrites them with server export on success. Edits still inside the outgoing debounce or unconfirmed queue can disappear from the downloaded file.

**Fix:** Export a clearly identified local or durable revision, flushing and confirming when required.

**Acceptance:** Export immediately after a stroke and during reconnect; the chosen snapshot contains the expected latest work.

## CQ-063 · Cursor frames bypass join and coordinate validation

**P2 · Hardening · Source review**

Source: [internal/boards/protocol.go:162](../../../internal/boards/protocol.go:162)

Cursor messages are accepted before Joined and relay arbitrary x/y values. A client can advertise presence without initialization or send unusable coordinate objects to every peer.

**Fix:** Require join completion and validate finite bounded coordinates and a supported tool identifier.

**Acceptance:** Pre-join and invalid-coordinate cursor frames are rejected; valid cursors remain bounded and render correctly.

## CQ-064 · Clearing the final answer is never autosaved to the server

**P1 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:1096](../../../web/src/routes/quiz.$examId.tsx:1096)

The autosave effect returns when the answer map is empty. After the last saved answer is cleared, server state still retains it and can resurrect it on another device or saved-attempt submission.

**Fix:** Treat an empty answer map as a valid dirty snapshot that must be saved.

**Acceptance:** Save one answer, clear it, wait for autosave, then resume on a second client; the answer remains cleared.

## CQ-065 · Learner autosaves can overlap and race their versions

**P1 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:1100](../../../web/src/routes/quiz.$examId.tsx:1100)

A new debounce can start while the previous save is pending, using the same serverVersionRef. The server correctly rejects a stale version, but ordinary slow networking triggers conflicts and unnecessary merges.

**Fix:** Serialize saves and coalesce the latest snapshot behind the in-flight request.

**Acceptance:** Delay each save beyond the debounce interval while changing answers; saves complete in order without false conflicts.

## CQ-066 · An older save can label newer local answers durably saved

**P2 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:933](../../../web/src/routes/quiz.$examId.tsx:933)

Every onSuccess sets saveStatus to durable without checking which answer snapshot it saved. A learner who edits while a slow save is in flight can see a durable badge for unsaved newer work.

**Fix:** Associate save responses with a snapshot generation or content digest.

**Acceptance:** Resolve an old save after a newer edit; the UI continues to show the newer snapshot as pending.

## CQ-067 · Failed autosaves have no retry action or scheduled retry

**P2 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:956](../../../web/src/routes/quiz.$examId.tsx:956)

Save failure changes the badge to Save needs retry, but the effect depends on answers/online/pending state and does not retry an unchanged snapshot. There is no save-retry button.

**Fix:** Offer a retry control and bounded transient retry while preserving the current snapshot.

**Acceptance:** Fail one save, leave answers unchanged and restore service; the learner can recover durability without editing an answer.

## CQ-068 · Conflict merging can bring back intentionally cleared answers

**P1 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:943](../../../web/src/routes/quiz.$examId.tsx:943)

Conflict handling merges latest server answers with the current partial map. Absence is used for a cleared answer, so a server value fills it back in; deletion intent is not represented.

**Fix:** Track answer deletions explicitly or use a three-way merge against the edited base revision.

**Acceptance:** Clear an answer in one tab while another saves; conflict resolution preserves the deliberate clear.

## CQ-069 · Old local drafts override newer server answers on resume

**P1 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:869](../../../web/src/routes/quiz.$examId.tsx:869)

Runner initialization spreads server answers and then the local draft. Local drafts contain no base server version, so an old browser copy can silently replace newer answers saved from another device.

**Fix:** Version local drafts and reconcile against the server revision before choosing a winner.

**Acceptance:** Save newer answers on another device, then reopen an old local draft; the UI presents a conflict instead of replacing them silently.

## CQ-070 · The visible countdown trusts the device wall clock

**P1 · Hardening · Source review**

Source: [web/src/routes/quiz.$examId.tsx:1107](../../../web/src/routes/quiz.$examId.tsx:1107)

remainingMs is server deadline minus Date.now without server-time synchronization. A fast device can auto-submit early; a slow device can show time remaining after the API stops accepting work.

**Fix:** Supply server time and estimate clock offset using a monotonic elapsed timer.

**Acceptance:** Shift the browser wall clock forward/backward; the countdown and submission boundary remain aligned with the server.

## CQ-071 · An expired unsubmitted attempt has no recovery completion path

**P1 · Missing capability · Source review**

Source: [internal/exams/exams.go:231](../../../internal/exams/exams.go:231)

Once duration plus grace expires, new final submissions are rejected even if autosaved answers exist. A closed browser cannot auto-submit, and the attempt can remain in progress with no result or retake route.

**Fix:** Define a server-side timeout finalization or explicit expired-attempt workflow using the last durable answers.

**Acceptance:** Let an attempt expire with saved answers while the browser is closed; reopening shows a deterministic final or expired state.

## CQ-072 · Browser storage failure prevents any final submission

**P2 · Policy gap · Source review**

Source: [web/src/routes/quiz.$examId.tsx:1058](../../../web/src/routes/quiz.$examId.tsx:1058)

Submission is intentionally withheld if the local immutable request cannot be stored. This protects retry identity, but a learner with blocked/full storage has no alternative completion path, even while the server is reachable.

**Fix:** Provide a durable server-prepared submission intent or another explicit fallback that retains idempotency.

**Acceptance:** Disable localStorage writes and complete an assessment using a receipt-safe supported path.

## CQ-073 · Previous and Next do not navigate the filtered question set

**P2 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:1149](../../../web/src/routes/quiz.$examId.tsx:1149)

Next and Previous increment raw positions while the filter effect forces nonmatching positions to the first match. With separated flagged questions, navigation can jump backward or repeatedly return to the same question.

**Fix:** Advance through visiblePositions and define behavior when the final filtered question is reached.

**Acceptance:** Flag nonadjacent questions and use both navigation buttons; movement follows the selected filter in order.

## CQ-074 · The screen reader countdown announces every second

**P2 · Accessibility · Source review**

Source: [web/src/routes/quiz.$examId.tsx:1433](../../../web/src/routes/quiz.$examId.tsx:1433)

A polite live region changes once a second for the full timed attempt. Continuous announcements can interrupt question and option reading, making the exam harder to complete with assistive technology.

**Fix:** Announce meaningful time thresholds and expiry; leave a readable timer available on demand.

**Acceptance:** A screen-reader session receives only useful threshold announcements while reading questions uninterrupted.

## CQ-075 · Learners cannot intentionally submit an empty assessment

**P3 · Polish · Source review**

Source: [web/src/routes/quiz.$examId.tsx:1328](../../../web/src/routes/quiz.$examId.tsx:1328)

The manual submit button is disabled when answeredCount is zero even though the API accepts an empty map and the timer can submit one automatically. Learners cannot deliberately finish with all questions skipped.

**Fix:** Allow empty submission after a clear zero-answered confirmation.

**Acceptance:** A learner can intentionally submit all questions unanswered and receives an honest zero-score receipt.

## CQ-076 · Cached question data bypasses the explicit begin screen

**P2 · Defect · Source review**

Source: [web/src/routes/quiz.$examId.tsx:289](../../../web/src/routes/quiz.$examId.tsx:289)

ExamLobby renders the runner whenever examQuery.data exists, even when beginRequested is false. Returning while the query cache survives can skip the promised explicit lobby/resume action.

**Fix:** Gate runner rendering on an explicit begin/resume decision as well as valid cached data.

**Acceptance:** Leave and return to an attempt with warm query cache; the lobby appears until Resume is chosen.

## CQ-077 · Downloaded receipts omit assessment and learner identity

**P2 · Polish · Source review**

Source: [web/src/lib/result-receipt.ts:6](../../../web/src/lib/result-receipt.ts:6)

The receipt contains score, timestamp, request reference and answers, but no exam title/ID, learner identity or attempt number. Files separated from their filename or shared for support are difficult to attribute.

**Fix:** Include safe assessment identity, learner reference and attempt number in the receipt content.

**Acceptance:** Copy and download two different attempts; each text receipt can be unambiguously attributed without its filename.

## CQ-078 · Print result has no dedicated print layout

**P3 · Polish · Source review**

Source: [web/src/routes/quiz.$examId.tsx:731](../../../web/src/routes/quiz.$examId.tsx:731)

Print invokes window.print, while the surrounding shell uses a fixed viewport and scrollable main area and has no print stylesheet. Navigation and clipped scroll regions can appear in the printed result.

**Fix:** Add a print layout that expands the result, removes app navigation and preserves question page breaks.

**Acceptance:** Print a multi-page result; all feedback is visible without the sidebar, clipping or blank overflow pages.

## CQ-079 · Catalog availability does not advance with time

**P3 · Polish · Source review**

Source: [web/src/routes/index.tsx:336](../../../web/src/routes/index.tsx:336)

Open/upcoming/closed labels and disabled Start actions use Date.now only during rendering. A foreground idle page has no clock tick or boundary-triggered refresh, so a scheduled exam can stay disabled after opening.

**Fix:** Schedule refreshes at availability boundaries and expose a manual catalog refresh.

**Acceptance:** Leave the catalog untouched across an opening and closing boundary; labels and actions update on time.

## CQ-080 · Local draft save can fail while the UI says Saved locally

**P1 · Defect · Source review**

Source: [web/src/lib/quiz-drafts.ts:106](../../../web/src/lib/quiz-drafts.ts:106)

persist catches storage errors and returns no success state. QuizBuilder then updates savedAt and Saved locally, so a full or blocked storage area can be presented as recoverable after reload when it is only in memory.

**Fix:** Return persistence success and display an explicit session-only recovery warning.

**Acceptance:** Force quota/security errors; the builder never claims a successful local disk save.

## CQ-081 · Draft sync hides the actual server rejection

**P2 · Defect · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:280](../../../web/src/components/teacher/quiz-builder.tsx:280)

Every server-sync error becomes The server could not be reached, including validation, ownership, conflict and size errors. Teachers are directed toward connectivity when editing is actually required.

**Fix:** Preserve typed API error messages and separate actionable validation/conflict states from transport failures.

**Acceptance:** Trigger a 422 and a 409 during sync; each displays the relevant correction or conflict action.

## CQ-082 · Manual Save draft can show success after server sync fails

**P2 · Defect · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:684](../../../web/src/components/teacher/quiz-builder.tsx:684)

queueServerSync absorbs errors, then saveDraft can emit Draft saved because its captured status is not synced. A success toast can accompany a failed cloud save.

**Fix:** Return an explicit sync outcome and label local-only success separately.

**Acceptance:** Make the save endpoint fail; the toast clearly says which copy, if any, was saved.

## CQ-083 · Local draft recovery loses its server identity

**P2 · Defect · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:517](../../../web/src/components/teacher/quiz-builder.tsx:517)

loadLocalDraft calls replaceDraft without a server draft ID. A previously synced browser draft can be re-created as a new server draft or collide on its custom ID when recovered.

**Fix:** Persist the server draft ID/revision alongside each local recovery draft.

**Acceptance:** Recover a synced local draft and save it; exactly one server draft is updated.

## CQ-084 · Published recovery copies remain labeled as drafts

**P3 · Polish · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:747](../../../web/src/components/teacher/quiz-builder.tsx:747)

Successful publishing clears localDraftIdRef but does not remove or mark the saved local draft as published. The library can continue offering a stale draft whose server record is already published.

**Fix:** Mark the local copy with publication identity/status or remove it after confirmed publication with a clear archive path.

**Acceptance:** Publish a draft, reopen the local library, and verify it cannot be mistaken for an unpublished current draft.

## CQ-085 · Incomplete drafts cannot be saved to the server

**P2 · Missing capability · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:338](../../../web/src/components/teacher/quiz-builder.tsx:338)

Cloud sync requires a complete title, question, nonempty options and valid answer. Normal partially authored work remains browser-only, which limits cross-device editing and reliable recovery of unfinished work.

**Fix:** Separate permissive draft validation from strict publication validation.

**Acceptance:** Save an incomplete prompt or empty option, switch devices and continue the draft; publication remains correctly blocked.

## CQ-086 · Fractional scoring and attempt values pass authoring checks

**P2 · Defect · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:776](../../../web/src/components/teacher/quiz-builder.tsx:776)

Builder checks finite ranges but not integers for points, duration, attempt limit and grace. Import also permits fractional points, while the Go DTO fields are integers and reject those JSON values.

**Fix:** Share an integer validation schema across import, builder and API.

**Acceptance:** Decimal numeric values receive an inline correction before sync or publish is enabled.

## CQ-087 · The builder offers more questions than cloud publishing supports

**P2 · Polish · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:766](../../../web/src/components/teacher/quiz-builder.tsx:766)

canPublish checks completeness but no total question maximum. Add-question and bulk-import workflows can build an apparently publishable quiz beyond the cloud transaction boundary.

**Fix:** Show the effective question budget and reject or split oversized imports before editing work accumulates.

**Acceptance:** At the supported maximum the counter is clear; additional questions cannot lead to an unexplained publish failure.

## CQ-088 · Removing the correct option silently chooses a new correct answer

**P2 · Defect · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:552](../../../web/src/components/teacher/quiz-builder.tsx:552)

If the removed option was correct, answer is reset to zero. The first remaining option becomes correct without a deliberate teacher decision and still passes publication validation.

**Fix:** Clear correctness and require a new selection when the correct option is deleted.

**Acceptance:** Remove the marked answer; publication stays disabled until the teacher explicitly chooses the replacement.

## CQ-089 · Undo option removal can overwrite subsequent option edits

**P2 · Defect · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:562](../../../web/src/components/teacher/quiz-builder.tsx:562)

The undo closure restores the full earlier options array and answer. Edits or additions performed after removal can be lost when an older toast's Undo is clicked.

**Fix:** Use stable option IDs and an inverse operation that preserves unrelated later edits.

**Acceptance:** Remove an option, edit another option, then Undo; both the restored option and newer edits remain.

## CQ-090 · Browser-only bank questions have no explicit sync retry

**P2 · Missing capability · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:630](../../../web/src/components/teacher/quiz-builder.tsx:630)

A failed bank save retains a local question, but the bank list exposes use/delete without a per-item retry to upload it. Re-saving from the builder can create more local copies or duplicates.

**Fix:** Give pending bank items a stable client ID and explicit retry/auto-resume behavior.

**Acceptance:** Save a bank question offline, restore service and synchronize the same item once without duplication.

## CQ-091 · Local authoring caches ignore edits from other tabs

**P2 · Defect · Source review**

Source: [web/src/lib/quiz-drafts.ts:17](../../../web/src/lib/quiz-drafts.ts:17)

Draft and bank caches are populated once and have no storage-event invalidation, unlike auth. Two tabs can show stale libraries and overwrite each other's local collection on the next save.

**Fix:** Invalidate or merge scoped caches on storage events and use stable per-record revisions.

**Acceptance:** Save or delete in one tab; the other tab updates without reload and does not reintroduce deleted entries.

## CQ-092 · Saving past local capacity silently evicts older work

**P2 · Defect · Source review**

Source: [web/src/lib/quiz-drafts.ts:104](../../../web/src/lib/quiz-drafts.ts:104)

Drafts are sliced to 100 and bank items to 500 when persisted, with no archive, export or eviction warning. Creating a new item can silently remove an older unsynced recovery copy.

**Fix:** Enforce visible capacity management and protect unsynced data from silent eviction.

**Acceptance:** Reach capacity with unsynced records; users can export/archive or choose what to remove before data is dropped.

## CQ-093 · Editing availability silently truncates timestamp precision

**P2 · Defect · Source review**

Source: [web/src/lib/teacher-authoring.ts:13](../../../web/src/lib/teacher-authoring.ts:13)

toLocalDateTime slices to minute precision. Opening a published exam with seconds in its boundaries and saving an unrelated change sends those timestamps back rounded down.

**Fix:** Preserve untouched original timestamps or display/edit their full supported precision.

**Acceptance:** Edit only the title of a second-precision exam; opening and closing instants remain byte-for-byte equivalent.

## CQ-094 · Library pagination can point beyond the final page

**P3 · Defect · Source review**

Source: [web/src/components/teacher/quiz-builder.tsx:114](../../../web/src/components/teacher/quiz-builder.tsx:114)

makePage computes the new page count but slices using the existing unbounded page index. Deleting items or changing remote data can leave an empty library even when earlier pages contain records.

**Fix:** Clamp/reset the selected page whenever the filtered page count shrinks.

**Acceptance:** Delete the last item on the final page; the previous valid page appears automatically.

## CQ-095 · CSV parsing accepts malformed quotation in unquoted fields

**P2 · Defect · Source review**

Source: [web/src/lib/teacher-authoring.ts:129](../../../web/src/lib/teacher-authoring.ts:129)

Every quote toggles inQuotes regardless of its position in a field. Paired stray quotes inside unquoted text can be stripped or change delimiters while still producing a seemingly valid import.

**Fix:** Use a strict CSV parser or enforce quote placement and trailing-character rules.

**Acceptance:** Malformed mid-field and trailing quote cases produce row-specific errors instead of modified question text.

## CQ-096 · The local class list silently drops everything after 20

**P2 · Defect · Source review**

Source: [web/src/lib/classrooms.ts:164](../../../web/src/lib/classrooms.ts:164)

Every server sync truncates mapped classes to MAX_CLASSROOMS equal to 20. The API can return more, so older classes disappear from the selector without paging or a visible limit.

**Fix:** Preserve the full synchronized list or implement explicit pagination and caching.

**Acceptance:** Join or own more than 20 classes; all remain discoverable and selectable.

## CQ-097 · Learner roster totals ignore the server's member count

**P2 · Defect · Source review**

Source: [web/src/lib/classrooms.ts:188](../../../web/src/lib/classrooms.ts:188)

fromServer discards member_count and preserves a local members array. Students do not fetch the owner-only roster, so the dashboard can display only themselves or stale members as the class total.

**Fix:** Store server aggregate counts separately from permission-limited roster details.

**Acceptance:** A learner joins a populated class and sees the correct total without receiving private member data.

## CQ-098 · Teacher roster counts change after a fresh browser sync

**P3 · Defect · Source review**

Source: [web/src/lib/classrooms.ts:214](../../../web/src/lib/classrooms.ts:214)

Newly created local classes include an owner row. membersFromServer retains that row only if it already exists locally, so a fresh browser and the creator's browser count different populations.

**Fix:** Define whether the owner belongs in displayed counts and construct it consistently from server identity.

**Acceptance:** The same class shows identical roster totals in a fresh teacher browser and the original one.

## CQ-099 · Invitation failure prevents otherwise available roster refresh

**P2 · Defect · Source review**

Source: [web/src/lib/classrooms.ts:401](../../../web/src/lib/classrooms.ts:401)

Roster and invitation requests are combined with Promise.all. If only invitation retrieval fails, fresh roster rows are discarded and the entire roster is labeled unavailable.

**Fix:** Track and apply roster and invitation outcomes independently with partial-status feedback.

**Acceptance:** Fail invitation retrieval while roster succeeds; current members update and only invitation status is degraded.

## CQ-100 · Sync can adopt the wrong pending classroom by matching its name

**P2 · Defect · Source review**

Source: [web/src/lib/classrooms.ts:293](../../../web/src/lib/classrooms.ts:293)

A pending local class is adopted using case-insensitive name and subject equality rather than its stable client request ID. Two intentional classes with equal names can be conflated during reconciliation.

**Fix:** Return and reconcile the creation client ID or receipt identity from the server.

**Acceptance:** Create two equal-named classes under different request IDs; sync preserves both intended identities.

## CQ-101 · Existing invitations cannot be resent through the UI flow

**P2 · Missing capability · Source review**

Source: [web/src/lib/classrooms.ts:495](../../../web/src/lib/classrooms.ts:495)

The invitation helper finds a pending invitation and returns it without POSTing again. Although the backend delivery path can send an existing code, the UI's retry flow never reaches it.

**Fix:** Expose an explicit resend action with delivery status and a bounded resend policy.

**Acceptance:** Fail the first delivery, then resend from the roster; the same invitation code is emailed once service returns.

## CQ-102 · Classroom join codes have no rotation or expiry workflow

**P2 · Hardening · Source review**

Source: [internal/classrooms/service.go:290](../../../internal/classrooms/service.go:290)

Shared join codes remain valid until classroom deletion, and the API provides no code-rotation operation. A leaked or widely forwarded code cannot be revoked without deleting the class.

**Fix:** Allow an owning teacher to rotate/revoke a join code and optionally set expiry or approval rules.

**Acceptance:** Rotate a code; the old code is rejected while enrolled learners and the new code continue to work.

## CQ-103 · A removed learner can rejoin with the unchanged shared code

**P2 · Policy gap · Source review**

Source: [internal/classrooms/service.go:297](../../../internal/classrooms/service.go:297)

Removing membership does not change the join code or record a rejoin restriction. A learner removed for access reasons can immediately use the still-known class code to regain access.

**Fix:** Define removal semantics and offer a blocked/reapproval state or automatic code rotation.

**Acceptance:** Under a restrictive removal policy, the removed learner cannot regain enrollment without teacher approval.

## CQ-104 · Editing a failed class-creation form does not change the retried payload

**P2 · Defect · Source review**

Source: [web/src/components/classroom-dashboard-real.tsx:238](../../../web/src/components/classroom-dashboard-real.tsx:238)

When pendingCreateId exists, createClass retries the stored local draft rather than current className/subject fields. Teachers can correct visible input and still resend the original invalid values.

**Fix:** Lock the original retry payload visibly or create a new intent when edited fields change.

**Acceptance:** Cause a validation failure, correct the displayed form and retry; the corrected payload is sent with consistent request identity.

## CQ-105 · The active classroom board is only a browser-local setting

**P2 · Missing capability · Source review**

Source: [web/src/lib/classrooms.ts:763](../../../web/src/lib/classrooms.ts:763)

active_room_id is written only to local cached classes and is not part of the classroom API record. A teacher can mark a session ready on one device while learners or another device lack that selected session state.

**Fix:** Persist the active session on the classroom with authorized start/end actions.

**Acceptance:** Start a class board on one device; another authorized device discovers the same active room from the server.

## CQ-106 · Small screens hide essential page actions

**P1 · Defect · Source review**

Source: [web/src/components/layout/app-shell.tsx:35](../../../web/src/components/layout/app-shell.tsx:35)

All AppShell actions are hidden below the sm breakpoint with no mobile replacement. This removes the only teacher studio tab controls and board export/history/drawing controls on narrow screens.

**Fix:** Provide a responsive action menu or move essential controls into the page body.

**Acceptance:** At a 375-pixel viewport, teachers can reach Manage/Analytics and board users can reach export/history and permitted controls.

## CQ-107 · Select triggers display internal values instead of labels

**P3 · Defect · Observed in browser and source**

Source: [web/src/routes/index.tsx:283](../../../web/src/routes/index.tsx:283)

The inspected landing page showed all and recent in filter triggers instead of All activity and Most recent. SelectValue lacks a value-to-label mapping when items are not mounted.

**Fix:** Supply item metadata or render the selected display label explicitly.

**Acceptance:** Closed selects show readable labels before and after opening, including unknown-value fallback behavior.

## CQ-108 · Reset and account settings lack keyboard submit behavior

**P2 · Accessibility · Source review**

Source: [web/src/routes/login.tsx:248](../../../web/src/routes/login.tsx:248)

Password reset and account settings render input groups with click-only buttons rather than forms with onSubmit. Their fields lack the Enter handlers used by normal sign-in, so keyboard submission is inconsistent.

**Fix:** Use semantic forms and submit buttons while retaining pending-state and validation checks.

**Acceptance:** Keyboard-only users can submit reset and profile/password forms with Enter exactly once, matching normal sign-in behavior.

## CQ-109 · API fetches have no application timeout or cancellation

**P2 · Hardening · Source review**

Source: [web/src/lib/api.ts:121](../../../web/src/lib/api.ts:121)

The shared fetch layer forwards optional init but provides no default deadline, and query functions do not pass the React Query abort signal. A stalled request can leave actions waiting indefinitely after navigation.

**Fix:** Add bounded request deadlines and propagate cancellation from queries and workflows.

**Acceptance:** Hold a response indefinitely; the UI transitions to a recoverable timeout and navigation cancels obsolete reads.

## CQ-110 · Teacher CSV exports can contain spreadsheet formulas

**P1 · Hardening · Source review**

Source: [web/src/routes/teacher.tsx:954](../../../web/src/routes/teacher.tsx:954)

CSV escaping quotes syntax but does not neutralize formula prefixes in user-controlled student names. Opening results in a spreadsheet may interpret names beginning with equals or other formula markers as commands.

**Fix:** Export text-safe values using a documented CSV formula-escape policy.

**Acceptance:** Names beginning with formula markers open as literal text in the supported spreadsheet workflow.

## CQ-111 · A stale cockpit can still offer disruptive actions

**P2 · Defect · Source review**

Source: [web/src/routes/ops.tsx:475](../../../web/src/routes/ops.tsx:475)

ActionsPanel receives backend state and busy, but no observation age or paused flag. Paused or old snapshots still offer stop/fault/failback controls, which can act on outdated assumptions.

**Fix:** Display last successful observation time and disable state-sensitive actions until a fresh verification succeeds.

**Acceptance:** Age or pause a snapshot; disruptive actions require refreshed, current target state.

## CQ-112 · Disruptive scenario actions have no confirmation or scoped warning

**P2 · Polish · Source review**

Source: [web/src/routes/ops.tsx:659](../../../web/src/routes/ops.tsx:659)

Stop-instance, capability-fault and receipt-drop buttons call mutations directly. In a shared demo environment an accidental click can disrupt an active learner's assessment.

**Fix:** Add explicit target/context confirmation or a clearly armed rehearsal mode for disruptive controls.

**Acceptance:** A casual click cannot stop the selected backend without confirming the intended rehearsal target.

## CQ-113 · Typing an admin token immediately starts requests with partial values

**P2 · Defect · Source review**

Source: [web/src/routes/ops.tsx:184](../../../web/src/routes/ops.tsx:184)

Every keystroke is persisted and changes an enabled status-query key. The first nonempty character can leave the entry form for a loading view, and invalid partial tokens generate avoidable requests.

**Fix:** Use an unsaved input value plus an explicit Unlock action and one stable authenticated operator session.

**Acceptance:** Type a token normally; no request is sent until Unlock and input focus is not interrupted.

## CQ-114 · Global API throttling is too small for a classroom workload

**P2 · Demo limit · Source review**

Source: [infra/demo/lambda.tf:127](../../../infra/demo/lambda.tf:127)

The default stage allows three requests per second with burst six, shared by exam reads, autosaves, login and operations. This is a deliberately small demo configuration but simultaneous classroom activity can be throttled.

**Fix:** Document the supported rehearsal size and load-test a project-approved per-route capacity plan before classroom use.

**Acceptance:** A representative simultaneous start/autosave workload meets the stated limits and keeps recovery controls available.

## CQ-115 · The supported DynamoDB table has no recovery backup policy

**P1 · Hardening · Source review**

Source: [infra/demo/data.tf:1](../../../infra/demo/data.tf:1)

The table enables encryption and TTL but defines no point-in-time recovery or backup plan. EC2 failover protects application availability, while accidental deletion or bad writes have no configured recovery path here.

**Fix:** Define and verify data backup, restore and retention appropriate to the intended project use.

**Acceptance:** Restore an isolated copy from the configured backup mechanism and verify assessment/receipt integrity.

## CQ-116 · A frontend deployment can apply unrelated infrastructure changes

**P2 · Defect · Source review**

Source: [scripts/deploy_demo_web.sh:16](../../../scripts/deploy_demo_web.sh:16)

The demo-web script builds the frontend and then runs a full Terraform plan/apply for infra/demo. Unrelated pending changes, including instance-replacing user-data changes, can be included in an action named web deployment.

**Fix:** Separate frontend release state or require inspection of a concrete saved plan with explicit scope reporting.

**Acceptance:** A web-only release does not unexpectedly replace instances or alter runtime/monitor state.

## CQ-117 · Frontend releases delete assets needed by open browser tabs

**P2 · Hardening · Source review**

Source: [infra/demo/assets.tf:16](../../../infra/demo/assets.tf:16)

Terraform manages only files in the newest build and removes old content-hashed assets. Invalidation refreshes future shells, but an already open tab can later request an old lazy chunk that no longer exists.

**Fix:** Retain previous release assets for a compatibility window and use release-scoped deployment directories.

**Acceptance:** Keep a tab open through deployment and navigate to a lazy route; all referenced chunks still load.

## CQ-118 · Browser workflows are absent from continuous integration

**P2 · Verification gap · Source review**

Source: [.github/workflows/tests.yml:29](../../../.github/workflows/tests.yml:29)

CI runs typecheck, lint, unit tests and build but never installs/runs Playwright. All eight browser journeys passed locally in this audit, yet navigation and proxy regressions are not protected in CI.

**Fix:** Add a bounded browser job with retained failure traces and isolated test services.

**Acceptance:** A pull request that breaks sign-in or board navigation fails CI with an inspectable trace.

## CQ-119 · Authentication tests expire against the real calendar

**P1 · Defect · Reproduced (existing Go test suite)**

Source: [internal/auth/qol_test.go:30](../../../internal/auth/qol_test.go:30)

Two tests pin the service clock to October 11, 2026, while JWT validation and the memory store still use real time. On October 23 the race-enabled suite failed both tests despite the intended simulated timeline.

**Fix:** Inject one shared clock into token validation and all relevant stores, avoiding fixed-calendar validity assumptions.

**Acceptance:** Run the authentication tests at unrelated dates; token expiry and reset validity remain deterministic.

## CQ-120 · Classroom guidance contradicts the implemented exam access model

**P3 · Documentation · Source review**

Source: [docs/classrooms.md:20](../../../docs/classrooms.md:20)

The classroom guide says membership is ready to be threaded into quiz ownership later. The current HTTP API already enforces classroom assignment and learner membership, so developers are directed toward obsolete boundaries.

**Fix:** Update the guide to the current authorization model and explicitly document the remaining board sharing boundary.

**Acceptance:** Documentation examples agree with current tests for private exams, assigned exams and historical receipts.
