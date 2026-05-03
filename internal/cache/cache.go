package cache

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/allank/psst/internal/config"
	"github.com/allank/psst/internal/store"
	"github.com/allank/psst/internal/vector"
)

// Embedder is satisfied by *embedder.ONNXEmbedder.
type Embedder interface {
	Embed(text string) ([]float32, error)
	Close() error
}

type Result struct {
	Hit   bool
	Match string  // "exact" | "semantic"
	Score float64 // 0 for exact; cosine similarity for semantic
	Entry *store.Entry
}

type toolIndex struct {
	flat    vector.Index
	idToKey map[uint64]string
	nextID  uint64
}

type Cache struct {
	s         *store.Store
	cfg       *config.Config
	emb       Embedder
	storePath string

	mu    sync.RWMutex
	tools map[string]*toolIndex

	indexOnce sync.Once
}

func New(s *store.Store, cfg *config.Config, emb Embedder, storePath string) *Cache {
	return &Cache{
		s:         s,
		cfg:       cfg,
		emb:       emb,
		storePath: storePath,
		tools:     make(map[string]*toolIndex),
	}
}

// EnsureIndex loads the sidecar (if fresh) or rebuilds the in-memory vector index.
// Called explicitly by the daemon on startup; called lazily on first semantic lookup in CLI mode.
func (c *Cache) EnsureIndex() {
	c.indexOnce.Do(func() {
		if err := c.loadSidecar(); err != nil {
			_ = c.RebuildIndex()
			_ = c.saveSidecar()
		}
	})
}

// RebuildIndex clears and rebuilds the in-memory vector index from bbolt.
// Called by the daemon on SIGHUP.
func (c *Cache) RebuildIndex() error {
	c.mu.Lock()
	c.tools = make(map[string]*toolIndex)
	c.mu.Unlock()

	return c.s.Scan(func(e *store.Entry) error {
		if time.Now().After(e.ExpiresAt) {
			return nil
		}
		vec, err := c.s.GetVector(e.Key)
		if err != nil || vec == nil {
			return nil
		}
		c.addToIndex(e.Tool, e.Key, vec)
		return nil
	})
}

func (c *Cache) Lookup(ctx context.Context, tool string, args json.RawMessage, threshold float64) (*Result, error) {
	key, err := CanonicalKey(tool, args)
	if err != nil {
		return &Result{Hit: false}, nil
	}

	// Exact match
	e, err := c.s.Get(key)
	if err != nil {
		return nil, err
	}
	if e != nil {
		if time.Now().Before(e.ExpiresAt) {
			return &Result{Hit: true, Match: "exact", Entry: e}, nil
		}
		_ = c.s.Delete(key)
		_ = c.s.DeleteVector(key)
	}

	// Semantic match
	if c.emb == nil {
		return &Result{Hit: false}, nil
	}
	q, ok := DetectQueryString(tool, args, c.cfg)
	if !ok {
		return &Result{Hit: false}, nil
	}

	c.EnsureIndex()

	vec, err := c.emb.Embed(q)
	if err != nil {
		return &Result{Hit: false}, nil
	}

	c.mu.RLock()
	ti := c.tools[tool]
	c.mu.RUnlock()

	if ti == nil || ti.flat.Len() == 0 {
		return &Result{Hit: false}, nil
	}

	results, err := ti.flat.Search(vec, 1)
	if err != nil || len(results) == 0 {
		return &Result{Hit: false}, nil
	}
	if float64(results[0].Score) < threshold {
		return &Result{Hit: false}, nil
	}

	c.mu.RLock()
	hitKey := ti.idToKey[results[0].ID]
	c.mu.RUnlock()

	if hitKey == "" {
		return &Result{Hit: false}, nil
	}

	e, err = c.s.Get(hitKey)
	if err != nil || e == nil || time.Now().After(e.ExpiresAt) {
		return &Result{Hit: false}, nil
	}

	return &Result{
		Hit:   true,
		Match: "semantic",
		Score: float64(results[0].Score),
		Entry: e,
	}, nil
}

func (c *Cache) Store(ctx context.Context, tool string, args json.RawMessage, result json.RawMessage, ttl int) (*store.Entry, error) {
	key, err := CanonicalKey(tool, args)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	e := &store.Entry{
		Tool:      tool,
		Args:      args,
		Result:    result,
		CachedAt:  now,
		ExpiresAt: now.Add(config.ResolveTTL(tool, ttl, c.cfg)),
		Key:       key,
	}
	if err := c.s.Put(e); err != nil {
		return nil, err
	}
	if c.emb != nil {
		if q, ok := DetectQueryString(tool, args, c.cfg); ok {
			if vec, err := c.emb.Embed(q); err == nil {
				_ = c.s.PutVector(key, vec)
				c.addToIndex(tool, key, vec)
			}
		}
	}
	return e, nil
}

func (c *Cache) Invalidate(tool, key string) (int, error) {
	if key != "" {
		if err := c.s.Delete(key); err != nil {
			return 0, err
		}
		_ = c.s.DeleteVector(key)
		c.removeKeyFromIndex(tool, key)
		return 1, nil
	}
	if tool == "*" || tool == "" {
		n, err := c.s.DeleteAll()
		if err != nil {
			return 0, err
		}
		c.mu.Lock()
		c.tools = make(map[string]*toolIndex)
		c.mu.Unlock()
		return n, nil
	}
	n, err := c.s.DeleteTool(tool)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	delete(c.tools, tool)
	c.mu.Unlock()
	return n, nil
}

func (c *Cache) Evict() (int, error) {
	var toDelete []string
	if err := c.s.Scan(func(e *store.Entry) error {
		if time.Now().After(e.ExpiresAt) {
			toDelete = append(toDelete, e.Key)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	for _, k := range toDelete {
		_ = c.s.Delete(k)
		_ = c.s.DeleteVector(k)
	}
	return len(toDelete), nil
}

func (c *Cache) SaveSidecar() error { return c.saveSidecar() }

func (c *Cache) addToIndex(tool, key string, vec []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ti := c.tools[tool]
	if ti == nil {
		idx, _ := vector.New(384, 0)
		ti = &toolIndex{flat: idx, idToKey: make(map[uint64]string)}
		c.tools[tool] = ti
	}
	id := ti.nextID
	ti.nextID++
	_ = ti.flat.Add(id, vec)
	ti.idToKey[id] = key
}

func (c *Cache) removeKeyFromIndex(tool, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ti := c.tools[tool]
	if ti == nil {
		return
	}
	for id, k := range ti.idToKey {
		if k == key {
			delete(ti.idToKey, id)
			return
		}
	}
}
