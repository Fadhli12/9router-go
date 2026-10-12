### ✨ feat(quota): 4-card clean layout & 100% scale for Antigravity quota

- **Background**: Antigravity quotas were computed against an artificial scale of `1000.0`,
  and displayed as flat model lists rather than the 4 canonical time-window quota buckets
  provided by Google Antigravity (`retrieveUserQuotaSummary`).
- **Backend**: Standardized Antigravity quotas to a pure 100.0 percentage scale with window
  metadata (`5h` and `weekly`).
- **Dashboard UI**: Introduced `AntigravityQuotaCard.svelte` rendering a 2x2 grid (Gemini 5h,
  Gemini Weekly, Claude/GPT 5h, Claude/GPT Weekly) with color-coded progress bars and countdown timers.
