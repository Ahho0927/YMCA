<h1 align="center">YMCA</h1>

<p align="center">YNU LMS Management CLI Assistant</p>

A Go CLI for Yokohama National University's CoursePower LMS.

## Build and first use

Requires Go 1.26 or newer. Runtime binaries do not require Go or a browser.

```sh
go build -o ymca ./cmd/ymca
./ymca auth login
./ymca profile show
./ymca topics list
./ymca courses list --json
```

On Windows build `ymca.exe` instead. Login prompts for your YNU username and a hidden password. Off-campus, enter the code sent to your student email when prompted. There are no password/code command-line flags or automatic email access.

To read profile or topic data from saved home-page HTML without logging in:

```sh
./ymca profile show --file dashboard_ref.html
./ymca topics list --file dashboard_ref.html --json
```

`ymca` without arguments shows help and does not contact the LMS. The aggregate `dashboard` command has been removed. Use `profile show` for your name, role and previous login time, `topics list` for dates/titles/IDs of system announcements currently exposed on the LMS home page, `notices list` for notices, and `courses list` for courses. Session checks and maintenance state remain in `auth status` and `keepalive status`.

Profile JSON includes `authenticated_request`; offline profile output sets it to `false`. Offline topics are an array of parsed announcements; the `--file` option never verifies or imports the HTML file's session. Profile and Topics still read the authenticated home page internally, without fetching unrelated course or notice details. Courses preserve on-demand and communication-only entries, provisional registration and distinct IDs for courses with the same name, using the server-selected academic year and terms.

## Commands

```sh
ymca auth login
ymca auth status
ymca auth logout
ymca profile show
ymca topics list
ymca courses list --json
ymca courses COURSE show
ymca courses COURSE syllabus
ymca courses COURSE syllabus --url-only
ymca notices list
ymca notices NOTICE_ID show
ymca courses COURSE reports list
ymca courses COURSE reports REPORT_ID show
ymca courses COURSE materials list
ymca courses COURSE materials MATERIAL_ID show
ymca courses COURSE materials MATERIAL_ID download '*' --output ./downloads
ymca courses COURSE materials MATERIAL_ID download FILE_ID --output ./downloads
ymca courses COURSE materials '*' download --lesson "第１回" --output ./downloads
ymca courses COURSE materials '*' download --output ./downloads
ymca courses '*' materials '*' download '*' --output ./downloads
```

Replace `COURSE` with an internal `KOG...` ID or a seven-character lecture code such as `801105Z`. The grammar is `courses COURSE RESOURCE [ID] ACTION`: `materials` and `reports` always use plural names. Individual item IDs come before `show` or `download`; collections use `list` or `download --all`. Notices follow the same rule: `notices NOTICE_ID show`.

Course details show the lecture code, academic year, lesson descriptions, attendance labels and counts of materials/reports/other items. JSON preserves the existing full course-detail structure, including typed item rows. Reports and tests remain distinct; the CLI never starts or submits a test. Empty courses with the LMS's explicit “no registered lessons” message return an empty lesson list. CoursePower's AJAX content is loaded using the forms and `loadBlock` actions found on the page.

Notices include category, sender, publication time, expiry and unread state. Lists follow the LMS's JavaScript pagination; details isolate the notice body from navigation controls and expose attachment metadata. The global notice view does not expose a course-ID filter, so course-scoped notice lists are not supported.

Material lists show SRY material-group IDs and lesson names. `courses COURSE materials MATERIAL_ID show` resolves one group to its attached files. Put an attachment ID **after** `download` to select one file; a literal `'*'` selects all at that identifier position.

| Scope | Command |
|---|---|
| All materials in one course | `ymca courses COURSE materials '*' download` |
| All materials in one lesson | `ymca courses COURSE materials '*' download --lesson "第１回"` |
| All files in one SRY group | `ymca courses COURSE materials SRY_ID download '*'` |
| One attachment | `ymca courses COURSE materials SRY_ID download FILE_ID` |
| All materials in all current courses | `ymca courses '*' materials '*' download '*'` |

