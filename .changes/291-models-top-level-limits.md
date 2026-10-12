### 🐛 `/v1/models` top-level `context_length` / `max_completion_tokens` now match `capabilities` (#291)

- Top-level limits came from the generic substring table (`claude-*` → 200000/8192) while
  `capabilities.contextWindow` / `maxOutput` carried the per-model values, so one entry
  contradicted itself (e.g. `cc/claude-opus-5-5`: 200000/8192 vs 1000000/128000). Clients that
  read the OpenAI-style fields — such as `pi-9router-ext` — capped Claude output at 8192.
- The top-level fields (and `/v1/models/info`) are now filled from the same chain as
  `capabilities` (declared custom limits → per-model caps → catalog → substring table); the
  substring table only fills gaps.
- `claude-fable-5-1` gets its documented 1M context / 128K output in the per-model table.
