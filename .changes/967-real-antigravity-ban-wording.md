### fix(resilience): detect the real Antigravity ban wording, not the guessed one

The suspension patterns were written from assumed phrasing and missed the
message an operator's accounts actually came back with:

> This service has been disabled in this account for violation of Terms of
> Service. Please submit an appeal to continue using this product.

Every entry looked for `account disabled` / `account suspended`, while the
upstream scopes the subject to the service — `disabled in this account`. The
halt therefore would not have fired on the exact event it was built for: the
rotation would have spent all 40 accounts the same way it did before.

Added the account-scoped forms (`disabled in this account`, `disabled for this
account`, and the suspended/banned/terminated variants). The message is pinned
verbatim in a test, at 403/400/401/402 and inside the JSON envelope the executor
passes, so a future edit to the pattern list cannot silently stop matching it.

The Terms-of-Service wording from that same message is deliberately **not** a
pattern. A content-moderation refusal uses identical vocabulary about a single
request — "your request was blocked for violation of Terms of Service", "if you
believe this is an error, submit an appeal" — and matching it would let one
blocked prompt halt the provider and take every healthy account with it. Five
such refusals are pinned as must-not-match.
