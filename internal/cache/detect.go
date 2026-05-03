package cache

import (
	"encoding/json"

	"github.com/allank/psst/internal/config"
)

var wellKnownQueryKeys = map[string]bool{
	"q": true, "query": true, "text": true,
	"search": true, "summary": true, "keywords": true,
}

// DetectQueryString identifies and extracts query string values from tool arguments.
// It checks for well-known query keys and tool-specific configured keys.
// Returns the query string value and a boolean indicating if a query was found.
func DetectQueryString(tool string, args json.RawMessage, cfg *config.Config) (string, bool) {
	if !cfg.Semantic.Enabled {
		return "", false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return "", false
	}

	queryKeys := make(map[string]bool, len(wellKnownQueryKeys))
	for k := range wellKnownQueryKeys {
		queryKeys[k] = true
	}
	for _, st := range cfg.Semantic.Tools {
		if st.Tool == tool {
			for _, k := range st.ArgKeys {
				queryKeys[k] = true
			}
		}
	}

	for k, v := range m {
		if queryKeys[k] {
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				return s, true
			}
		}
	}
	return "", false
}
