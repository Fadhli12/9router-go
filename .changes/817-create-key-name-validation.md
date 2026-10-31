### fix(keys): validate the API key name on create, and repair two Windows-only test failures

Found while auditing the dashboard's Create API Key modal. Three changes, one
PR because they came out of one investigation and each is small.

**`POST /api/keys` now applies the same name rules as the edit dialog.**
`HandleCreateApiKey` stored the submitted `name` verbatim. Two consequences,
both reachable from the modal and from any scripted caller:

- A whitespace-only name (`"   "`) was stored as-is, so the key list rendered a
  row with a blank label. The modal blocks that case client-side, but the
  contract did not, and a caller that skipped the check got the blank row.
- A 250-character name was accepted. `maxAPIKeyNameLen` (200) existed and was
  enforced on `PUT /api/keys/{id}` — the rename path — but never on create, so
  the cap could be bypassed by naming a key too long rather than renaming one.
  The name rides along on every `/api/keys` listing and is rendered in the
  dashboard list, which is why the cap exists at all.

The handler now trims the name and refuses one past the limit, with the same
message the update path uses. Validation runs before the first write, so a
refused create leaves no row behind. An omitted or empty name is still accepted:
the name is operator-facing only and the row is identified by its secret.

**Two unit tests failed only on Windows, in the test rather than the code.**

`TestBrowserNamesCoverTheBrowsersTheFlowCanDrive` asserted the Linux executable
spellings on every platform, skipping only `google-chrome` on Windows. Brave
ships as `brave-browser` from the Linux package and as `brave.exe` from the
Windows installer — which `exec.LookPath` finds as `brave` — and Edge is
`microsoft-edge` on Linux against `msedge` on Windows. The expected names are
now per-platform, so the assertion is about the lookup list rather than about
which spelling the platform happens to use. The rewrite also drops a shadowed
loop variable that compared a string against a slice.

`TestCLIDetectors_ClaudeHas9Router` set only `HOME` before calling
`claudeHas9Router`, which reads the home directory through `os.UserHomeDir()`.
On Windows that resolves `USERPROFILE` first and falls back to `HOME`, so the
temp directory the test wrote `settings.json` into was never read and the
function looked at the real profile instead. Both affected subtests now set
`USERPROFILE` as well, matching what the `unconfigured` subtest in the same
table already did.

**Verified:** `TestHandleCreateApiKeyNameIsTrimmedAndCapped` (table-driven: plain
name, surrounding whitespace, whitespace-only, omitted, exactly at the limit,
one past the limit — the last asserting both the 400 and that no row was
written); `go test ./internal/handlers/dashboard/ ./internal/browserlogin/
./internal/handlers/media/ ./internal/db/` green after a rebase onto
`origin/main`; `go vet ./...` clean; and a smoke run against a live gateway on
:20130 confirming 201 chars → 400, 200 chars → 200, `"  Trimmed Name  "` →
`"Trimmed Name"`, `"   "` → empty.
