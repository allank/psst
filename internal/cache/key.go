package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalKey generates a stable cache key from a tool name and arguments.
// Args are re-marshalled through map[string]any to sort keys alphabetically,
// ensuring that different argument orderings produce the same key.
func CanonicalKey(tool string, args json.RawMessage) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	input := append([]byte(tool+":"), canonical...)
	sum := sha256.Sum256(input)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
