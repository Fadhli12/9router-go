### fix(dashboard): keep the API key mask the same length as the secret

- The mask collapsed everything between the first six and last four characters
  into a fixed run of bullets, so a 35-character key rendered as 11 characters.
  That told the reader two things it had no business knowing: that the secret was
  longer than ten characters, and roughly how much longer. A mask whose length
  tracks the thing it hides is not a mask — it is a length oracle.
- All four masks now preserve the exact length: `maskClientKey` (the value the
  API sends as `keyDisplay`), `keikey.Mask` (stored on rotate), `maskSecret`
  in the dashboard's key table, and `maskConnectionSecret` on the connection
  form. A revealed key and its mask occupy the same width, so pressing reveal
  no longer reflows the row or moves the buttons beside the value.
- `keikey.Mask` cut its plaintext by byte, not by rune — the same defect
  `maskClientKey` was fixed for in #362. A key ending in a multi-byte rune
  produced a mask holding invalid UTF-8, which `encoding/json/v2` rejects
  mid-encode: the 200 header is already on the wire, so the client receives a
  half-written body. It now shares `handlerutil.RunePrefix`/`RuneSuffix` with
  the other three.
- Short values still collapse to a fixed sentinel: there is nothing to show and
  nothing to measure. `keikey.Mask`'s `"***"` is kept for the same reason
  upstream keeps it, so existing no-key usage rows stay in one bucket instead of
  splitting.

**Verified.** `go build ./internal/...` and `go test ./internal/keikey/
./internal/handlers/dashboard/` pass. `TestMask` gained a rune-count assertion
and was mutation-checked: restoring the ellipsis form fails it. A multi-byte
case joins the table, because the rune-count assertion alone passes vacuously
on ASCII input — the byte-slicing bug only shows on a key ending in a
multi-byte rune.
