### 🐛 Quota Tracker now shows 5h / weekly windows for Claude OAuth accounts (#293)

- Claude connections were listed in the tracker but never read: `fetchProviderUsage` had no
  `claude` case, so every account answered `{plan:"claude", quotas:{}}` and the dashboard
  printed "Account active. No quota limits tracked." without any upstream call.
- The dashboard now reads `GET api.anthropic.com/api/oauth/usage` (port of upstream
  `getClaudeUsage`) and renders `session (5h)`, `weekly (7d)` and model-scoped weekly rows
  (`weekly opus (7d)`, `weekly fable (7d)`, …) with used / remaining / resetAt.
- Reads are cached per token for 60s, a 429 puts the token on a cooldown (honouring
  `Retry-After`) and keeps serving the last good reading; a 401/403 goes through the existing
  refresh-and-retry path.
