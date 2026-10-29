package jsonutils

import (
	"bytes"
	"encoding/json"
)

// TODO(go1.27): Once the module is migrated to Go 1.27+, replace this normalization
// implementation with native RFC 8785 canonicalization from encoding/json/jsontext:
//
//	val := jsontext.Value(raw)
//	if err := val.Canonicalize(jsontext.CanonicalizeRawInts(false)); err == nil {
//	    return []byte(val), nil
//	}
//
// Canonical marshals v to JSON and normalizes it to deterministic canonical JSON
// with lexicographically sorted object keys and preserved number precision.
// This guarantees that whether v is a concrete struct, a pointer, or an unmarshaled
// map[string]any, the resulting JSON bytes are identical before and after persistence.
func Canonical(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}

	var raw []byte
	switch val := v.(type) {
	case json.RawMessage:
		raw = val
	case []byte:
		raw = val
	default:
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var normalized any
	if err := dec.Decode(&normalized); err != nil {
		return raw, nil
	}

	canon, err := json.Marshal(normalized)
	if err != nil {
		return raw, nil
	}

	return canon, nil
}
