package cache

import (
	"encoding/json"
	"slices"

	"github.com/allank/psst/internal/config"
)

var wellKnownQueryKeys = map[string]bool{
	"q": true, "query": true, "text": true,
	"search": true, "summary": true, "keywords": true,
}

func DetectQueryString(tool string, args json.RawMessage, cfg *config.Config) (string, bool) {
	if cfg == nil || !cfg.Semantic.Enabled {
		return "", false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return "", false
	}

	var toolArgKeys []string
	for _, st := range cfg.Semantic.Tools {
		if st.Tool == tool {
			toolArgKeys = st.ArgKeys
			break
		}
	}

	for k, v := range m {
		if wellKnownQueryKeys[k] || slices.Contains(toolArgKeys, k) {
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				return s, true
			}
		}
	}
	return "", false
}