**Quote every `'*'` (or use `\*`).** Otherwise zsh/Bash expands it to local filenames before YMCA receives it. Only the exact literal `*` selects all; patterns such as `SRY*`, `?` or `[abc]` are not supported.

The legacy forms `materials download --all`, `materials SRY_ID download --all` and `materials SRY_ID download --file FILE_ID` remain compatible. A bound material ID or `'*'` with no file selector defaults to all its files. Do not combine a positional file selector with `--file`, or a specific file ID with `--all`. A concrete SRY ID and `--lesson` are alternative scopes; `materials '*' download --lesson NAME` is supported.

Use the **exact lesson name** from `courses COURSE show`, including Japanese full-width digits where present. `materials list --lesson "第１回"` previews that lesson's materials without fetching attachment details. Names are used because a stable per-lesson ID has not been verified; screen ordinals are not treated as permanent IDs. Missing or duplicate lesson names fail before downloading, rather than selecting another lesson. A valid lesson with no materials returns an empty result successfully.

Course and lesson bulk downloads use course-name/lesson subdirectories. Downloads of one SRY group in one course place files directly in `--output`, including with `download '*'`. When either the course or material selector is `'*'`, files are grouped under course-name/lesson subdirectories to keep courses separate. Duplicate or sanitized-equivalent course names receive numeric suffixes, such as `日本語` and `日本語 (1)`. Names are assigned from the whole current dashboard in course-ID order, so selecting one course by ID/code uses the same folder as selecting all courses. Existing folders are reused and existing files are preserved. File IDs, detail IDs and hidden fields come from the actual page's download handlers.

Unavailable materials/files and text-only materials without attachments are skipped with a `Warning:` on stderr containing the course/material/file IDs and reason. Remaining downloads continue, and these skips alone return exit status `0`, even if nothing could be saved. JSON records use `warning` for skipped items and `path` for saved files. A course that cannot be read during an all-course download is also skipped with a warning. Invalid selectors, unknown explicit IDs, a missing/ambiguous lesson, initial session/course-list access failure, cancellation and expired authentication still report errors; authentication expiry stops further requests and returns `3`.

Download filenames decode URL-encoded Japanese names and standard `filename*` headers. Long names are shortened on UTF-8 character boundaries while preserving the extension and leaving room for duplicate-name suffixes. If the response has no filename, the attachment name shown by LMS is used before falling back to the download URL.

Report lists read the actual report detail pages for submission deadlines and status, sorted by deadline. Details decode the report's escaped `subjectCon` instructions without executing JavaScript or including saved draft answers. Report availability (`available_until`, the LMS's 公開期限) is separate from submission deadlines (`deadline`). Unknown deadlines are `null`, never inferred from an availability limit or an automatic notice. Times such as `2026/10/21 24:00` normalize to `2026-10-22T00:00:00+09:00`. Unknown layouts remain explicit errors.

The old top-level `materials` and `assignments` commands have been replaced. They return exit code 2 with migration examples, rather than executing a second command grammar. Move the course argument immediately after `courses`; for example, `materials list --course KOG...` becomes `courses KOG... materials list`. The old `courses show COURSE_ID` and `courses syllabus COURSE_ID` orders also change to `courses COURSE show` and `courses COURSE syllabus`. The previous action-before-ID spellings for material/report/notice details and single-material downloads now return exit code 2 with an ID-before-action hint. JSON resource fields and exit codes remain unchanged.

There is no separate `items` command, singular `material`/`report` command, or course-wide `download` command. Additional status filters, Topics detail and notice attachment downloading are deferred. Topic summaries are available through `topics list`; notice attachment metadata remains available in `notices NOTICE_ID show`. The removed `dashboard` command returns a local migration message and exit code 2.

## Wildcard queries

```sh
ymca courses '*' show
ymca courses '*' materials list
ymca courses COURSE materials '*' show
ymca courses COURSE reports '*' show
ymca courses '*' reports list
ymca courses '*' reports '*' show
ymca notices '*' show
```

