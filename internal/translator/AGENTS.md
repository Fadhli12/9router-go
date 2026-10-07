# internal/translator — Protocol Format Converters

> Parent context: read root `AGENTS.md` (provider isolation §3, Go engineering §4, testing §5) before working here.

## Role

Pure data-transformation layer. Bidirectional conversion between LLM wire schemas — **no network calls, no database access, no side effects**.

| Area | Responsibility |
| :--- | :--- |
| OpenAI Chat Completions ↔ Anthropic Messages | `request.go`, `response.go`, `claude_*.go` |
| Google Gemini `GenerateContent` | `gemini.go`, `gemini_*.go` |
| Antigravity assist envelope | `antigravity.go` |
| OpenAI Responses API (stream + non-stream) | `responses_*.go` |
| Tool-call translation & fingerprint concealment | `claude_tool_*.go`, `fingerprint.go`, `kiro_tools.go` |
| Parameter sanitization | `sanitize.go`, `sanitizer.go` |

## Key Symbols

- `TranslateRequestToAnthropic`, `TranslateAnthropicToOpenAI` — Messages API transforms.
- `TranslateToGemini`, `TranslateGeminiToOpenAI` — Gemini content format transforms.
- `TranslateToAntigravity` — Cloud Code assist wrapper.
- `NewResponsesStreamTranslator` — transcodes standard chat SSE chunks into Responses API events.
- `SanitizeMessages`, `ConcealFingerprintTools` — strip provider fingerprints and unsupported attributes.

## Hard Rules (enforced by existing code & tests)

1. **Tool calls close the answer first** — they must always follow text content, never precede it (`responses_stream.go`).
2. **Close reasoning blocks before text deltas** for providers without explicit `</think>` tags (`responses_stream.go`).
3. **Never leak provider fingerprints or internal tool structures** downstream — always pass through `ConcealFingerprintTools`.
4. This package must stay **pure and dependency-free**: do not add HTTP/db imports. Handlers/proxy orchestrate; translator only converts.

## Conventions

- **Strongly typed structs** for known wire protocols; reserve `any`/`map[string]any` for genuinely dynamic provider payloads (root §4.C).
- Use `json.RawMessage` to forward opaque passthrough segments without unmarshaling (root §4.A).
- **Table-driven `testing.T`** tests, one `*_test.go` per concern (there are already ~30 test files here — follow their shape). No live network.

## Quick Checks

```bash
rtk go test ./internal/translator/...
```
