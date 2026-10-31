### Effective connection status, orphan-model cleanup, and race-free OAuth refresh

Four fixes ported from `arewedaks/9router-go`, each one closing a class of
bug that a live deployment had already hit.

**A toggle is not a connection.** The dashboard counted a connection as active
whenever its `isActive` flag was 1, so a category read "33/33 active" while
every account in it was out of credit. `providers.EffectiveStatus` now derives
the verdict from the stored `testStatus`, any live `modelLock_*` cooldown, and
the stored `lastError`, with the same vocabulary the request path uses
(`active`, `unavailable`, `quota_exhausted`, `rate_limited`, `expired`,
`disabled`). `IsEffectivelyActive` is what the "N of M active" counters and the
`accountStatus=active` filter now consult. Two recovery paths are deliberately
refused: a spent credit budget never heals on a timer (`balance=0` stays
`quota_exhausted` long after its lock lapses), and a rejected credential never
heals at all (`invalid api key` stays `unavailable`). A per-minute throttle
still reads as `rate_limited` rather than a spent balance, because the advice
differs — one waits, the other tops up.

The request path gained the matching cooldown family: `QuotaExhaustedCooldownMs`
(1 hour) and a `Quota` flag on `ErrorRule`, so "insufficient balance" no longer
falls through to the 30-second transient default and re-dials a guaranteed-failed
upstream every half minute.

**Orphan custom models are no longer advertised.** A restored backup can carry
`customModels` rows pointing at provider node ids that exist in neither
`providerNodes` nor `providerConnections` — on the measured database, 1504 of
2057 rows pointed at 19 dead ids. `/v1/models` published them under their raw
generated key (`openai-compatible-chat-<uuid>/model`), which no client can
route: a request for one fails with `no active connections for provider`.
`ChatHandler.aliveAliases` now gates the loose-custom-model pass on a set built
from `KnownProviders`, `ProviderAliasMap`, configured node ids and their
prefixes, and every provider holding a connection. `/v1/models` dropped from
2069 to 562 entries on the reference database with no working route lost —
`gh`, `oc`, `cl` and `cbai` are aliases, not connections, and stay listed.

`providers.OutputAlias` and `NormalizeModelAlias` encode the upstream precedence
(prefix → static alias → raw id) so a compatible node's models export as
`bri/…` rather than `<uuid>/…`, and `IsGeneratedNodeID` inspects the trailing
UUID instead of splitting on the last dash (a uuid contains dashes, so the old
split never matched).

**OAuth refresh is serialised per connection.** Refresh tokens are single-use at
most providers, so two concurrent refreshes of the same connection — a chat
request and a "Test Connection" click landing in the same second — race on one
token; the loser gets `refresh_token_reused` and a healthy account is bricked
until re-login. The chat path had a per-connection `singleflight.Group` of its
own and the dashboard probe had none, so the two could not see each other.

`oauth.RunRefresh` is now the single entry point, with a generation counter that
tells a queued caller whether the winner already completed the work. It gives
each caller the fresh token on success, and on failure gives every caller the
error while each keeps the credential it arrived with — a shared flight must
never hand one caller's token to another. `WithRefreshLock` wraps a plain
per-connection mutex for callers that must hold the lock across a multi-step
sequence (the forced-refresh path). The dashboard probe's
`refreshConnectionToken` takes the same lock, so a manual test can no longer race
a live request.

**Re-signing an already-connected account no longer duplicates it.** Every
`save*Connection` handler inserted unconditionally, so re-running an OAuth flow
produced a second row for one account. That is not cosmetic: account fallback
walks connections in order and locks the failing one per model, so a duplicate
makes the router "fall back" to the same account's quota and lock it twice — the
production database carried exactly this for `codebuddy-intl`, two rows six days
apart sharing one JWT `sub`.

`IdentityOf` reads the identity from the credential, not the label, so a renamed
account is still recognised: GitHub keys off the immutable numeric user id
(falling back to login, then email), Antigravity off the Google account email,
CodeBuddy off the JWT `sub` claim, and any other JWT-bearing provider off its
`sub`. Providers without a stable identity (`kiro`, `cloudflare-ai`,
`openai-compatible-*`) are deliberately not deduplicated — many connections there
are legitimate.

The default is to refuse with a `409` carrying a `duplicate` object (existing id,
name, identity kind), so the UI can offer "update that account instead".
`replace: true` refreshes the existing connection in place, keeping its id and
priority and clearing its model locks — the old failure state described the old
token, so keeping it would leave a healthy account cooling down.

**Antigravity client profile.** `providers.AntigravityUserAgentFor` selects
between the IDE identity (`antigravity/<version>`) and the standalone CLI
identity (`antigravity/cli/<version> (aidev_client; …)`). The profile is
instance-wide rather than per-connection, so accounts cannot drift onto
different fingerprints. `NormalizeAntigravityClientProfile` still reads the
legacy `harness`/`sdk` spellings so previously stored connections keep working.

Verified: `go build ./...` clean; `go test ./internal/providers/
./internal/proxy/oauth/ ./internal/handlers/dashboard/ ./internal/handlers/chat/`
all pass. New tests pin the status vocabulary (15 cases), the helper pattern
tables, the `RunRefresh` collapse/keep-own-token contract under concurrency, the
duplicate identity extraction per provider, and the Antigravity profile
normalisation.