A course `'*'` means the distinct courses in the LMS's currently selected academic year/terms. It does not change the LMS selection or search every historical year. Material/report/notice `'*'` reads every matching detail; report detail queries include instructions, while `reports list` remains a summary. Explicit wildcard detail queries can make many requests. No submissions or test attempts are performed.

Single-ID JSON keeps its existing object shape. Wildcard queries return a flat array; an empty selection is `[]`. A concrete child ID below a course wildcard matches courses that contain that ID. Successful query items survive partial failures, with errors on stderr and a nonzero exit status. Downloads keep per-file success/warning records; all-course downloads also include `course_id` in each record. A missing explicit download target is an error when all required lists were read successfully. Authentication expiry stops subsequent requests and returns exit code 3. Duplicate course or material IDs are processed once.

## Course codes and cached lookup

```sh
ymca courses KOG0000000163774 materials list
ymca courses 801105Z materials list
```

The dashboard contains internal IDs, so the first code lookup automatically reads course entry pages to build the mapping. It does not fetch report details or attachments. Successful mappings are saved under the same private state lock and reused for the current selected academic year and current dashboard course membership. Course lists do not fetch details just to populate codes; they include codes already known from earlier requests.

All uncached current courses are checked before selecting a code, including after a match is found, to detect duplicates. Duplicate codes list the candidate internal IDs and require an explicit ID. A failed partial scan is reported as incomplete and can be retried; successful entries survive the failure. Codes are matched without case sensitivity. There are no user aliases or implicit default-course settings. Logging in again clears the mapping so a changed account cannot reuse it. Direct internal-ID commands do not trigger a full code scan. Lookup progress goes to stderr, including with `--json`.

## Syllabus lookup

```sh
ymca courses COURSE syllabus
ymca courses COURSE syllabus --json
ymca courses COURSE syllabus --url-only
ymca courses COURSE syllabus --year 2026
```

Dashboard cards contain the internal `KOG...` ID, not the seven-character public lecture code. The CLI enters that course and reads the final `[801105Z]`-style code from its breadcrumb. It uses the dashboard-selected academic year unless `--year` is provided, then reads the Japanese public syllabus. `--url-only` returns the unwrapped link without fetching its contents. There is no guessed conversion from KOG IDs to lecture codes; communication-only courses without a valid code return an explanatory error. The public academic portal is accessed with a separate cookie-free HTTP client.

## Shell completion

`completion` generates a script enabling Tab completion of YMCA commands and flags in your shell. It does not log in or contact the LMS. Completion understands the command sequence after a manually entered course ID/code, including wildcard selectors, positional file selectors and collection download flags; it does not fetch course or attachment IDs. `ymca courses COURSE materials --help` and `ymca help courses COURSE materials` also work without authentication. For the current zsh session:

```sh
source <(./ymca completion zsh)
```

Run `ymca completion --help` for Bash, zsh, fish and PowerShell setup instructions. Persisting completion in a shell profile is optional and is not performed by the CLI.

JSON fields use English names, LMS titles and body text remain unchanged, and dates include Japan's UTC+09:00 offset. Only result data goes to stdout; errors and warnings go to stderr. Exit statuses: `0` success (including downloads with skipped-item warnings), `1` request/parsing/setup/service failure, `2` invalid usage, `3` login required. Downloads preserve existing files with numeric suffixes, use temporary files and reject HTML login/error responses. An interrupted or incomplete file is removed. Individual download failures preserve successful files and become warnings; fatal errors such as expired authentication retain their nonzero exit status and `error` result records.

## Session maintenance

```sh
ymca keepalive enable
ymca keepalive status --json
ymca keepalive disable
# Alternative when a per-user service manager is unavailable:
ymca keepalive run
```

