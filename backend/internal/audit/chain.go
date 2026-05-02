package audit

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
)

// computeEventHash returns base64-url-encoded SHA-256 over a canonicalized
// event representation chained from prevHash.
//
// Canonicalization: sorted JSON keys, fixed field order, RFC3339Nano UTC time.
// ImpersonatedBy is intentionally NOT in the hash so that legitimate
// impersonation provenance can be back-filled without breaking the chain.
// (LLD §03-services/02-audit "Open questions" — leans include; revisit at
// implementation review.)
func computeEventHash(e Event, prevHash string) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte(e.ID.String()))
	if e.SellerID != nil {
		h.Write([]byte(e.SellerID.String()))
	}
	h.Write([]byte(e.Action))
	h.Write([]byte(e.Target.Kind))
	h.Write([]byte(e.Target.Ref))
	h.Write([]byte(e.OccurredAt.UTC().Format("2006-01-02T15:04:05.000000000Z")))
	h.Write(canonicalJSON(e.Payload))
	h.Write(canonicalJSON(map[string]any{
		"kind": string(e.Actor.Kind),
		"ref":  e.Actor.Ref,
	}))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// canonicalJSON returns deterministic, sort-keyed JSON for hashing.
// json.Marshal already sorts top-level map keys; we recursively sort
// nested objects ourselves.
func canonicalJSON(v any) []byte {
	if v == nil {
		return []byte("null")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		// Hashing has to succeed; surface the marshal error in the hash so
		// it doesn't silently match an unrelated input.
		return []byte(fmt.Sprintf("err:%v", err))
	}
	var anyVal any
	if err := json.Unmarshal(raw, &anyVal); err != nil {
		return raw
	}
	sorted, err := json.Marshal(sortDeep(anyVal))
	if err != nil {
		return raw
	}
	return sorted
}

func sortDeep(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = sortDeep(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = sortDeep(x)
		}
		return out
	default:
		return v
	}
}

// VerifyChain recomputes hashes for the given ordered event slice and
// returns ErrChainBroken with a wrapped detail on the first mismatch.
//
// Used by the daily VerifyChainsWorker; also runnable on-demand for ops.
func VerifyChain(events []Event, eventHashes []string) error {
	if len(events) != len(eventHashes) {
		return fmt.Errorf("audit: events/hashes length mismatch (%d vs %d)", len(events), len(eventHashes))
	}
	var prev string
	for i, e := range events {
		expected := computeEventHash(e, prev)
		if expected != eventHashes[i] {
			return fmt.Errorf("%w: index=%d id=%s expected=%s got=%s",
				ErrChainBroken, i, e.ID, expected, eventHashes[i])
		}
		prev = eventHashes[i]
	}
	return nil
}
