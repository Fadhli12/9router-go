### feat(mitm): record the real Antigravity client identity before overwriting it

The MITM handler replaces an intercepted Antigravity request's `User-Agent` and
`metadata.ideVersion` with the gateway's pinned identity. That overwrite is
deliberate, but it discarded the genuine fingerprint in the process — and that
fingerprint is the only thing a version bump can be built from.

`cl` in the CLI identity comes from the client's runtime config rather than its
binary: `strings` on the installed `agy` executable finds no `cl=` token at all.
So the pinned identity in `client_identity.go` cannot be bumped by editing the
version string — doing that produces a mixed fingerprint (new version, old `cl`)
which is plausibly more suspicious than being consistently stale. An
interception is the only place the real values are observable.

The handler now logs the client's `User-Agent`, the body's `ideVersion`, the
request URL and both currently pinned versions before the override runs. Warn
level, so it survives a default log configuration: a capture session is rare and
deliberate, and an operator who ran one must not discover afterwards that the
line was filtered away.

The capture sits outside the `isGeneration` branch on purpose. The catalog lane
(`fetchAvailableModels`) keeps the client's own identity rather than having it
replaced, so it carries the same fingerprint and is the cheaper request to
capture from.

`antigravityBodyIDEVersion` reads both payload shapes the clients send — top
level, and nested under `request` — matching what the overwrite path already
handles. Ten table-driven cases cover both shapes, which one wins when both are
present, and the malformed inputs that arrive off the wire: a non-string
version, a non-map `metadata`, a non-map `request`.

Capturing does not need a healthy account. The identity is in the *request*, so
a permanently banned account still sends the full fingerprint before Google
refuses it — and the sender is the genuine Antigravity client, which is ordinary
use rather than proxy use.
