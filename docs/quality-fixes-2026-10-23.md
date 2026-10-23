# 200 additional quality fixes — 23 October 2026

Baseline: `1ec26fb0b59cb4704ccce28b93fc4046da5a35f4`. These are additional to the 120-item historical audit in `docs/reports/continuum-audit-2026-10-23/`. This ledger includes behavioral defects, input/data-contract hardening, recovery edge cases, accessibility, and polish; it does not claim 200 production incidents or 200 independent root causes. Related failures are listed separately when they have different triggers or user-visible outcomes.

The historical audit remains a record of its baseline; this change does not mark all of its original findings resolved. The existing fixed-calendar auth tests (CQ-119) and fractional authoring-number checks (CQ-086) were also repaired as prerequisites and are not counted below. No AWS resources were changed.

## Fix register

| ID | Corrected behavior | Implementation |
| --- | --- | --- |
| QF-001 | Blank persisted user IDs created an authenticated session | [source](../web/src/lib/auth.ts) |
| QF-002 | Unknown persisted roles were silently changed to student | [source](../web/src/lib/auth.ts) |
| QF-003 | Non-text profile fields were coerced into misleading display strings | [source](../web/src/lib/auth.ts) |
| QF-004 | Partial token records produced authenticated pages without a usable session | [source](../web/src/lib/auth.ts) |
| QF-005 | Caller mutations silently changed the auth snapshot | [source](../web/src/lib/auth.ts) |
| QF-006 | Unrelated session-storage events reloaded local auth | [source](../web/src/lib/auth.ts) |
| QF-007 | Trailing slashes and whitespace in API origins produced malformed URLs | [source](../web/src/lib/api.ts) |
| QF-008 | Headers objects and tuple headers lost their contents | [source](../web/src/lib/api.ts) |
| QF-009 | Lowercase authorization headers were overwritten | [source](../web/src/lib/api.ts) |
| QF-010 | Public authentication requests carried the previous user's bearer token | [source](../web/src/lib/api.ts) |
| QF-011 | A rejected login retried through the old account's refresh token | [source](../web/src/lib/api.ts) |
| QF-012 | Malformed refresh responses replaced valid credentials | [source](../web/src/lib/api.ts) |
| QF-013 | Top-level API error codes disappeared | [source](../web/src/lib/api.ts) |
| QF-014 | Error-string responses lost their actionable message | [source](../web/src/lib/api.ts) |
| QF-015 | Successful 205 responses failed JSON parsing | [source](../web/src/lib/api.ts) |
| QF-016 | Array-shaped pending answers passed object validation | [source](../web/src/lib/pending-submission.ts) |
| QF-017 | Noncanonical saved question keys changed answer identity on reload | [source](../web/src/lib/pending-submission.ts) |
| QF-018 | Fractional or negative saved choices survived restoration | [source](../web/src/lib/pending-submission.ts) |
| QF-019 | Invalid pending attempt numbers escaped scope checks | [source](../web/src/lib/pending-submission.ts) |
| QF-020 | Blank generated request IDs created unusable retry records | [source](../web/src/lib/pending-submission.ts) |
| QF-021 | Invalid pending timestamps corrupted recovery ordering | [source](../web/src/lib/pending-submission.ts) |
| QF-022 | Pending records stored under a different attempt key were recovered | [source](../web/src/lib/pending-submission.ts) |
| QF-023 | An older attempt with a later timestamp hid a newer pending attempt | [source](../web/src/lib/pending-submission.ts) |
| QF-024 | Invalid retry counts ended without the expected final error | [source](../web/src/lib/pending-submission.ts) |
| QF-025 | Invalid retry delays overflowed browser timers | [source](../web/src/lib/pending-submission.ts) |
| QF-026 | A submit callback could mutate the next retry's payload | [source](../web/src/lib/pending-submission.ts) |
| QF-027 | HTTP 408 submission failures were not retried | [source](../web/src/lib/pending-submission.ts) |
| QF-028 | HTTP 500 submission failures were not retried | [source](../web/src/lib/pending-submission.ts) |
| QF-029 | Malformed nested draft questions crashed the builder | [source](../web/src/lib/quiz-drafts.ts) |
| QF-030 | Malformed optional draft settings reached rendering and API requests | [source](../web/src/lib/quiz-drafts.ts) |
| QF-031 | Duplicate stored draft IDs rendered duplicate controls | [source](../web/src/lib/quiz-drafts.ts) |
| QF-032 | Drafts saved in the same millisecond overwrote each other | [source](../web/src/lib/quiz-drafts.ts) |
| QF-033 | Blank bank identities made items impossible to target reliably | [source](../web/src/lib/question-bank.ts) |
| QF-034 | Invalid bank timestamps rendered invalid dates | [source](../web/src/lib/question-bank.ts) |
| QF-035 | Malformed option lists reached the question builder | [source](../web/src/lib/question-bank.ts) |
| QF-036 | Out-of-range saved correct answers could be inserted | [source](../web/src/lib/question-bank.ts) |
| QF-037 | Malformed saved scoring or feedback fields broke authoring | [source](../web/src/lib/question-bank.ts) |
| QF-038 | Blank room identities appeared in board history | [source](../web/src/lib/board-history.ts) |
| QF-039 | Invalid history timestamps broke sorting and date display | [source](../web/src/lib/board-history.ts) |
| QF-040 | Invalid visit counts rendered nonsensical history | [source](../web/src/lib/board-history.ts) |
| QF-041 | Duplicate saved board rows produced duplicate controls | [source](../web/src/lib/board-history.ts) |
| QF-042 | History was truncated before selecting the most recent visits | [source](../web/src/lib/board-history.ts) |
| QF-043 | Board history stayed stale when another tab changed it | [source](../web/src/lib/board-history.ts) |
| QF-044 | Visit counters could overflow safe integer precision | [source](../web/src/lib/board-history.ts) |
| QF-045 | History merge sorted timezone strings instead of instants | [source](../web/src/lib/board-history.ts) |
| QF-046 | Equal-time board rows had unstable ordering | [source](../web/src/lib/board-history.ts) |
| QF-047 | Downloads clicked a detached anchor | [source](../web/src/lib/board-history.ts) |
| QF-048 | Downloads revoked their blob before the browser consumed it | [source](../web/src/lib/board-history.ts) |
| QF-049 | Empty recent-room entries created broken navigation | [source](../web/src/lib/recent-rooms.ts) |
| QF-050 | Duplicate recent rooms displaced usable history | [source](../web/src/lib/recent-rooms.ts) |
| QF-051 | Recent-room changes in another tab were not reflected | [source](../web/src/lib/recent-rooms.ts) |
| QF-052 | Invalid question counts produced invalid restored positions or unbounded arrays | [source](../web/src/lib/quiz-progress.ts) |
| QF-053 | Invalid progress records were reported as successfully persisted | [source](../web/src/lib/quiz-progress.ts) |
| QF-054 | Invalid flag targets polluted question navigation | [source](../web/src/lib/quiz-progress.ts) |
| QF-055 | Unknown attempt statuses bypassed coherent status filtering | [source](../web/src/lib/assessment-history.ts) |
| QF-056 | Invalid assessment dates destabilized sorting | [source](../web/src/lib/assessment-history.ts) |
| QF-057 | Invalid retake counters enabled impossible retakes | [source](../web/src/lib/assessment-history.ts) |
| QF-058 | Email local parts with repeated or boundary dots were accepted | [source](../internal/auth/auth.go) |
| QF-059 | Invalid DNS labels were accepted as email domains | [source](../internal/auth/auth.go) |
| QF-060 | Overlength emails bypassed validation outside HTTP registration | [source](../internal/auth/auth.go) |
| QF-061 | The auth service itself allowed invalid email identities | [source](../internal/auth/auth.go) |
| QF-062 | Missing profile updates returned a successful null user | [source](../internal/auth/auth.go) |
| QF-063 | Token issuance panicked on a missing user | [source](../internal/auth/auth.go) |
| QF-064 | Unknown token types could be minted | [source](../internal/auth/auth.go) |
| QF-065 | Subsecond token lifetimes minted already expired credentials | [source](../internal/auth/auth.go) |
| QF-066 | Signed JWTs without expiration were accepted | [source](../internal/auth/auth.go) |
| QF-067 | JWTs issued in the future were accepted | [source](../internal/auth/auth.go) |
| QF-068 | Nil decoded tokens panicked | [source](../internal/auth/auth.go) |
| QF-069 | Whitespace-only JWT subjects passed subject validation | [source](../internal/auth/auth.go) |
| QF-070 | Fractional, negative or overflowing session versions were accepted | [source](../internal/auth/auth.go) |
| QF-071 | Bearer headers with valid tab separators failed parsing | [source](../internal/auth/auth.go) |
| QF-072 | Admin token comparison used variable-time equality | [source](../internal/auth/auth.go) |
| QF-073 | Generated fallback quiz IDs shared the high-order clock prefix | [source](../internal/httpapi/server.go) |
| QF-074 | Integer query parameters accepted trailing junk | [source](../internal/httpapi/server.go) |
| QF-075 | Submission timestamps were sorted lexicographically | [source](../internal/httpapi/server.go) |
| QF-076 | Unbounded caller request IDs were echoed into responses | [source](../internal/middleware/middleware.go) |
| QF-077 | Flushed responses could lose their correlation ID | [source](../internal/middleware/middleware.go) |
| QF-078 | Log fields could overwrite the event identity and timestamp | [source](../internal/middleware/middleware.go) |
| QF-079 | An unserializable log field silently erased the whole event | [source](../internal/middleware/middleware.go) |
| QF-080 | Expired rate-limit clients accumulated indefinitely | [source](../internal/middleware/middleware.go) |
| QF-081 | Retry-After rounded down and invited premature retry | [source](../internal/middleware/middleware.go) |
| QF-082 | Integer formatting corrupted the minimum integer | [source](../internal/middleware/middleware.go) |
| QF-083 | Invalid error-rate measurements broke health JSON or reported healthy | [source](../internal/health/deep.go) |
| QF-084 | Impossible used-disk values were accepted | [source](../internal/health/deep.go) |
| QF-085 | Invalid load measurements broke health JSON or reported healthy | [source](../internal/health/deep.go) |
| QF-086 | Cancellation during the final health probe still reported healthy | [source](../internal/health/deep.go) |
| QF-087 | The standard load parser accepted nonfinite and negative readings | [source](../internal/health/deep.go) |
| QF-088 | A canceled zero-latency request still reached the handler | [source](../internal/chaos/state.go) |
| QF-089 | An arbitrary Upgrade header bypassed injected faults | [source](../internal/chaos/state.go) |
| QF-090 | Database-stall simulation omitted retake writes | [source](../internal/chaos/state.go) |
| QF-091 | Informational HTTP statuses hid the final response status | [source](../internal/chaos/state.go) |
| QF-092 | Flushing before writing recorded a different final status | [source](../internal/chaos/state.go) |
| QF-093 | Loading previews had no status announcement | [source](../web/src/components/quiz-preview-dialog.tsx) |
| QF-094 | Preview failures were not announced | [source](../web/src/components/quiz-preview-dialog.tsx) |
| QF-095 | Preview options truncated the text teachers needed to check | [source](../web/src/components/quiz-preview-dialog.tsx) |
| QF-096 | Preview prompts collapsed intentional line breaks | [source](../web/src/components/quiz-preview-dialog.tsx) |
| QF-097 | Preview omitted question weights | [source](../web/src/components/quiz-preview-dialog.tsx) |
| QF-098 | Preview omitted feedback teachers needed to verify | [source](../web/src/components/quiz-preview-dialog.tsx) |
| QF-099 | Participant initials split Unicode surrogate pairs | [source](../web/src/components/board/presence-panel.tsx) |
| QF-100 | Large participant lists extended beyond the viewport | [source](../web/src/components/board/presence-panel.tsx) |
| QF-101 | Whitespace participant names appeared blank | [source](../web/src/components/board/presence-panel.tsx) |
| QF-102 | The participant list contained invalid separator markup | [source](../web/src/components/board/presence-panel.tsx) |
| QF-103 | Truncated participant names could not be inspected | [source](../web/src/components/board/presence-panel.tsx) |
| QF-104 | White cursor labels had poor contrast on bright backgrounds | [source](../web/src/components/board/peer-cursors.tsx) |
| QF-105 | Long cursor names could cover the canvas | [source](../web/src/components/board/peer-cursors.tsx) |
| QF-106 | Mobile layout disagreed with the media query at viewport boundaries | [source](../web/src/hooks/use-mobile.ts) |
| QF-107 | Connectivity changes between render and effect were missed | [source](../web/src/hooks/use-online-status.ts) |
| QF-108 | Restored pages kept stale connectivity state | [source](../web/src/hooks/use-online-status.ts) |
| QF-109 | Keyboard users had no way to skip repeated navigation | [source](../web/src/components/layout/app-shell.tsx) |
| QF-110 | Account avatars split Unicode surrogate pairs | [source](../web/src/components/layout/app-sidebar.tsx) |
| QF-111 | Sign-out retained private query results in memory | [source](../web/src/components/layout/app-sidebar.tsx) |
| QF-112 | Active navigation lacked an accessible current-page indicator | [source](../web/src/components/layout/app-sidebar.tsx) |
| QF-113 | Mobile navigation stayed open after choosing a page | [source](../web/src/components/layout/app-sidebar.tsx) |
| QF-114 | Initial client startup displayed a blank page | [source](../web/src/routes/__root.tsx) |
| QF-115 | Users with JavaScript disabled received no explanation | [source](../web/src/routes/__root.tsx) |
| QF-116 | Application animations ignored reduced-motion preferences | [source](../web/src/styles.css) |
| QF-117 | Malformed cached invitation metadata crashed roster rendering | [source](../web/src/lib/classrooms.ts) |
| QF-118 | Invalid cached class timestamps reached date rendering | [source](../web/src/lib/classrooms.ts) |
| QF-119 | Invalid cached member timestamps broke roster ordering | [source](../web/src/lib/classrooms.ts) |
| QF-120 | Classroom caches ignored changes made in other tabs | [source](../web/src/lib/classrooms.ts) |
| QF-121 | Accepting a cached invitation retained the invitation's identity instead of the learner's | [source](../web/src/lib/classrooms.ts) |
| QF-122 | An older classroom-list response could replace a newer list | [source](../web/src/lib/classrooms.ts) |
| QF-123 | An older roster response could replace a newer roster | [source](../web/src/lib/classrooms.ts) |
| QF-124 | An in-flight roster fetch undid a local membership mutation | [source](../web/src/lib/classrooms.ts) |
| QF-125 | Saving class details overwrote concurrently refreshed members | [source](../web/src/lib/classrooms.ts) |
| QF-126 | A late class update recreated a locally removed class | [source](../web/src/lib/classrooms.ts) |
| QF-127 | A sign-in change between preflight and mutation sent the mutation as the new user | [source](../web/src/lib/classrooms.ts) |
| QF-128 | Deleting a synced class missing from a limited catalog only deleted its local copy | [source](../web/src/lib/classrooms.ts) |
| QF-129 | Leaving a synced class missing from a limited catalog only removed it locally | [source](../web/src/lib/classrooms.ts) |
| QF-130 | An empty local join code matched an unsynced class | [source](../web/src/lib/classrooms.ts) |
| QF-131 | Imperative selection returned IDs of missing classes | [source](../web/src/lib/classrooms.ts) |
| QF-132 | Classroom hooks did not subscribe directly to identity changes | [source](../web/src/lib/classrooms.ts) |
| QF-133 | Invitation links had duplicate slashes for origins ending in a slash | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-134 | Roster initials split Unicode surrogate pairs | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-135 | Repeated class write clicks could create overlapping operations | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-136 | The join dialog promised invitation URLs but submitted the entire URL as a code | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-137 | Joining a class removed unrelated query parameters and router history state | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-138 | Class dialogs could close while a write was still pending | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-139 | The create-class dialog did not submit with Enter | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-140 | The edit-class dialog did not submit with Enter | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-141 | The join-class dialog did not submit with Enter | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-142 | The invitation dialog did not submit with Enter | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-143 | Class-name fields allowed values beyond the server contract | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-144 | Subject fields allowed values beyond the server contract | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-145 | Invitation names allowed values beyond the server contract | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-146 | Invitation emails allowed values beyond the server contract | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-147 | Teacher accounts attempted learner-only joins without an explanation | [source](../web/src/components/classroom-dashboard-real.tsx) |
| QF-148 | Catalog refreshes reset edits in an open settings dialog | [source](../web/src/components/teacher/edit-exam-dialog.tsx) |
| QF-149 | Repeated save clicks could overlap quiz-setting writes | [source](../web/src/components/teacher/edit-exam-dialog.tsx) |
| QF-150 | Invalid local datetimes silently cleared saved boundaries | [source](../web/src/components/teacher/edit-exam-dialog.tsx) |
| QF-151 | A late save response closed a different quiz's dialog | [source](../web/src/components/teacher/edit-exam-dialog.tsx) |
| QF-152 | Quiz settings could close through Escape while saving | [source](../web/src/components/teacher/edit-exam-dialog.tsx) |
| QF-153 | Quiz settings could not save through Enter | [source](../web/src/components/teacher/edit-exam-dialog.tsx) |
| QF-154 | A missing classroom option concealed the current assignment | [source](../web/src/components/teacher/edit-exam-dialog.tsx) |
| QF-155 | Repeated confirmation events could run a destructive action twice | [source](../web/src/components/confirm-action.tsx) |
| QF-156 | Duplicate CSV metadata headers silently selected the first value | [source](../web/src/lib/teacher-authoring.ts) |
| QF-157 | Extra CSV cells were silently discarded | [source](../web/src/lib/teacher-authoring.ts) |
| QF-158 | CSV scoring accepted hexadecimal and other unexpected number syntax | [source](../web/src/lib/teacher-authoring.ts) |
| QF-159 | A UTF-8 BOM prevented otherwise valid JSON imports | [source](../web/src/lib/teacher-authoring.ts) |
| QF-160 | Unbounded import text could freeze parsing and preview | [source](../web/src/lib/teacher-authoring.ts) |
| QF-161 | Spellcheck altered the presentation of pasted import data | [source](../web/src/components/teacher/question-import.tsx) |
| QF-162 | Empty imports offered an action that could only fail | [source](../web/src/components/teacher/question-import.tsx) |
| QF-163 | Loading another draft left obsolete server work queued | [source](../web/src/components/teacher/quiz-builder.tsx) |
| QF-164 | Undoing question removal could inject the question into a different draft | [source](../web/src/components/teacher/quiz-builder.tsx) |
| QF-165 | Rapidly inserting the same bank question produced duplicate React keys | [source](../web/src/components/teacher/quiz-builder.tsx) |
| QF-166 | Bank deduplication hid questions with distinct weights or feedback | [source](../web/src/components/teacher/quiz-builder.tsx) |
| QF-167 | Redirect targets containing backslashes or control characters passed the local-path check | [source](../web/src/routes/login.tsx) |
| QF-168 | Account-mode switches carried a visible password into the other mode | [source](../web/src/routes/login.tsx) |
| QF-169 | Malformed login responses were treated as successful authentication | [source](../web/src/routes/login.tsx) |
| QF-170 | Repeated authentication actions could overlap before React disabled them | [source](../web/src/routes/login.tsx) |
| QF-171 | Successful password resets retained the revoked browser session | [source](../web/src/routes/login.tsx) |
| QF-172 | Successful reset navigation left the used reset token in back history | [source](../web/src/routes/login.tsx) |
| QF-173 | Authentication tabs could change while a request was in flight | [source](../web/src/routes/login.tsx) |
| QF-174 | Repeated profile-save events could overlap updates | [source](../web/src/routes/settings.tsx) |
| QF-175 | Repeated password-change events could overlap updates | [source](../web/src/routes/settings.tsx) |
| QF-176 | Password changes retained private query results after sign-out | [source](../web/src/routes/settings.tsx) |
| QF-177 | CORS preflights did not explicitly allow bearer authorization | [source](../internal/httpapi/server.go) |
| QF-178 | Responses without an allowed Origin omitted the cache Vary key | [source](../internal/httpapi/server.go) |
| QF-179 | Cross-origin clients could not read Retry-After | [source](../internal/httpapi/server.go) |
| QF-180 | Null and non-object JSON bodies were accepted by object endpoints | [source](../internal/httpapi/server.go) |
| QF-181 | Oversized or control-bearing request IDs reached persistence | [source](../internal/exams/exams.go) |
| QF-182 | A missing service user could retrieve an exam | [source](../internal/exams/exams.go) |
| QF-183 | Negative autosave versions were reported as storage outages | [source](../internal/exams/exams.go) |
| QF-184 | Missing autosave request IDs were reported as storage outages | [source](../internal/exams/exams.go) |
| QF-185 | Reset-link query tokens could be sent in resource referrers | [source](../web/src/routes/__root.tsx) |
| QF-186 | An unconfigured router panicked instead of returning unavailable | [source](../internal/continuum/router.go) |
| QF-187 | Routing-state reads were outside the request timeout | [source](../internal/continuum/router.go) |
| QF-188 | Backend redirects bypassed the selected routing target | [source](../internal/continuum/router.go) |
| QF-189 | Requests with a nil body panicked in the router | [source](../internal/continuum/router.go) |
| QF-190 | Base64 request decoding allocated the entire oversized payload before checking its limit | [source](../internal/continuum/router.go) |
| QF-191 | Health probes followed redirects to unrelated destinations | [source](../internal/continuum/probe.go) |
| QF-192 | Health probes accepted trailing JSON documents or garbage | [source](../internal/continuum/probe.go) |
| QF-193 | Timed-out local-demo shutdown returned before closing its listeners | [source](../internal/continuum/local.go) |
| QF-194 | Display-name sender addresses were sent as invalid SMTP envelopes | [source](../internal/emaildelivery/smtp.go) |
| QF-195 | Unicode invitation subjects were sent without MIME encoding | [source](../internal/emaildelivery/smtp.go) |
| QF-196 | Unicode email bodies relied on unadvertised eight-bit transport support | [source](../internal/emaildelivery/smtp.go) |
| QF-197 | A failed analytics catalog had no retry action | [source](../web/src/routes/teacher.tsx) |
| QF-198 | A failed selected quiz trapped analytics without a selector or retry | [source](../web/src/routes/teacher.tsx) |
| QF-199 | An assessment with no submissions displayed a misleading zero-percent average | [source](../web/src/routes/teacher.tsx) |
| QF-200 | Assessment progress bars lacked accessible names | [source](../web/src/routes/teacher.tsx) |

## Verification

New regression coverage is in `web/src/lib/quality-regressions.test.tsx`, `web/src/lib/classrooms-quality.test.tsx`, `web/src/components/quality-regressions.test.tsx`, and the `quality_regression_test.go` files beside the changed Go packages. Existing workflow and route suites cover the UI changes.

- Go: `go test -race -timeout 30m ./...` and `go vet ./...`.
- Frontend: type checking, ESLint, production build, and 238 Vitest tests across 30 files.
- Browser journeys: eight Playwright tests against isolated local services.
- LaTeX: desktop compiler and Tectonic compilation; eight rendered pages inspected; all 200 IDs extracted in order; no text outside the checked page bounds.

The production build retains existing warnings about large chunks and route exports. Checks use local fixtures and services; they do not establish live AWS deployment or external SMTP-provider behavior.

The editable supplement is [main.tex](quality-fixes-report/main.tex); the compiled [PDF](../output/pdf/continuum-quality-fixes-2026-10-23.pdf) is included with this change. See [verification.json](quality-fixes-report/verification.json) for the check inventory.
