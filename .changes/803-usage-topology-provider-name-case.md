### fix(analytics): resolve custom provider node names on usage topology and breakdown table

Custom OpenAI-compatible provider nodes with mixed-case custom suffixes
(e.g. `openai-compatible-chat-Wildy`) displayed as raw synthetic ids truncated to
`openai-compat...` on the Usage Overview topology map and in the "Usage by Provider"
breakdown table instead of rendering their configured display name (`wildy.ai.id`).

The root cause was case-folding during provider deduplication and lookup:
- `AnalyticsView.svelte` lowercased the provider id (`targetId = cat?.id || canonical`),
  which caused the exact-case lookup in `nodeNameById` (keyed by the database's
  case-sensitive node id) to miss and fall back to the raw lowercased id.
- `usedInPeriod` evaluated `stats.byProvider` with only lowercased keys, causing
  traffic under mixed-case provider ids to evaluate as 0 and dropping active connections
  from the topology map.
- `UsageBreakdownTable.svelte` invoked `providerDisplayName(key)` without passing the
  `providerNodes` list, causing all custom node rows in the provider breakdown view to
  render raw synthetic ids.

Fixed by:
- Preserving the original provider id casing in `AnalyticsView.svelte` for custom nodes
  while maintaining case-insensitive deduplication for catalog providers.
- Checking both raw and canonical keys in `usedInPeriod` against `stats.byProvider`.
- Indexing `nodeNameById` by both exact and lowercased keys, with case-insensitive fallback.
- Updating `providerDisplayName` in `types.ts` to match node ids and catalog entries
  case-insensitively.
- Passing `providerNodes` from `AnalyticsView.svelte` into `UsageBreakdownTable.svelte`
  so custom compatible node labels resolve cleanly.
- Adding regression tests in `types.test.ts` for mixed-case custom node ids.
