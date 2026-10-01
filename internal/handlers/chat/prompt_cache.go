package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"
)

// PromptPrefixAnalyzer analyzes chat messages to extract and hash the prefix
// (system instructions, tool definitions, initial history) for prompt cache affinity.
type PromptPrefixAnalysis struct {
	PrefixEndIdx int
	PrefixHash   string
	PrefixType   string
	Confidence   float64
}

// In-memory LRU-like mapping for prompt cache affinity: PrefixHash -> ConnectionID
type promptCacheRegistry struct {
	mu      sync.RWMutex
	entries map[string]promptCacheEntry
}

type promptCacheEntry struct {
	connectionID string
	updatedAt    time.Time
}

var globalPromptCacheRegistry = &promptCacheRegistry{
	entries: make(map[string]promptCacheEntry),
}

// SetPromptCacheAffinity associates a prefix hash with a winning connection ID.
func SetPromptCacheAffinity(prefixHash, connID string) {
	if prefixHash == "" || connID == "" {
		return
	}
	globalPromptCacheRegistry.mu.Lock()
	defer globalPromptCacheRegistry.mu.Unlock()

	// Periodic lightweight prune if table grows too large
	if len(globalPromptCacheRegistry.entries) > 2000 {
		cutoff := time.Now().Add(-2 * time.Hour)
		for k, v := range globalPromptCacheRegistry.entries {
			if v.updatedAt.Before(cutoff) {
				delete(globalPromptCacheRegistry.entries, k)
			}
		}
	}

	globalPromptCacheRegistry.entries[prefixHash] = promptCacheEntry{
		connectionID: connID,
		updatedAt:    time.Now(),
	}
}

// GetPromptCacheAffinity retrieves the preferred connection ID for a prefix hash.
func GetPromptCacheAffinity(prefixHash string) (string, bool) {
	if prefixHash == "" {
		return "", false
	}
	globalPromptCacheRegistry.mu.RLock()
	defer globalPromptCacheRegistry.mu.RUnlock()

	entry, ok := globalPromptCacheRegistry.entries[prefixHash]
	if !ok {
		return "", false
	}
	// Expire affinity if older than 1 hour (Google prompt cache window)
	if time.Since(entry.updatedAt) > time.Hour {
		return "", false
	}
	return entry.connectionID, true
}

func computePrefixHash(body []byte) string {
	var req struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
		return ""
	}
	return AnalyzePromptPrefix(req.Messages).PrefixHash
}

// AnalyzePromptPrefix inspects messages to compute a stable SHA-256 hash
// of the static prompt prefix (system messages, tools, initial turn).
func AnalyzePromptPrefix(messages []map[string]any) PromptPrefixAnalysis {
	if len(messages) == 0 {
		return PromptPrefixAnalysis{
			PrefixEndIdx: -1,
			PrefixHash:   "",
			PrefixType:   "none",
			Confidence:   0,
		}
	}

	prefixEndIdx := -1
	prefixType := "system_only"
	confidence := 0.5

	for i, msg := range messages {
		role, _ := msg["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))

		if role == "system" {
			prefixEndIdx = i
			prefixType = "system_only"
			confidence = 0.9
		} else if role == "tool" {
			prefixEndIdx = i
			prefixType = "system_and_tools"
			confidence = 0.8
		} else if role == "assistant" {
			// Initial assistant framing or prefill
			prefixEndIdx = i
			prefixType = "system_tools_history"
			confidence = 0.7
		} else {
			// Stop scanning at first actual user content message to keep prefix stable
			break
		}
	}

	if prefixEndIdx < 0 {
		// Fallback: if first message is a substantial user instruction
		if len(messages) > 0 {
			prefixEndIdx = 0
			prefixType = "initial_message"
			confidence = 0.4
		} else {
			return PromptPrefixAnalysis{
				PrefixEndIdx: -1,
				PrefixHash:   "",
				PrefixType:   "none",
				Confidence:   0,
			}
		}
	}

	var sb strings.Builder
	for i := 0; i <= prefixEndIdx; i++ {
		m := messages[i]
		if content, ok := m["content"]; ok {
			switch c := content.(type) {
			case string:
				sb.WriteString(c)
			default:
				bytes, err := json.Marshal(c)
				if err == nil {
					sb.Write(bytes)
				}
			}
			sb.WriteString("\n")
		}
	}

	h := sha256.New()
	h.Write([]byte(sb.String()))
	hashStr := hex.EncodeToString(h.Sum(nil))

	return PromptPrefixAnalysis{
		PrefixEndIdx: prefixEndIdx,
		PrefixHash:   hashStr,
		PrefixType:   prefixType,
		Confidence:   confidence,
	}
}
