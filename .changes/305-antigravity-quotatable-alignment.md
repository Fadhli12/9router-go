### 🎨 fix(dashboard): align Antigravity quota with standard QuotaTable layout and restore row hiding

- **Background**: The 2x2 nested grid card introduced in #282 created an oversized,
  bloated card height on accounts with multiple Antigravity connections, diverged from
  the sleek layout of Codex and other providers, and dropped row-hiding controls (`onHideQuota`).
- **Refinement**: Reverted Antigravity rendering to the standard, compact `QuotaTable.svelte`
  component, preserving the 100% scale and 4 canonical window buckets while restoring
  row-hiding capabilities and uniform card sizing.
