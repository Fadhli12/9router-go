// Package fastjson provides a drop-in replacement for encoding/json backed by
// github.com/bytedance/sonic on supported platforms (amd64, arm64 on linux/darwin/windows)
// and falling back to encoding/json elsewhere.
//
// All public functions have identical signatures to their encoding/json
// counterparts, so migration is a one-line import swap.
package fastjson

import (
	"bytes"
	"encoding/json"
	"io"
)

// Common type aliases for drop-in compatibility with encoding/json.
type (
	RawMessage            = json.RawMessage
	Number                = json.Number
	Marshaler             = json.Marshaler
	Unmarshaler           = json.Unmarshaler
	Decoder               = json.Decoder
	Encoder               = json.Encoder
	Delim                 = json.Delim
	Token                 = json.Token
	InvalidUTF8Error      = json.InvalidUTF8Error
	InvalidUnmarshalError = json.InvalidUnmarshalError
	MarshalerError        = json.MarshalerError
	SyntaxError           = json.SyntaxError
	UnmarshalFieldError   = json.UnmarshalFieldError
	UnmarshalTypeError    = json.UnmarshalTypeError
	UnsupportedTypeError  = json.UnsupportedTypeError
	UnsupportedValueError = json.UnsupportedValueError
)

// NewDecoder returns a streaming JSON decoder reading from r.
// Identical to json.NewDecoder.
func NewDecoder(r io.Reader) *Decoder {
	return json.NewDecoder(r)
}

// NewEncoder returns a streaming JSON encoder writing to w.
// Identical to json.NewEncoder.
func NewEncoder(w io.Writer) *Encoder {
	return json.NewEncoder(w)
}

// Compact appends to dst the JSON-encoded src with insignificant space characters elided.
func Compact(dst *bytes.Buffer, src []byte) error {
	return json.Compact(dst, src)
}

// Indent appends to dst an indented form of the JSON-encoded src.
func Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	return json.Indent(dst, src, prefix, indent)
}

// HTMLEscape appends to dst the JSON-encoded src with <, >, &, U+2028, and U+2029
// characters escaped inside string literals.
func HTMLEscape(dst *bytes.Buffer, src []byte) {
	json.HTMLEscape(dst, src)
}

// UnmarshalRead reads from r and unmarshals into v.
func UnmarshalRead(r io.Reader, v any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return Unmarshal(data, v)
}