Build a permanent executable before `enable`; do not enable from `go run` or move/delete that executable afterward. The opt-in worker is installed as a macOS LaunchAgent, Linux systemd user service or Windows scheduled task at user logon. It does not use administrator privileges, change sleep settings or wake the computer. Native job definitions are generated and unit tested; installation/resume behavior also needs checking on each target OS.

The worker checks at startup, then requests the authenticated dashboard after 60 minutes without successful CLI activity. It polls the wall clock every 30 seconds to notice resume, and retries temporary network errors after one minute and five minutes before returning to hourly attempts. The computer needs network access. While asleep or powered off, **no requests are sent**. If the approximately two-hour idle timeout passes during sleep, resume may require login again. No exact expiry time or indefinite session lifetime is promised. Actual idle-timer extension needs the live comparison described below.

A detected expired session stops network probes until you log in again. Passwords are never automatically resubmitted. `auth logout` clears the local session and stops keepalive; it does not log your other browsers out of the university SSO.

Session files contain sensitive cookies/SIDs and live outside the repository by default:

- macOS: `~/Library/Application Support/YMCA`
- Linux: `$XDG_DATA_HOME/ymca` or `~/.local/share/ymca`
- Windows: `%AppData%\YMCA`

Directories/files use restrictive permissions (current-user ACLs on Windows). A shared process lock serializes CLI and worker requests, and state is replaced atomically. `--state-dir PATH` selects a separate private session, useful for development. There is one active account per state directory; only one OS-managed worker is installed per OS user.

## Private captures for completing parser support

```sh
./ymca --state-dir private/state --capture-dir private/capture auth login
./ymca --state-dir private/state --capture-dir private/capture courses COURSE show
./ymca --state-dir private/state --capture-dir private/capture notices list
```

Captures contain authenticated HTML and personal data. The original `dashboard.html`, `private/`, session files and build artifacts are excluded from Git. Never publish original captures, cookies, SID values, SAML payloads or credentials. Only opt-in captures are written; normal commands do not save HTML. Captures include AJAX fragments and use unique timestamped filenames so earlier captures are preserved. Capture occurs before parsing so unsupported pages can still be inspected locally. Fixtures committed under `internal/lms/testdata` contain synthetic identities and tokens.

The implementation does not guess undocumented report/material endpoints, change LMS language preferences, submit work, mark attendance, answer tests/surveys, or automatically fetch every course when reading a profile, topics or course list. Reading a detail page may have the same LMS read-history side effects as opening it in the browser.

## Verification and local releases

```sh
go test ./...
go vet ./...
python3 scripts/release.py --version 0.1.0
```

The release script builds macOS/Linux amd64 and arm64 and Windows amd64 ZIP files plus `dist/SHA256SUMS`. It includes only an executable and this README, never session files or raw HTML. It does not publish anything. CI runs tests and vet on all three operating systems.

Tests cover on/off-campus callback and SAML flows, explicit default HTTPS ports in the university's redirects, cookies and restored sessions, HTTP-200 login pages, dashboard parsing, virtual-clock keepalive behavior, concurrent state updates, download failures, course/lesson/SRY download scopes, missing/duplicate/empty lessons, profile/topic isolation and JSON/exit-code contracts. Network errors identify the login stage and distinguish blocked redirects, DNS failures, timeouts and certificate failures without exposing URLs or authentication payloads.

An optional public login-handoff check submits no credentials and runs only when explicitly enabled:

```sh
YNU_LIVE_PREFLIGHT=1 go test ./internal/lms -run '^TestPublicLoginHandoff$' -count=1 -v
```

Live verification: the user confirmed both on-campus login and off-campus email 2FA. The developer checked course details (including empty courses), AJAX loading of 249 notices, notice detail bodies, material attachments, a PDF download, report instructions/deadlines/status, and a full course-ID-to-public-syllabus lookup. Synthetic fixtures cover the same layouts without committing private data. Native job installation/resume on each OS and two isolated sessions comparing inactivity against keepalive for more than two hours still require validation. A server-enforced absolute session lifetime is respected even when keepalive is enabled.
