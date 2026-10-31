### fix(oauth): handle device flow slow_down backoff and debounce polling

When authenticating providers via OAuth Device Flow (such as GitHub Copilot or Grok CLI):
- Polling previously dispatched immediately at T=0 right after initiating `deviceStart`, violating RFC 8628's minimum interval requirement and triggering upstream `slow_down` rate limits.
- Backend `githubPoll` and `grokcliPoll` ignored upstream `interval` adjustments in `slow_down` responses, discarding the requested cooldown window and keeping clients on default polling intervals, leading to compounding rate-limit penalties.
- Rapid button clicks on `Check now` or concurrent interval ticks fired overlapping polls without an in-flight guard.
- Modal displayed "Step 2: Paste callback URL here" for device flows despite device flows requiring no callback URL.

Fixes applied:
- Frontend omits immediate zero-millisecond poll in `openDeviceOAuth` and adds an in-flight guard (`isPollingDevice`) on polling and the `Check now` button.
- Backend extracts `interval` from upstream `slow_down` error responses in `githubPoll` and `grokcliPoll`, returning `{"status":"pending","interval":<n>}`.
- Frontend dynamically updates polling timer to respect upstream `slow_down` interval backoff.
- Modal hides manual callback URL input for device-code flows when a device user code is present.
