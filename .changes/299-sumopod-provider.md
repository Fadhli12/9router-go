### Added
- **SumoPod provider**: `sumopod` registered as an API-key provider (bearer transport, OpenAI-compatible) pointing at `ai.sumopod.com/v1/chat/completions` with the `sp` short alias and a 61-model catalogue synced from the live `/v1/models` response (embeddings excluded). Live `/v1/models` import is wired via `modelsListURL` (bearer, like tokenharbor/dahl).
