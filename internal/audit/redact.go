package audit

import (
	"encoding/json"
	"strings"
)

// defaultRedactKeys is the built-in set of object keys whose values are secrets.
// env/headers are included for generality (they can carry credentials in other
// payload shapes) even though routed-call payloads rarely contain them.
var defaultRedactKeys = []string{
	"password", "token", "apikey", "api_key", "authorization", "secret",
	"env", "headers",
}

// redactedMarker replaces any redacted value.
const redactedMarker = "[REDACTED]"

// Redactor scrubs secret-valued object keys from a JSON payload before it is
// stored or exported. Matching is case-insensitive on the key name; the value
// (whatever its type) is replaced wholesale with a marker string.
type Redactor struct {
	keys map[string]struct{}
}

// NewRedactor builds a redactor from the built-in secret keys plus any extra
// keys from config (additive). Extra keys are matched case-insensitively.
func NewRedactor(extra []string) *Redactor {
	keys := make(map[string]struct{}, len(defaultRedactKeys)+len(extra))
	for _, k := range defaultRedactKeys {
		keys[strings.ToLower(k)] = struct{}{}
	}
	for _, k := range extra {
		if k != "" {
			keys[strings.ToLower(k)] = struct{}{}
		}
	}
	return &Redactor{keys: keys}
}

// Redact returns a copy of raw with every secret-keyed value replaced. Empty
// input passes through. Input that is not valid JSON is never stored verbatim
// (it might carry unstructured secrets); it becomes a safe placeholder instead.
func (r *Redactor) Redact(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return json.RawMessage(`"[unredactable non-JSON payload]"`)
	}
	out, err := json.Marshal(r.walk(v))
	if err != nil {
		return json.RawMessage(`"[unredactable payload]"`)
	}
	return out
}

// walk recursively rewrites secret-keyed values in maps and descends into
// arrays. Scalars pass through unchanged.
func (r *Redactor) walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if r.isSecret(k) {
				t[k] = redactedMarker
			} else {
				t[k] = r.walk(val)
			}
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = r.walk(val)
		}
		return t
	default:
		return v
	}
}

// isSecret reports whether key names a secret value (case-insensitive).
func (r *Redactor) isSecret(key string) bool {
	_, ok := r.keys[strings.ToLower(key)]
	return ok
}
