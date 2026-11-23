package providers

// Upstream ships one version constant per client family and derives every
// identity header from it (open-sse/providers/registry/codex.js CODEX_CLI_VERSION,
// open-sse/config/grokCli.js GROK_CLI_VERSION). These are the same two values,
// so the Go port has exactly one literal per family instead of one per call
// site — the previous layout hardcoded "0.2.99" and "0.2.93" in six files,
// which is how the grok-cli proxy ended up rejecting every request with
// HTTP 426 once cli-chat-proxy required 1.0.13.
const (
	// CodexCLIVersion is the codex CLI identity OpenAI's backend sees.
	CodexCLIVersion = "0.167.1"
	// CodexCLIUserAgent is the User-Agent header the codex backend expects.
	CodexCLIUserAgent = "codex_cli_rs/" + CodexCLIVersion
	// CodexCLIVersionHeader carries the same value in the `version` header,
	// which upstream sends alongside the User-Agent. Omitting it is what
	// leaves newer Codex models gated (upstream decolua/9router ca6e8407).
	CodexCLIVersionHeader = CodexCLIVersion

	// GrokCLIVersion is the @xai-official/grok identity cli-chat-proxy.grok.com
	// validates; anything below GrokCLIMinimumVersion is refused with HTTP 426.
	GrokCLIVersion = "1.0.44"
	// GrokCLIMinimumVersion is that floor, kept beside the shipped version so a
	// bump that reintroduces an older identity fails loudly instead of at
	// runtime against the proxy.
	GrokCLIMinimumVersion = "1.0.13"
	// GrokCLIUserAgent is the inference-path identity (grok-shell).
	GrokCLIUserAgent = "grok-shell/" + GrokCLIVersion + " (linux; x86_64)"
	// GrokCLIPagerUserAgent is the billing/probe identity. Upstream keeps a
	// separate pager UA because those calls go through the pager client, not
	// the inference one (open-sse/config/grokCli.js GROK_CLI_PAGER_USER_AGENT).
	GrokCLIPagerUserAgent = "grok-pager/" + GrokCLIVersion + " grok-shell/" + GrokCLIVersion + " (linux; x86_64)"

	// GrokCLIClientIdentifier is the x-grok-client-identifier sent by the
	// inference path; the pager paths below use GrokCLIPagerIdentifier.
	GrokCLIClientIdentifier = "grok-shell"
	// GrokCLIPagerIdentifier is the identifier the probe and OAuth paths send.
	GrokCLIPagerIdentifier = "grok-pager"

	// Antigravity client identity. Upstream decolua/9router moved off the
	// `agy` CLI lane entirely (PR #4816, open-sse/providers/shared.js): the
	// CLI fingerprint carries a runtime `cl=` token that only a live mitm
	// capture can observe and that moves on every CLI release, so every
	// release either cost a capture or drifted. The Hub desktop fingerprint
	// has no such token, and it is what the Argon lanes answer — the CLI lane
	// was adopted for the same reason (the 'ide' flavor 404'd on Argon) and
	// the Hub shape now serves both.
	//
	// The UA is upstream's captured Hub string verbatim
	// (open-sse/providers/shared.js ANTIGRAVITY_IDE_USER_AGENT). The earlier
	// "(os_type=windows; arch=amd64)" suffix was the CLI lane's metadata
	// grafted onto the Hub version: a string no real Hub or CLI client sends,
	// shared by every pooled account. It stays darwin/arm64 regardless of
	// the gateway host, and antigravity_project.go pins "platform": 2
	// (DARWIN_ARM64) so the body agrees with the header. Only the version
	// moves on a bump.
	AntigravityHubVersion   = "2.34.0"
	AntigravityCLIVersion   = AntigravityHubVersion
	AntigravityCLIUserAgent = "antigravity/hub/" + AntigravityHubVersion + " darwin/arm64"
	AntigravityIDEVersion   = AntigravityHubVersion
	// AntigravityCLIReleaseBaseline is the `agy` CLI stable version the
	// release watch compares the CLI auto-updater against. The updater only
	// reports the CLI line (1.5.x), so comparing it with the Hub pin (2.x)
	// could never report drift. Bump it after reviewing a new CLI release.
	AntigravityCLIReleaseBaseline = "1.5.14"
	// AntigravityIDEUserAgent is the hub shape on both lanes. Upstream's
	// agy 1.5.9 capture (decolua/9router#1220) belongs to the CLI lane this
	// fork no longer presents; the version the drift check compares against
	// is AntigravityHubVersion above.
	AntigravityIDEUserAgent = AntigravityCLIUserAgent
)
