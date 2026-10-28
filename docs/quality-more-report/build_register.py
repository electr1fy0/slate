"""Regenerate the additional-100 register and LaTeX appendix from final sources."""
from pathlib import Path
import hashlib
import json
from collections import Counter

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
BASELINE = "e218e12"
FILES = {
    "model": "internal/model/model.go",
    "manager": "internal/boards/manager.go",
    "persist": "internal/boards/persistence.go",
    "protocol": "internal/boards/protocol.go",
    "permissions": "internal/boards/permissions.go",
    "http": "internal/boards/http.go",
    "token": "web/src/lib/admin-token.ts",
    "sync": "web/src/hooks/use-board-sync.ts",
    "elements": "web/src/lib/board-elements.ts",
    "canvas": "web/src/components/board/board-canvas.tsx",
    "board": "web/src/routes/board.$roomId.tsx",
    "history": "web/src/components/board/board-history-dialog.tsx",
    "dialog": "web/src/components/ui/dialog.tsx",
    "alert": "web/src/components/ui/alert-dialog.tsx",
    "confirm": "web/src/components/confirm-action.tsx",
    "addon": "web/src/components/ui/input-group.tsx",
    "home": "web/src/routes/index.tsx",
    "rooms": "web/src/routes/board.index.tsx",
    "login": "web/src/routes/login.tsx",
    "settings": "web/src/routes/settings.tsx",
    "builder": "web/src/components/teacher/quiz-builder.tsx",
    "import": "web/src/components/teacher/question-import.tsx",
    "root": "web/src/routes/__root.tsx",
    "shell": "web/src/components/layout/app-shell.tsx",
    "class": "web/src/components/classroom-dashboard-real.tsx",
}

