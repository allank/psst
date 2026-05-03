package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/allank/psst/internal/cache"
	"github.com/allank/psst/internal/config"
	"github.com/allank/psst/internal/embedder"
	"github.com/allank/psst/internal/store"
)

func resolveStorePath(flagVal string, cfg *config.Config) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("PSST_STORE"); v != "" {
		return v
	}
	if cfg != nil && cfg.Server.Store != "" {
		return cfg.Server.Store
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "psst", "psst.db")
}

func loadConfig() *config.Config {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		cfg, _ = config.Load("")
	}
	return cfg
}

func openStore(path string) (*store.Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	s, err := store.Open(path, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w (is psst serve running?)", path, err)
	}
	return s, nil
}

// loadEmbedder tries to load the ONNX embedder. Returns nil, nil if no model is
// available — semantic lookup is gracefully disabled in that case.
func loadEmbedder() (cache.Embedder, error) {
	if m := embedder.EmbeddedModel(); len(m) > 0 {
		return embedder.NewFromBytes(m, embedder.EmbeddedTokenizer())
	}
	modelPath := os.Getenv("PSST_MODEL_PATH")
	tokPath := os.Getenv("PSST_TOKENIZER_PATH")
	if modelPath != "" && tokPath != "" {
		return embedder.NewONNX(modelPath, tokPath)
	}
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".config", "psst")
	mp := filepath.Join(base, "model.onnx")
	tp := filepath.Join(base, "tokenizer.json")
	if _, err := os.Stat(mp); err == nil {
		if _, err := os.Stat(tp); err == nil {
			return embedder.NewONNX(mp, tp)
		}
	}
	return nil, nil
}

func openCache(s *store.Store, cfg *config.Config, emb cache.Embedder, storePath string) *cache.Cache {
	return cache.New(s, cfg, emb, storePath)
}
