# Product quality-of-life features

The assessment and classroom workflows now include the following features.

| Area | Behavior | Regression coverage |
| --- | --- | --- |
| Assessment setup | Duration, availability window, late grace, attempt limits, instructions, and classroom assignment | Teacher authoring, HTTP validation, classroom access tests |
| Start and resume | A lobby shows the rules before the learner starts the server timer; saved attempts resume explicitly | Quiz runner tests and browser assessment journey |
| Assessment history | Search, status/classroom filters, sorting, and Start/Resume/View result actions; receipts remain reachable after closing | History helper, HTTP catalog, and quiz runner tests |
| Classroom assignment | Assigned exams are available only to their teacher and enrolled learners; unassigned teacher exams are private | HTTP authorization tests and browser class assignment journey |
| Draft recovery | Local autosave, serialized server autosave, sync status, and unsaved navigation warnings | Teacher authoring tests, including deferred sync responses |
| Destructive actions | Confirmation for deletion, leaving classes, board restore, submission, and starting retakes; undo for removed questions/options | Confirmation and workflow tests |
| Published assessments | Teachers edit availability/instructions and reopen an exam without changing its graded questions | Teacher management and HTTP update tests |
| Invitations | Durable, email-bound invitations with retry-safe codes and explicit delivery status | Classroom service/HTTP/store tests |
| Classroom sync | Cached data is labeled honestly, failed mutations can retry, and classroom creation uses a stable request ID | Classroom store/dashboard tests |
| Classroom management | Rename/update descriptions, delete owned classes, remove members, and leave joined classes | Classroom dashboard and HTTP tests |
| Discovery | Search/filter/sort for assessments, rosters, and results | Authoring, history, and dashboard tests |
| Analytics | Teachers choose the assessment whose submissions and scores they want to inspect | Teacher analytics tests and browser workflow |
| Libraries | Searchable, paginated drafts and local/server question banks | Teacher library and backend bank tests |
| Question authoring | Reordering and validated JSON/CSV import | Import/reorder helper tests |
| Learner navigation | Flag questions, jump through flagged/unanswered questions, clear answers, and persist review state across reloads | Progress helper and runner tests |
| Results | Weighted feedback and copy/download/printable receipts | Receipt helper, runner, and browser tests |
| Shared boards | Teacher-controlled drawing permissions enforced by the server and retained across reconnects | Board manager/protocol/socket tests |
| Board history | Browse versions and restore an exact snapshot as a new version | Board history, persistence, and restore tests |
| Account settings | Profile editing, password visibility, password change, and single-use password recovery | Auth/store, account UI, and browser tests |

New assessments default to one attempt; an explicit limit of `0` allows
unlimited attempts. Retakes start only after confirmation, preserve earlier
receipts, and use separate answer/review state. When late work is enabled,
a grace value of `0` allows it without a cutoff; a positive value sets the
number of minutes after the closing time.

## Email setup

Password recovery and invitation email use an optional SMTP connection. Set
`SMTP_HOST`, `SMTP_FROM`, and `PUBLIC_BASE_URL`, plus `SMTP_PORT` (default `587`).
If authentication is required, set both `SMTP_USERNAME` and `SMTP_PASSWORD`.
Remote SMTP servers must support STARTTLS; loopback SMTP is supported for local
tests. Connections have a bounded deadline and respect request cancellation.

Without SMTP, classroom invitations still persist and expose a copyable code,
while the UI reports that email is not configured. Password recovery reports
that delivery is unavailable. Reset tokens are sent only through email, expire
after one hour, and can be used once. Password change/reset revokes existing
sessions and requires signing in again.

## Local verification

```sh
go test -race ./...
go vet ./...
make web-check
cd web
bunx playwright install chromium
bunx playwright test
```

Browser tests start their own memory-backed API and frontend on ports `18080`
and `18300`. Set `E2E_API_PORT` and `E2E_WEB_PORT` to override them. Recovery
tests start a separate local two-backend demo on ports `18480`–`18482` (override
the first with `E2E_CONTROL_PORT`). The board tests exercise the standalone
API's WebSocket server. The bounded two-backend HTTP recovery demo continues
to exclude board/WebSocket failover.