# Classification distinguishes observable behavior, boundary hardening and UX polish.
# Each row is one distinct trigger or user-facing outcome, not a claim of an
# independent root cause or production incident. Related rows can share a fix.
DATA = r"""
Behavior~model~func (q Question) MarshalJSON~Option zero disappears from authored JSON~Save or preview a question whose correct option is A; the answer field was absent.~Encode zero explicitly for authoring and use a separate redaction flag on learner reads.
Behavior~model~json:"score"~Zero scores disappear from catalog metadata~A submitted assessment scored zero serialized without its score field.~Keep numeric zero in the public metadata contract so submitted results stay distinguishable from missing data.
Polish~manager~func normaliseName~Whitespace-only board names~Join with spaces as a display name; presence showed an empty-looking participant.~Trim names before applying the Guest fallback and length limit.
Behavior~manager~if peer := r.peers[socket]~Duplicate socket registration leaves phantom peers~Connect the same socket twice; peer order contained it twice while the map retained one identity.~Return the existing peer ID without appending a second registration.
Hardening~manager~v := reflect.ValueOf(socket)~Typed nil transports pass connection validation~An integration passes a typed nil pointer as a socket; it was accepted and could panic on send.~Reject nil pointer, channel and interface transport values before registration.
Hardening~manager~func comparableKey~Non-comparable transport helpers panic~Pass a slice or map to peer lookup helpers after room creation; map indexing could panic.~Apply the comparable-socket guard consistently to public peer operations and exclusions.
Behavior~manager~func (m *Manager) ActiveRooms~Peerless rooms inflate active-room metrics~Export or absorb a room with no connected peers; status counted it as active.~Count only rooms with at least one attached peer.
Behavior~manager~func (m *Manager) Reset~Reset retains obsolete persistence errors~Reset local board state after a failed save; LastPersistenceError still reported the old incident.~Clear asynchronous error state together with rooms and pending saves.
Behavior~manager~if ctx.Err() != nil~Canceled broadcast removes healthy peers~Cancel a broadcast context before or during delivery; healthy recipients were detached as transport failures.~Stop the broadcast on context cancellation without evicting recipients.
Behavior~manager~func (m *Manager) disconnectIfStale~Heartbeat races with idle eviction~A peer sends fresh traffic after the sweep collects stale peers but before its turn is processed.~Recheck LastSeen under the room lock immediately before detaching the peer.
Behavior~manager~func cloneCollection~Typed collections alias board snapshots~Integrations pass typed point slices or nested maps; mutating input or returned snapshots could mutate live room state.~Recursively clone typed maps, slices and arrays while preserving their concrete types.
Hardening~manager~func versionValue~Invalid element revisions enter ordering~Negative, fractional, infinite or unsafe integer revisions could enter last-writer ordering and persistence.~Accept only finite, non-negative, safe integer revisions.
Behavior~persist~incomingNonce~Concurrent equal-version edits do not converge~Two valid Excalidraw edits share a version but differ in versionNonce; arrival order determined the stored winner.~Use the lower nonce as the deterministic tie breaker, matching canvas reconciliation.
Polish~protocol~alreadyJoined~Unchanged repeated joins generate redundant announcements~Identity refresh or another join frame announced the same unchanged peer again.~Send init on rejoin while broadcasting presence only for first joins or actual identity changes.
Hardening~protocol~s.closed.Load()~Detached sessions can process new frames~Call HandleFrame after Close or eviction; a stale session could still issue protocol operations.~Reject closed or no-longer-registered sessions before dispatch.
Behavior~protocol~context.WithoutCancel(ctx)~Canceled request suppresses peer departure~Close a session using a canceled request context; remaining peers missed peer-left.~Detach once and announce departure using a cleanup context independent of request cancellation.
Behavior~http~stopCancel :=~Canceled WebSocket requests keep blocking~Cancel the HTTP request while ReadMessage waits for a silent client; the handler and peer remained alive.~Close the connection when its request context ends and stop the callback during normal cleanup.
Hardening~persist~snapshot.RoomID !=~Snapshots can load into the wrong room~A backing adapter returns a snapshot with another room ID; Resume previously merged it.~Validate snapshot ownership before merging or restoring persisted content.
Hardening~persist~snapshot.SchemaVersion <~Unsupported snapshot schemas load silently~A future or invalid schema revision was treated like the current format.~Accept the current schema and legacy zero only, returning an explicit error for unsupported schemas.
Hardening~persist~snapshot.Version <~Invalid snapshot revisions damage room sequencing~Negative or maximum int64 snapshot versions can corrupt the next room revision.~Reject invalid or immediately overflowing persisted revisions before applying them.
Behavior~persist~err = ctx.Err()~Canceled loads become cached successes~A store ignores cancellation and returns successfully; Resume marked the room loaded anyway.~Check request cancellation after loading and leave the room eligible for a later successful resume.
Behavior~persist~saveCtx = flushContext~Explicit flush ignores its caller cancellation~A pending save retained its original writer context but also discarded a canceled flush caller.~Preserve writer identity values while honoring the flush caller's cancellation and deadline.
Hardening~persist~historical.Version != version~Restore accepts a different requested revision~LoadVersion returns a valid snapshot for another revision; the manager restored it without checking.~Compare the returned revision with the requested version before touching the live room.
Polish~permissions~if r.studentsDraw == allowed~No-op permission changes create history~Repeat the current students-draw setting; it incremented versions and wrote another snapshot.~Return without persistence or broadcast when the setting is unchanged.
Behavior~token~useSyncExternalStore~Admin token consumers disagree in one tab~Save a token from one mounted hook; other consumers kept their previous local state.~Use one subscribed external store and notify every mounted consumer on writes.
Behavior~token~window.addEventListener~Admin token changes do not cross tabs~Change or clear the token in another tab; existing consumers kept stale credentials.~Handle relevant local-storage events and full storage clears while ignoring other storage areas.
Behavior~token~if (ephemeral)~Blocked storage loses shared ephemeral credentials~A token write fails because storage is blocked; another component reading the token saw empty or old data.~Retain a shared in-memory fallback for the session and expose it consistently.
Hardening~sync~export function validBoardMessage~Malformed socket envelopes crash consumers~Valid JSON such as null, missing peers or non-array elements passed directly to callbacks.~Validate known message envelopes and ignore malformed frames before updating state.
Behavior~sync~socketRef.current !== socket~Queued old-socket callbacks affect a new room~A queued event arrives after room replacement or disposal; it could update the current callbacks and presence.~Check both disposal and socket identity before processing open, message and close events.
Behavior~sync~Could not open the board connection~WebSocket constructor failures crash the page~Browser policy or an invalid connection environment throws synchronously during socket construction.~Expose an unavailable state with a recoverable error instead of escaping the effect.
Behavior~sync~Could not send board changes~Synchronous send errors lose a drawing batch~A socket closes between readyState checking and send; the exception escaped without buffering work.~Catch send failure, close the failed transport and retain the element batch for reconnect.
Polish~sync~setStatus("reconnecting")~Disconnected presence remains visible~A connection closes but the participant list and self ID stayed visible until another init.~Clear disconnected presence immediately while reconnecting.
Polish~sync~const onOnline~Online recovery waits for exponential backoff~The browser regains connectivity while a reconnect timer is waiting.~Attempt reconnect immediately on the online event and remove the listener during cleanup.
Behavior~sync~export function boardMessageBatches~Large drawing batches exceed the frame limit~A normal gesture produces a multi-element batch larger than the server's 64 KiB limit and disconnects.~Split by encoded UTF-8 size with envelope headroom and explain an individually oversized element.
Hardening~canvas~const parsed: unknown~One corrupt cache row discards the local scene~A local snapshot mixes valid elements with null or malformed rows; the broad catch discarded everything.~Validate rows individually and recover the valid subset with normalized revision defaults.
Behavior~elements~export function mergeBoardElements~Duplicate cached IDs retain stale revisions~A cached scene has repeated IDs in a different revision order; the last array row could overwrite newer state.~Keep the newest element revision and deterministic nonce winner for each ID.
Behavior~board~mergeBoardElements(current~Board deltas arrive before the lazy canvas exists~An elements frame arrives after init while the canvas module is still loading; the optional callback dropped it.~Merge early deltas into the pending remote snapshot until the canvas can consume them.
Hardening~board~enabled: boardRealtimeEnabled~An empty room still opens a socket~A route normalizes to an empty room code; the error view appeared while its hook still connected.~Disable realtime transport for an empty room identifier.
Polish~board~if (exportLock.current)~Repeated export clicks create overlapping downloads~Click Export repeatedly while the persistence request is pending.~Use a synchronous export lock and a visible exporting state until completion.
Behavior~board~downloadTextFile(~Board JSON downloads revoke URLs immediately~The board export clicked an unattached anchor and immediately revoked its blob URL.~Reuse the attached-anchor download helper with delayed object-URL cleanup.
Polish~board~Could not download the board snapshot~Board download failures have no actionable feedback~Blob URL creation or a download operation throws; the async event handler rejected without guidance.~Catch download errors, release the pending state and show a retryable failure message.
Polish~board~sync.status === "unavailable"~Constructor failure leaves no retry control~Realtime is configured but socket construction fails; the unavailable state had no Retry now button.~Expose manual retry for recoverable unavailable connections as well as reconnecting ones.
Polish~board~aria-label={`Copy room code~Room-code button lacks an explicit copy action~The toolbar button's accessible name was only the code, leaving its purpose unclear.~Name the control with the copy action and complete room code.
Behavior~sync~current.send(JSON.stringify~Heartbeat send exceptions escape the timer~The socket closes during an interval heartbeat; send could throw outside the transport recovery path.~Catch heartbeat send failure and close the transport to enter reconnect handling.
Behavior~history~requestedScope~Late history restore replaces another room~Change rooms or user identity while restore is pending; its late response applied through the current callback.~Capture the requested room and user scope and ignore a response after either changes.
Polish~history~aria-label={`Restore version~History restore controls are indistinguishable~A screen-reader button list contains multiple controls all named Restore.~Include the target revision in each restore button's accessible name.
Polish~history~Save time unavailable~Malformed history timestamps show Invalid Date~A history adapter returns an invalid saved_at string.~Display a clear unavailable-time fallback instead of rendering Invalid Date.
Polish~dialog~max-h-[calc(100dvh~Standard dialogs exceed short viewports~Open a tall dialog with a short window or virtual keyboard; top or bottom content can be unreachable.~Bound popup height to the dynamic viewport and allow internal vertical scrolling.
Polish~dialog~gap-2 pr-8~Dialog headings overlap the close control~A long dialog title reaches the top-right close button.~Reserve header space for the close control.
Polish~alert~w-[calc(100%-2rem)]~Confirmations overflow very narrow screens~The alert popup's full width and fixed maximum can reach viewport edges at small widths.~Keep a viewport-relative width with consistent outer margins.
Polish~alert~max-h-[calc(100dvh~Long confirmations exceed the visible height~A confirmation has long text or an error on a short screen.~Bound its height and scroll its contents so actions remain reachable.
Polish~alert~overflow-wrap:anywhere~Unbroken names overflow confirmation titles~An item title contains a long token without spaces.~Allow long confirmation titles to wrap anywhere within the popup.
Polish~confirm~aria-busy={pending}~Pending confirmations are not communicated as busy~A destructive request waits while controls are disabled, without exposing busy state.~Mark the dialog busy and announce the changing confirmation action label politely.
Behavior~addon~input, textarea~Input-group addons do not focus textareas~Click a text addon next to an InputGroupTextarea; the helper searched only for input.~Focus either supported editable control.
Behavior~addon~onClick?.(e)~Custom addon click handlers suppress built-in focus~Passing onClick replaced the internal focus handler through prop spreading.~Compose the supplied callback with default focus behavior and respect preventDefault.
Polish~home~Retry assessments~Home catalog failures require a page refresh~The assessment catalog fails but the error state offers only explanatory text.~Add a catalog retry control that refetches the failed query.
Polish~home~Clear assessment filters~Empty assessment searches lack a reset action~Search and class/status filters produce zero results; recovery requires clearing several controls manually.~Reset search, status and classroom filters from the empty state.
Polish~rooms~Clear room filter~Empty room searches lack a reset action~A room filter yields no results with only text suggesting that it be cleared.~Provide a clear-filter action in the empty state.
Polish~home~Matching assessments~Filtered assessment counts are silent~Change filters while using assistive technology; the changing badge is not announced.~Expose the count as a named polite status.
Behavior~login~!event.nativeEvent.isComposing~IME confirmation can submit authentication~Press Enter to finish composing text in an authentication field; it also submits credentials.~Ignore Enter submission while the browser reports active composition.
Polish~login~id="forgot-email"~Recovery email fields enable unwanted typing assistance~On mobile, recovery email entry can receive capitalization or spellchecking behavior.~Disable capitalization, autocorrection and spellchecking for the recovery address.
Polish~login~aria-pressed={showPassword}~Password visibility controls do not expose state~Toggle password visibility in authentication or settings; only the label changes.~Expose the pressed state on all password visibility controls.
Polish~login~autoCorrect="off"~Visible passwords can receive text autocorrection~Show a password as text on mobile; default keyboard assistance can modify its contents.~Disable capitalization, correction and spellchecking on password inputs in both account flows.
Behavior~settings~disabled={profilePending}~Profile edits made during save are overwritten~Type another display name while an earlier profile update is pending; the response replaces that new edit.~Disable the profile input for its pending write.
Behavior~settings~disabled={passwordPending}~New credentials typed during a password change are erased~Edit password fields while the mutation waits; successful cleanup clears those newly typed values.~Disable both password inputs and visibility controls while the request is pending.
Polish~settings~const profileNameValid~Overlong profile names fail only after submission~Enter more than the server's 60-character profile limit with no field guidance.~Validate character count locally, explain the limit and disable invalid saves.
Behavior~settings~const previousProfile~Background profile refresh destroys unsaved names~A same-user identity refresh updates the canonical name while a local profile edit is in progress.~Update pristine values on refresh and preserve an independently edited local name.
Polish~settings~Use 8–72 UTF-8 bytes~Password instructions disagree with byte-based validation~The settings description promises eight characters while the actual policy measures UTF-8 bytes.~Explain the 8–72-byte range and how non-ASCII text affects it.
Polish~settings~aria-describedby="new-password-help"~Settings password validation lacks input semantics~A password field is visibly invalid but does not expose aria-invalid or its help text association.~Connect the live validation message to the input and expose its invalid state.
Polish~login~aria-describedby={~Authentication validation is only visual~Registration and reset fields show invalid styling without equivalent input semantics.~Expose invalid field states and associate password guidance with the relevant inputs.
Polish~builder~aria-describedby="quiz-window-help"~Builder validation is only visual~Invalid time, duration or attempt settings color their groups without exposing input-level errors.~Add invalid state and associated guidance to the affected question-builder controls.
Polish~builder~aria-label={`Question ${index + 1} prompt~Repeated prompts lack question context~A teacher navigates text fields for several questions; every prompt is named Prompt.~Include the question number in each prompt's accessible name.
Polish~builder~aria-pressed={optionIndex~Correct-answer controls hide their selected state~The current answer is conveyed through a visual badge and button color only.~Expose the selected correct option with aria-pressed.
Polish~builder~aria-label={`Question ${index + 1} option~Repeated option controls lack question context~Option A and answer-selection controls repeat across multiple question cards.~Include the question number in option input and correct-answer action names.
Polish~builder~aria-label={`Question ${index + 1} feedback~Repeated feedback fields lack question context~Several feedback inputs have the same accessible name.~Identify each feedback field by question number.
Polish~builder~aria-label={`Question ${index + 1} points~Repeated scoring fields lack question context~Several point inputs have the same accessible name.~Identify each points field by question number.
Polish~builder~aria-label={`Save question~Bank-save actions lack question context~A list of builder buttons contains several controls named Bank.~Name each save-to-bank action with its question number and purpose.
Polish~builder~aria-label={`Add option~Add-option actions lack question context~Multiple question cards expose identical Add option controls.~Include the receiving question number in each add-option action.
Polish~builder~aria-label={`Remove question ${index + 1} option~Remove-option actions lack question context~A screen-reader action list repeats Remove option A across question cards.~Identify both the question and option being removed.
Polish~builder~aria-label={`Load local draft~Local draft actions are indistinguishable~Several local rows expose identical Load controls.~Include the local draft title and storage context in the action name.
Polish~builder~aria-label={`Load workspace draft~Workspace draft actions are indistinguishable~Several remote rows expose identical Load controls.~Include the workspace draft title and storage context in the action name.
Polish~builder~aria-label={`Use bank question~Question-bank reuse actions are indistinguishable~Each bank row exposes a button named Use, without naming its question.~Include the question text in the reuse action name.
Polish~builder~draft.title || "Untitled draft"~Draft list titles are permanently truncated~Long local or workspace draft titles cannot be distinguished visually in their rows.~Wrap full draft titles, including authored line breaks.
Polish~builder~min-w-0 text-sm break-words whitespace-pre-wrap~Question-bank prompts hide distinguishing content~Long bank prompts are truncated, concealing differences between similar questions.~Wrap complete question text and preserve authored line breaks.
Polish~builder~Retry workspace drafts~Workspace draft failures have no retry control~Loading the remote drafts fails; only an error sentence is available.~Add a retry action while keeping local drafts available.
Polish~builder~Assigned classroom unavailable~Missing builder assignments appear unassigned~Load a draft assigned to a class outside the current classroom list; the select has no matching option.~Keep an explicit unavailable-assignment option for the selected classroom ID.
Polish~builder~0 means no limit~Duration zero conflicts with the builder's native minimum~The backend supports zero for unlimited duration but the builder sets its numeric minimum to one.~Align the numeric minimum and guidance with the supported zero value.
Hardening~builder~Number.isFinite(openInstant)~One invalid date can pass publish readiness~Only one time boundary is invalid; the previous window expression short-circuits as valid.~Validate each supplied date independently before enabling publication.
Polish~import~{index + 1}. {question.question}~Import preview collapses multiline prompts~A question prompt contains authored line breaks; preview collapses them.~Preserve prompt whitespace and wrap long words during review.
Polish~import~<ol className=~Import preview merges choices into ambiguous text~Options contain separators or line breaks; a joined paragraph obscures choice boundaries.~Render each option as a separate ordered list item with its answer marker.
Polish~import~Feedback: {question.feedback}~Import preview collapses feedback formatting~A feedback explanation contains multiple lines or a long token.~Preserve line breaks and wrap feedback within the preview.
Polish~import~aria-describedby="question-import-help"~Import format instructions are disconnected from inputs~Assistive technology reaches the format or source field without its required data-shape guidance.~Associate both controls with the format-specific instructions.
Behavior~import~imported.current = true~One reviewed import can be inserted twice~Two activations occur before the cleared preview rerenders; the same reviewed batch is appended twice.~Guard insertion synchronously and reset the guard for a new preview.
Polish~builder~Loading workspace drafts~Workspace draft loading has no announced status~The remote draft section shows only a skeleton while loading.~Give the loading region a named status for assistive technology.
Polish~builder~Loading question bank~Question-bank loading has no announced status~The empty local bank waits for its remote query with only a skeleton.~Give the bank loading region a named status.
Polish~root~<h1 className="text-7xl~The not-found page lacks a main landmark and heading~A user reaches an unknown route and cannot find a page heading or main landmark.~Use a main landmark and a real level-one 404 heading.
Polish~root~role="alert"~The rendering-error page lacks announced structure~A route rendering error shows plain paragraphs without alert semantics or a heading.~Use an alerting main region and a level-one error heading.
Polish~shell~<h1 title={title}~Header ellipsis hides complete page context~A narrow header truncates the title and description with no pointer-accessible disclosure.~Expose the full title and description through their title attributes.
Polish~class~{member.email ||~Roster rows hide full member identities~Names and emails in roster rows are ellipsized, concealing the distinguishing part.~Wrap complete member names and email addresses within their table cells.
Polish~class~{invitation.email}~Invitation cards hide full recipient identities~Long recipient names and addresses are truncated in pending invitation cards.~Wrap full recipient details so teachers can verify the invitation destination.
"""


