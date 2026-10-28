# 100 additional quality corrections

Baseline: `e218e12`. Entries: **QF-201–QF-300**, all fixed in this batch.

Distinct correction entries, including behavior, boundary hardening and accessibility/polish. Related entries may share a root cause or implementation. These are not 100 independent production incidents. Previous CQ-001..120 and QF-001..200 are separate baselines.

Classification: 32 behavior, 56 polish, 12 hardening.

Verification and limits are in [the LaTeX supplement](quality-more-report/main.tex) and its `verification.json`. The earlier 120-item project audit and 200-item correction register remain historical; this batch does not assert that their unresolved findings are closed.

## QF-201 — Option zero disappears from authored JSON

**Behavior · Fixed.** Save or preview a question whose correct option is A; the answer field was absent.

**Correction:** Encode zero explicitly for authoring and use a separate redaction flag on learner reads.

**Source:** [internal/model/model.go:93](../internal/model/model.go#L93).

## QF-202 — Zero scores disappear from catalog metadata

**Behavior · Fixed.** A submitted assessment scored zero serialized without its score field.

**Correction:** Keep numeric zero in the public metadata contract so submitted results stay distinguishable from missing data.

**Source:** [internal/model/model.go:123](../internal/model/model.go#L123).

## QF-203 — Whitespace-only board names

**Polish · Fixed.** Join with spaces as a display name; presence showed an empty-looking participant.

**Correction:** Trim names before applying the Guest fallback and length limit.

**Source:** [internal/boards/manager.go:128](../internal/boards/manager.go#L128).

## QF-204 — Duplicate socket registration leaves phantom peers

**Behavior · Fixed.** Connect the same socket twice; peer order contained it twice while the map retained one identity.

**Correction:** Return the existing peer ID without appending a second registration.

**Source:** [internal/boards/manager.go:168](../internal/boards/manager.go#L168).

## QF-205 — Typed nil transports pass connection validation

**Hardening · Fixed.** An integration passes a typed nil pointer as a socket; it was accepted and could panic on send.

**Correction:** Reject nil pointer, channel and interface transport values before registration.

**Source:** [internal/boards/manager.go:152](../internal/boards/manager.go#L152).

## QF-206 — Non-comparable transport helpers panic

**Hardening · Fixed.** Pass a slice or map to peer lookup helpers after room creation; map indexing could panic.

**Correction:** Apply the comparable-socket guard consistently to public peer operations and exclusions.

**Source:** [internal/boards/manager.go:618](../internal/boards/manager.go#L618).

## QF-207 — Peerless rooms inflate active-room metrics

**Behavior · Fixed.** Export or absorb a room with no connected peers; status counted it as active.

**Correction:** Count only rooms with at least one attached peer.

**Source:** [internal/boards/manager.go:361](../internal/boards/manager.go#L361).

## QF-208 — Reset retains obsolete persistence errors

**Behavior · Fixed.** Reset local board state after a failed save; LastPersistenceError still reported the old incident.

**Correction:** Clear asynchronous error state together with rooms and pending saves.

**Source:** [internal/boards/manager.go:374](../internal/boards/manager.go#L374).

## QF-209 — Canceled broadcast removes healthy peers

**Behavior · Fixed.** Cancel a broadcast context before or during delivery; healthy recipients were detached as transport failures.

**Correction:** Stop the broadcast on context cancellation without evicting recipients.

**Source:** [internal/boards/manager.go:486](../internal/boards/manager.go#L486).

## QF-210 — Heartbeat races with idle eviction

**Behavior · Fixed.** A peer sends fresh traffic after the sweep collects stale peers but before its turn is processed.

**Correction:** Recheck LastSeen under the room lock immediately before detaching the peer.

**Source:** [internal/boards/manager.go:665](../internal/boards/manager.go#L665).

## QF-211 — Typed collections alias board snapshots

**Behavior · Fixed.** Integrations pass typed point slices or nested maps; mutating input or returned snapshots could mutate live room state.

**Correction:** Recursively clone typed maps, slices and arrays while preserving their concrete types.

**Source:** [internal/boards/manager.go:626](../internal/boards/manager.go#L626).

## QF-212 — Invalid element revisions enter ordering

**Hardening · Fixed.** Negative, fractional, infinite or unsafe integer revisions could enter last-writer ordering and persistence.

**Correction:** Accept only finite, non-negative, safe integer revisions.

**Source:** [internal/boards/manager.go:697](../internal/boards/manager.go#L697).

## QF-213 — Concurrent equal-version edits do not converge

**Behavior · Fixed.** Two valid Excalidraw edits share a version but differ in versionNonce; arrival order determined the stored winner.

**Correction:** Use the lower nonce as the deterministic tie breaker, matching canvas reconciliation.

**Source:** [internal/boards/persistence.go:690](../internal/boards/persistence.go#L690).

## QF-214 — Unchanged repeated joins generate redundant announcements

**Polish · Fixed.** Identity refresh or another join frame announced the same unchanged peer again.

**Correction:** Send init on rejoin while broadcasting presence only for first joins or actual identity changes.

**Source:** [internal/boards/protocol.go:127](../internal/boards/protocol.go#L127).

## QF-215 — Detached sessions can process new frames

**Hardening · Fixed.** Call HandleFrame after Close or eviction; a stale session could still issue protocol operations.

**Correction:** Reject closed or no-longer-registered sessions before dispatch.

**Source:** [internal/boards/protocol.go:79](../internal/boards/protocol.go#L79).

## QF-216 — Canceled request suppresses peer departure

**Behavior · Fixed.** Close a session using a canceled request context; remaining peers missed peer-left.

**Correction:** Detach once and announce departure using a cleanup context independent of request cancellation.

**Source:** [internal/boards/protocol.go:229](../internal/boards/protocol.go#L229).

## QF-217 — Canceled WebSocket requests keep blocking

**Behavior · Fixed.** Cancel the HTTP request while ReadMessage waits for a silent client; the handler and peer remained alive.

**Correction:** Close the connection when its request context ends and stop the callback during normal cleanup.

**Source:** [internal/boards/http.go:122](../internal/boards/http.go#L122).

## QF-218 — Snapshots can load into the wrong room

**Hardening · Fixed.** A backing adapter returns a snapshot with another room ID; Resume previously merged it.

**Correction:** Validate snapshot ownership before merging or restoring persisted content.

**Source:** [internal/boards/persistence.go:712](../internal/boards/persistence.go#L712).

## QF-219 — Unsupported snapshot schemas load silently

**Hardening · Fixed.** A future or invalid schema revision was treated like the current format.

**Correction:** Accept the current schema and legacy zero only, returning an explicit error for unsupported schemas.

**Source:** [internal/boards/persistence.go:715](../internal/boards/persistence.go#L715).

## QF-220 — Invalid snapshot revisions damage room sequencing

**Hardening · Fixed.** Negative or maximum int64 snapshot versions can corrupt the next room revision.

**Correction:** Reject invalid or immediately overflowing persisted revisions before applying them.

**Source:** [internal/boards/persistence.go:718](../internal/boards/persistence.go#L718).

## QF-221 — Canceled loads become cached successes

**Behavior · Fixed.** A store ignores cancellation and returns successfully; Resume marked the room loaded anyway.

**Correction:** Check request cancellation after loading and leave the room eligible for a later successful resume.

**Source:** [internal/boards/persistence.go:297](../internal/boards/persistence.go#L297).

## QF-222 — Explicit flush ignores its caller cancellation

**Behavior · Fixed.** A pending save retained its original writer context but also discarded a canceled flush caller.

**Correction:** Preserve writer identity values while honoring the flush caller's cancellation and deadline.

**Source:** [internal/boards/persistence.go:350](../internal/boards/persistence.go#L350).

## QF-223 — Restore accepts a different requested revision

**Hardening · Fixed.** LoadVersion returns a valid snapshot for another revision; the manager restored it without checking.

**Correction:** Compare the returned revision with the requested version before touching the live room.

**Source:** [internal/boards/persistence.go:438](../internal/boards/persistence.go#L438).

## QF-224 — No-op permission changes create history

**Polish · Fixed.** Repeat the current students-draw setting; it incremented versions and wrote another snapshot.

**Correction:** Return without persistence or broadcast when the setting is unchanged.

**Source:** [internal/boards/permissions.go:29](../internal/boards/permissions.go#L29).

## QF-225 — Admin token consumers disagree in one tab

**Behavior · Fixed.** Save a token from one mounted hook; other consumers kept their previous local state.

**Correction:** Use one subscribed external store and notify every mounted consumer on writes.

**Source:** [web/src/lib/admin-token.ts:1](../web/src/lib/admin-token.ts#L1).

## QF-226 — Admin token changes do not cross tabs

**Behavior · Fixed.** Change or clear the token in another tab; existing consumers kept stale credentials.

**Correction:** Handle relevant local-storage events and full storage clears while ignoring other storage areas.

**Source:** [web/src/lib/admin-token.ts:15](../web/src/lib/admin-token.ts#L15).

## QF-227 — Blocked storage loses shared ephemeral credentials

**Behavior · Fixed.** A token write fails because storage is blocked; another component reading the token saw empty or old data.

**Correction:** Retain a shared in-memory fallback for the session and expose it consistently.

**Source:** [web/src/lib/admin-token.ts:23](../web/src/lib/admin-token.ts#L23).

## QF-228 — Malformed socket envelopes crash consumers

**Hardening · Fixed.** Valid JSON such as null, missing peers or non-array elements passed directly to callbacks.

**Correction:** Validate known message envelopes and ignore malformed frames before updating state.

**Source:** [web/src/hooks/use-board-sync.ts:39](../web/src/hooks/use-board-sync.ts#L39).

## QF-229 — Queued old-socket callbacks affect a new room

**Behavior · Fixed.** A queued event arrives after room replacement or disposal; it could update the current callbacks and presence.

**Correction:** Check both disposal and socket identity before processing open, message and close events.

**Source:** [web/src/hooks/use-board-sync.ts:191](../web/src/hooks/use-board-sync.ts#L191).

## QF-230 — WebSocket constructor failures crash the page

**Behavior · Fixed.** Browser policy or an invalid connection environment throws synchronously during socket construction.

**Correction:** Expose an unavailable state with a recoverable error instead of escaping the effect.

**Source:** [web/src/hooks/use-board-sync.ts:184](../web/src/hooks/use-board-sync.ts#L184).

## QF-231 — Synchronous send errors lose a drawing batch

**Behavior · Fixed.** A socket closes between readyState checking and send; the exception escaped without buffering work.

**Correction:** Catch send failure, close the failed transport and retain the element batch for reconnect.

**Source:** [web/src/hooks/use-board-sync.ts:142](../web/src/hooks/use-board-sync.ts#L142).

## QF-232 — Disconnected presence remains visible

**Polish · Fixed.** A connection closes but the participant list and self ID stayed visible until another init.

**Correction:** Clear disconnected presence immediately while reconnecting.

**Source:** [web/src/hooks/use-board-sync.ts:276](../web/src/hooks/use-board-sync.ts#L276).

## QF-233 — Online recovery waits for exponential backoff

**Polish · Fixed.** The browser regains connectivity while a reconnect timer is waiting.

**Correction:** Attempt reconnect immediately on the online event and remove the listener during cleanup.

**Source:** [web/src/hooks/use-board-sync.ts:313](../web/src/hooks/use-board-sync.ts#L313).

## QF-234 — Large drawing batches exceed the frame limit

**Behavior · Fixed.** A normal gesture produces a multi-element batch larger than the server's 64 KiB limit and disconnects.

**Correction:** Split by encoded UTF-8 size with envelope headroom and explain an individually oversized element.

**Source:** [web/src/hooks/use-board-sync.ts:81](../web/src/hooks/use-board-sync.ts#L81).

## QF-235 — One corrupt cache row discards the local scene

**Hardening · Fixed.** A local snapshot mixes valid elements with null or malformed rows; the broad catch discarded everything.

**Correction:** Validate rows individually and recover the valid subset with normalized revision defaults.

**Source:** [web/src/components/board/board-canvas.tsx:60](../web/src/components/board/board-canvas.tsx#L60).

## QF-236 — Duplicate cached IDs retain stale revisions

**Behavior · Fixed.** A cached scene has repeated IDs in a different revision order; the last array row could overwrite newer state.

**Correction:** Keep the newest element revision and deterministic nonce winner for each ID.

**Source:** [web/src/lib/board-elements.ts:14](../web/src/lib/board-elements.ts#L14).

## QF-237 — Board deltas arrive before the lazy canvas exists

**Behavior · Fixed.** An elements frame arrives after init while the canvas module is still loading; the optional callback dropped it.

**Correction:** Merge early deltas into the pending remote snapshot until the canvas can consume them.

**Source:** [web/src/routes/board.$roomId.tsx:86](../web/src/routes/board.$roomId.tsx#L86).

## QF-238 — An empty room still opens a socket

**Hardening · Fixed.** A route normalizes to an empty room code; the error view appeared while its hook still connected.

**Correction:** Disable realtime transport for an empty room identifier.

**Source:** [web/src/routes/board.$roomId.tsx:95](../web/src/routes/board.$roomId.tsx#L95).

## QF-239 — Repeated export clicks create overlapping downloads

**Polish · Fixed.** Click Export repeatedly while the persistence request is pending.

**Correction:** Use a synchronous export lock and a visible exporting state until completion.

**Source:** [web/src/routes/board.$roomId.tsx:142](../web/src/routes/board.$roomId.tsx#L142).

## QF-240 — Board JSON downloads revoke URLs immediately

**Behavior · Fixed.** The board export clicked an unattached anchor and immediately revoked its blob URL.

**Correction:** Reuse the attached-anchor download helper with delayed object-URL cleanup.

**Source:** [web/src/routes/board.$roomId.tsx:172](../web/src/routes/board.$roomId.tsx#L172).

## QF-241 — Board download failures have no actionable feedback

**Polish · Fixed.** Blob URL creation or a download operation throws; the async event handler rejected without guidance.

**Correction:** Catch download errors, release the pending state and show a retryable failure message.

**Source:** [web/src/routes/board.$roomId.tsx:179](../web/src/routes/board.$roomId.tsx#L179).

## QF-242 — Constructor failure leaves no retry control

**Polish · Fixed.** Realtime is configured but socket construction fails; the unavailable state had no Retry now button.

**Correction:** Expose manual retry for recoverable unavailable connections as well as reconnecting ones.

**Source:** [web/src/routes/board.$roomId.tsx:212](../web/src/routes/board.$roomId.tsx#L212).

## QF-243 — Room-code button lacks an explicit copy action

**Polish · Fixed.** The toolbar button's accessible name was only the code, leaving its purpose unclear.

**Correction:** Name the control with the copy action and complete room code.

**Source:** [web/src/routes/board.$roomId.tsx:242](../web/src/routes/board.$roomId.tsx#L242).

## QF-244 — Heartbeat send exceptions escape the timer

**Behavior · Fixed.** The socket closes during an interval heartbeat; send could throw outside the transport recovery path.

**Correction:** Catch heartbeat send failure and close the transport to enter reconnect handling.

**Source:** [web/src/hooks/use-board-sync.ts:207](../web/src/hooks/use-board-sync.ts#L207).

## QF-245 — Late history restore replaces another room

**Behavior · Fixed.** Change rooms or user identity while restore is pending; its late response applied through the current callback.

**Correction:** Capture the requested room and user scope and ignore a response after either changes.

**Source:** [web/src/components/board/board-history-dialog.tsx:90](../web/src/components/board/board-history-dialog.tsx#L90).

## QF-246 — History restore controls are indistinguishable

**Polish · Fixed.** A screen-reader button list contains multiple controls all named Restore.

**Correction:** Include the target revision in each restore button's accessible name.

**Source:** [web/src/components/board/board-history-dialog.tsx:104](../web/src/components/board/board-history-dialog.tsx#L104).

## QF-247 — Malformed history timestamps show Invalid Date

**Polish · Fixed.** A history adapter returns an invalid saved_at string.

**Correction:** Display a clear unavailable-time fallback instead of rendering Invalid Date.

**Source:** [web/src/components/board/board-history-dialog.tsx:80](../web/src/components/board/board-history-dialog.tsx#L80).

## QF-248 — Standard dialogs exceed short viewports

**Polish · Fixed.** Open a tall dialog with a short window or virtual keyboard; top or bottom content can be unreachable.

**Correction:** Bound popup height to the dynamic viewport and allow internal vertical scrolling.

**Source:** [web/src/components/ui/dialog.tsx:56](../web/src/components/ui/dialog.tsx#L56).

## QF-249 — Dialog headings overlap the close control

**Polish · Fixed.** A long dialog title reaches the top-right close button.

**Correction:** Reserve header space for the close control.

**Source:** [web/src/components/ui/dialog.tsx:86](../web/src/components/ui/dialog.tsx#L86).

## QF-250 — Confirmations overflow very narrow screens

**Polish · Fixed.** The alert popup's full width and fixed maximum can reach viewport edges at small widths.

**Correction:** Keep a viewport-relative width with consistent outer margins.

**Source:** [web/src/components/ui/alert-dialog.tsx:53](../web/src/components/ui/alert-dialog.tsx#L53).

## QF-251 — Long confirmations exceed the visible height

**Polish · Fixed.** A confirmation has long text or an error on a short screen.

**Correction:** Bound its height and scroll its contents so actions remain reachable.

**Source:** [web/src/components/ui/alert-dialog.tsx:53](../web/src/components/ui/alert-dialog.tsx#L53).

## QF-252 — Unbroken names overflow confirmation titles

**Polish · Fixed.** An item title contains a long token without spaces.

**Correction:** Allow long confirmation titles to wrap anywhere within the popup.

**Source:** [web/src/components/ui/alert-dialog.tsx:118](../web/src/components/ui/alert-dialog.tsx#L118).

## QF-253 — Pending confirmations are not communicated as busy

**Polish · Fixed.** A destructive request waits while controls are disabled, without exposing busy state.

**Correction:** Mark the dialog busy and announce the changing confirmation action label politely.

**Source:** [web/src/components/confirm-action.tsx:61](../web/src/components/confirm-action.tsx#L61).

## QF-254 — Input-group addons do not focus textareas

**Behavior · Fixed.** Click a text addon next to an InputGroupTextarea; the helper searched only for input.

**Correction:** Focus either supported editable control.

**Source:** [web/src/components/ui/input-group.tsx:64](../web/src/components/ui/input-group.tsx#L64).

## QF-255 — Custom addon click handlers suppress built-in focus

**Behavior · Fixed.** Passing onClick replaced the internal focus handler through prop spreading.

**Correction:** Compose the supplied callback with default focus behavior and respect preventDefault.

**Source:** [web/src/components/ui/input-group.tsx:58](../web/src/components/ui/input-group.tsx#L58).

## QF-256 — Home catalog failures require a page refresh

**Polish · Fixed.** The assessment catalog fails but the error state offers only explanatory text.

**Correction:** Add a catalog retry control that refetches the failed query.

**Source:** [web/src/routes/index.tsx:257](../web/src/routes/index.tsx#L257).

## QF-257 — Empty assessment searches lack a reset action

**Polish · Fixed.** Search and class/status filters produce zero results; recovery requires clearing several controls manually.

**Correction:** Reset search, status and classroom filters from the empty state.

**Source:** [web/src/routes/index.tsx:351](../web/src/routes/index.tsx#L351).

## QF-258 — Empty room searches lack a reset action

**Polish · Fixed.** A room filter yields no results with only text suggesting that it be cleared.

**Correction:** Provide a clear-filter action in the empty state.

**Source:** [web/src/routes/board.index.tsx:275](../web/src/routes/board.index.tsx#L275).

## QF-259 — Filtered assessment counts are silent

**Polish · Fixed.** Change filters while using assistive technology; the changing badge is not announced.

**Correction:** Expose the count as a named polite status.

**Source:** [web/src/routes/index.tsx:218](../web/src/routes/index.tsx#L218).

## QF-260 — IME confirmation can submit authentication

**Behavior · Fixed.** Press Enter to finish composing text in an authentication field; it also submits credentials.

**Correction:** Ignore Enter submission while the browser reports active composition.

**Source:** [web/src/routes/login.tsx:401](../web/src/routes/login.tsx#L401).

## QF-261 — Recovery email fields enable unwanted typing assistance

**Polish · Fixed.** On mobile, recovery email entry can receive capitalization or spellchecking behavior.

**Correction:** Disable capitalization, autocorrection and spellchecking for the recovery address.

**Source:** [web/src/routes/login.tsx:299](../web/src/routes/login.tsx#L299).

## QF-262 — Password visibility controls do not expose state

**Polish · Fixed.** Toggle password visibility in authentication or settings; only the label changes.

**Correction:** Expose the pressed state on all password visibility controls.

**Source:** [web/src/routes/login.tsx:462](../web/src/routes/login.tsx#L462).

## QF-263 — Visible passwords can receive text autocorrection

**Polish · Fixed.** Show a password as text on mobile; default keyboard assistance can modify its contents.

**Correction:** Disable capitalization, correction and spellchecking on password inputs in both account flows.

**Source:** [web/src/routes/login.tsx:245](../web/src/routes/login.tsx#L245).

## QF-264 — Profile edits made during save are overwritten

**Behavior · Fixed.** Type another display name while an earlier profile update is pending; the response replaces that new edit.

**Correction:** Disable the profile input for its pending write.

**Source:** [web/src/routes/settings.tsx:172](../web/src/routes/settings.tsx#L172).

## QF-265 — New credentials typed during a password change are erased

**Behavior · Fixed.** Edit password fields while the mutation waits; successful cleanup clears those newly typed values.

**Correction:** Disable both password inputs and visibility controls while the request is pending.

**Source:** [web/src/routes/settings.tsx:236](../web/src/routes/settings.tsx#L236).

## QF-266 — Overlong profile names fail only after submission

**Polish · Fixed.** Enter more than the server's 60-character profile limit with no field guidance.

**Correction:** Validate character count locally, explain the limit and disable invalid saves.

**Source:** [web/src/routes/settings.tsx:60](../web/src/routes/settings.tsx#L60).

## QF-267 — Background profile refresh destroys unsaved names

**Behavior · Fixed.** A same-user identity refresh updates the canonical name while a local profile edit is in progress.

**Correction:** Update pristine values on refresh and preserve an independently edited local name.

**Source:** [web/src/routes/settings.tsx:56](../web/src/routes/settings.tsx#L56).

## QF-268 — Password instructions disagree with byte-based validation

**Polish · Fixed.** The settings description promises eight characters while the actual policy measures UTF-8 bytes.

**Correction:** Explain the 8–72-byte range and how non-ASCII text affects it.

**Source:** [web/src/routes/settings.tsx:223](../web/src/routes/settings.tsx#L223).

## QF-269 — Settings password validation lacks input semantics

**Polish · Fixed.** A password field is visibly invalid but does not expose aria-invalid or its help text association.

**Correction:** Connect the live validation message to the input and expose its invalid state.

**Source:** [web/src/routes/settings.tsx:280](../web/src/routes/settings.tsx#L280).

## QF-270 — Authentication validation is only visual

**Polish · Fixed.** Registration and reset fields show invalid styling without equivalent input semantics.

**Correction:** Expose invalid field states and associate password guidance with the relevant inputs.

**Source:** [web/src/routes/login.tsx:433](../web/src/routes/login.tsx#L433).

## QF-271 — Builder validation is only visual

**Polish · Fixed.** Invalid time, duration or attempt settings color their groups without exposing input-level errors.

**Correction:** Add invalid state and associated guidance to the affected question-builder controls.

**Source:** [web/src/components/teacher/quiz-builder.tsx:975](../web/src/components/teacher/quiz-builder.tsx#L975).

## QF-272 — Repeated prompts lack question context

**Polish · Fixed.** A teacher navigates text fields for several questions; every prompt is named Prompt.

**Correction:** Include the question number in each prompt's accessible name.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1425](../web/src/components/teacher/quiz-builder.tsx#L1425).

## QF-273 — Correct-answer controls hide their selected state

**Polish · Fixed.** The current answer is conveyed through a visual badge and button color only.

**Correction:** Expose the selected correct option with aria-pressed.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1459](../web/src/components/teacher/quiz-builder.tsx#L1459).

## QF-274 — Repeated option controls lack question context

**Polish · Fixed.** Option A and answer-selection controls repeat across multiple question cards.

**Correction:** Include the question number in option input and correct-answer action names.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1447](../web/src/components/teacher/quiz-builder.tsx#L1447).

## QF-275 — Repeated feedback fields lack question context

**Polish · Fixed.** Several feedback inputs have the same accessible name.

**Correction:** Identify each feedback field by question number.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1530](../web/src/components/teacher/quiz-builder.tsx#L1530).

## QF-276 — Repeated scoring fields lack question context

**Polish · Fixed.** Several point inputs have the same accessible name.

**Correction:** Identify each points field by question number.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1509](../web/src/components/teacher/quiz-builder.tsx#L1509).

## QF-277 — Bank-save actions lack question context

**Polish · Fixed.** A list of builder buttons contains several controls named Bank.

**Correction:** Name each save-to-bank action with its question number and purpose.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1411](../web/src/components/teacher/quiz-builder.tsx#L1411).

## QF-278 — Add-option actions lack question context

**Polish · Fixed.** Multiple question cards expose identical Add option controls.

**Correction:** Include the receiving question number in each add-option action.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1496](../web/src/components/teacher/quiz-builder.tsx#L1496).

## QF-279 — Remove-option actions lack question context

**Polish · Fixed.** A screen-reader action list repeats Remove option A across question cards.

**Correction:** Identify both the question and option being removed.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1479](../web/src/components/teacher/quiz-builder.tsx#L1479).

## QF-280 — Local draft actions are indistinguishable

**Polish · Fixed.** Several local rows expose identical Load controls.

**Correction:** Include the local draft title and storage context in the action name.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1117](../web/src/components/teacher/quiz-builder.tsx#L1117).

## QF-281 — Workspace draft actions are indistinguishable

**Polish · Fixed.** Several remote rows expose identical Load controls.

**Correction:** Include the workspace draft title and storage context in the action name.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1202](../web/src/components/teacher/quiz-builder.tsx#L1202).

## QF-282 — Question-bank reuse actions are indistinguishable

**Polish · Fixed.** Each bank row exposes a button named Use, without naming its question.

**Correction:** Include the question text in the reuse action name.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1300](../web/src/components/teacher/quiz-builder.tsx#L1300).

## QF-283 — Draft list titles are permanently truncated

**Polish · Fixed.** Long local or workspace draft titles cannot be distinguished visually in their rows.

**Correction:** Wrap full draft titles, including authored line breaks.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1106](../web/src/components/teacher/quiz-builder.tsx#L1106).

## QF-284 — Question-bank prompts hide distinguishing content

**Polish · Fixed.** Long bank prompts are truncated, concealing differences between similar questions.

**Correction:** Wrap complete question text and preserve authored line breaks.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1292](../web/src/components/teacher/quiz-builder.tsx#L1292).

## QF-285 — Workspace draft failures have no retry control

**Polish · Fixed.** Loading the remote drafts fails; only an error sentence is available.

**Correction:** Add a retry action while keeping local drafts available.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1254](../web/src/components/teacher/quiz-builder.tsx#L1254).

## QF-286 — Missing builder assignments appear unassigned

**Polish · Fixed.** Load a draft assigned to a class outside the current classroom list; the select has no matching option.

**Correction:** Keep an explicit unavailable-assignment option for the selected classroom ID.

**Source:** [web/src/components/teacher/quiz-builder.tsx:926](../web/src/components/teacher/quiz-builder.tsx#L926).

## QF-287 — Duration zero conflicts with the builder's native minimum

**Polish · Fixed.** The backend supports zero for unlimited duration but the builder sets its numeric minimum to one.

**Correction:** Align the numeric minimum and guidance with the supported zero value.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1003](../web/src/components/teacher/quiz-builder.tsx#L1003).

## QF-288 — One invalid date can pass publish readiness

**Hardening · Fixed.** Only one time boundary is invalid; the previous window expression short-circuits as valid.

**Correction:** Validate each supplied date independently before enabling publication.

**Source:** [web/src/components/teacher/quiz-builder.tsx:796](../web/src/components/teacher/quiz-builder.tsx#L796).

## QF-289 — Import preview collapses multiline prompts

**Polish · Fixed.** A question prompt contains authored line breaks; preview collapses them.

**Correction:** Preserve prompt whitespace and wrap long words during review.

**Source:** [web/src/components/teacher/question-import.tsx:147](../web/src/components/teacher/question-import.tsx#L147).

## QF-290 — Import preview merges choices into ambiguous text

**Polish · Fixed.** Options contain separators or line breaks; a joined paragraph obscures choice boundaries.

**Correction:** Render each option as a separate ordered list item with its answer marker.

**Source:** [web/src/components/teacher/question-import.tsx:149](../web/src/components/teacher/question-import.tsx#L149).

## QF-291 — Import preview collapses feedback formatting

**Polish · Fixed.** A feedback explanation contains multiple lines or a long token.

**Correction:** Preserve line breaks and wrap feedback within the preview.

**Source:** [web/src/components/teacher/question-import.tsx:166](../web/src/components/teacher/question-import.tsx#L166).

## QF-292 — Import format instructions are disconnected from inputs

**Polish · Fixed.** Assistive technology reaches the format or source field without its required data-shape guidance.

**Correction:** Associate both controls with the format-specific instructions.

**Source:** [web/src/components/teacher/question-import.tsx:65](../web/src/components/teacher/question-import.tsx#L65).

## QF-293 — One reviewed import can be inserted twice

**Behavior · Fixed.** Two activations occur before the cleared preview rerenders; the same reviewed batch is appended twice.

**Correction:** Guard insertion synchronously and reset the guard for a new preview.

**Source:** [web/src/components/teacher/question-import.tsx:43](../web/src/components/teacher/question-import.tsx#L43).

## QF-294 — Workspace draft loading has no announced status

**Polish · Fixed.** The remote draft section shows only a skeleton while loading.

**Correction:** Give the loading region a named status for assistive technology.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1156](../web/src/components/teacher/quiz-builder.tsx#L1156).

## QF-295 — Question-bank loading has no announced status

**Polish · Fixed.** The empty local bank waits for its remote query with only a skeleton.

**Correction:** Give the bank loading region a named status.

**Source:** [web/src/components/teacher/quiz-builder.tsx:1283](../web/src/components/teacher/quiz-builder.tsx#L1283).

## QF-296 — The not-found page lacks a main landmark and heading

**Polish · Fixed.** A user reaches an unknown route and cannot find a page heading or main landmark.

**Correction:** Use a main landmark and a real level-one 404 heading.

**Source:** [web/src/routes/__root.tsx:62](../web/src/routes/__root.tsx#L62).

## QF-297 — The rendering-error page lacks announced structure

**Polish · Fixed.** A route rendering error shows plain paragraphs without alert semantics or a heading.

**Correction:** Use an alerting main region and a level-one error heading.

**Source:** [web/src/routes/__root.tsx:76](../web/src/routes/__root.tsx#L76).

## QF-298 — Header ellipsis hides complete page context

**Polish · Fixed.** A narrow header truncates the title and description with no pointer-accessible disclosure.

**Correction:** Expose the full title and description through their title attributes.

**Source:** [web/src/components/layout/app-shell.tsx:44](../web/src/components/layout/app-shell.tsx#L44).

## QF-299 — Roster rows hide full member identities

**Polish · Fixed.** Names and emails in roster rows are ellipsized, concealing the distinguishing part.

**Correction:** Wrap complete member names and email addresses within their table cells.

**Source:** [web/src/components/classroom-dashboard-real.tsx:691](../web/src/components/classroom-dashboard-real.tsx#L691).

## QF-300 — Invitation cards hide full recipient identities

**Polish · Fixed.** Long recipient names and addresses are truncated in pending invitation cards.

**Correction:** Wrap full recipient details so teachers can verify the invitation destination.

**Source:** [web/src/components/classroom-dashboard-real.tsx:873](../web/src/components/classroom-dashboard-real.tsx#L873).
