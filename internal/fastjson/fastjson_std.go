//go:build !((linux || darwin || windows) && (amd64 || arm64))

package fastjson

import (
	"encoding/json"
)

// Marshal serializes v to JSON bytes. Identical to json.Marshal.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal deserializes JSON data into v. Identical to json.Unmarshal.
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// MarshalIndent serializes v to indented JSON bytes.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// Valid reports whether data is a valid JSON encoding.
func Valid(data []byte) bool {
	return json.Valid(data)
}