def tex(value):
    replacements = {"\\": r"\textbackslash{}", "&": r"\&", "%": r"\%", "$": r"\$", "#": r"\#", "_": r"\_", "{": r"\{", "}": r"\}", "~": r"\textasciitilde{}", "^": r"\textasciicircum{}"}
    return "".join(replacements.get(char, char) for char in value)


def main():
    entries = []
    for number, row in enumerate(DATA.strip().splitlines(), 201):
        category, alias, anchor, title, trigger, correction = row.split("~")
        filename = FILES[alias]
        source = (ROOT / filename).read_text()
        # Source locations deliberately refer to the final implementation.
        if anchor not in source:
            raise ValueError(f"QF-{number}: source anchor missing: {filename}: {anchor}")
        line = source[:source.index(anchor)].count("\n") + 1
        entries.append({"id": f"QF-{number}", "category": category, "title": title, "trigger_and_before": trigger, "correction": correction, "status": "fixed", "source": {"file": filename, "line": line, "anchor": anchor, "sha256": hashlib.sha256(source.encode()).hexdigest()}})
    assert len(entries) == 100 and entries[-1]["id"] == "QF-300"
    counts = dict(Counter(entry["category"] for entry in entries))
    document = {"project": "Continuum / Slate", "baseline_commit": BASELINE, "date": "2026-10-23", "entries": 100, "categories": counts, "counting_policy": "Distinct correction entries, including behavior, boundary hardening and accessibility/polish. Related entries may share a root cause or implementation. These are not 100 independent production incidents. Previous CQ-001..120 and QF-001..200 are separate baselines.", "findings": entries}
    target = ROOT / "docs/quality-fixes-100-more-2026-10-23"
    target.with_suffix(".json").write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n")
    md = ["# 100 additional quality corrections", "", f"Baseline: `{BASELINE}`. Entries: **QF-201–QF-300**, all fixed in this batch.", "", document["counting_policy"], "", "Classification: " + ", ".join(f"{number} {category.lower()}" for category, number in counts.items()) + ".", "", "Verification and limits are in [the LaTeX supplement](quality-more-report/main.tex) and its `verification.json`. The earlier 120-item project audit and 200-item correction register remain historical; this batch does not assert that their unresolved findings are closed.", ""]
    for item in entries:
        evidence = item["source"]
        md.extend([f"## {item['id']} — {item['title']}", "", f"**{item['category']} · Fixed.** {item['trigger_and_before']}", "", f"**Correction:** {item['correction']}", "", f"**Source:** [{evidence['file']}:{evidence['line']}](../{evidence['file']}#L{evidence['line']}).", ""])
    target.with_suffix(".md").write_text("\n".join(md))
    appendix = []
    for item in entries:
        evidence = item["source"]
        appendix.append("\n".join([
            r"\begin{minipage}{\linewidth}",
            r"\textbf{" + item["id"] + " --- " + tex(item["title"]) + r"} {\scriptsize\color{accent}(" + item["category"] + r")}\par",
            r"\textit{Trigger.} " + tex(item["trigger_and_before"]) + r" \textit{Correction.} " + tex(item["correction"]) + r"\par",
            r"{\scriptsize\color{muted}\nolinkurl{" + evidence["file"] + "}:" + str(evidence["line"]) + r"}\par",
            r"\end{minipage}\par\vspace{7pt}",
        ]))
    (HERE / "latex/register.tex").write_text("% Generated by build_register.py; edit the DATA rows there.\n" + "\n".join(appendix))
    (HERE / "counts.json").write_text(json.dumps(counts, indent=2) + "\n")
    (HERE / "latex/classification.tex").write_text(r"""\begin{tabularx}{\linewidth}{@{}lrX@{}}
\toprule
Classification & Entries & Interpretation\\
\midrule
Behavioral corrections & 32 & Observable state, serialization, concurrency or recovery defects\\
Boundary hardening & 12 & Invalid transport or persisted data handled safely\\
Accessibility and polish & 56 & Clearer controls, readable content and recoverable interface states\\
\midrule
Total & 100 & Fixed entries QF-201--QF-300\\
\bottomrule
\end{tabularx}
""")
    print(json.dumps({"entries": len(entries), "categories": counts}))


if __name__ == "__main__":
    main()
