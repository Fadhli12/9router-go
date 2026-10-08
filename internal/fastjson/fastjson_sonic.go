//go:build (linux || darwin || windows) && (amd64 || arm64)

package fastjson

import (
	"github.com/bytedance/sonic"
)

// defaultConfig is the Sonic configuration used by this package.
// It matches encoding/json behavior: no HTML escaping, no sort keys.
var defaultConfig = sonic.ConfigDefault

// Marshal serializes v to JSON bytes. Identical to json.Marshal.
func Marshal(v any) ([]byte, error) {
	return defaultConfig.Marshal(v)
}

// Unmarshal deserializes JSON data into v. Identical to json.Unmarshal.
func Unmarshal(data []byte, v any) error {
	return defaultConfig.Unmarshal(data, v)
}

// MarshalIndent serializes v to indented JSON bytes.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return defaultConfig.MarshalIndent(v, prefix, indent)
}

// Valid reports whether data is a valid JSON encoding.
func Valid(data []byte) bool {
	return defaultConfig.Valid(data)
}
