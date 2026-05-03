package cache

import (
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"

	"github.com/allank/psst/internal/vector"
)

type sidecarEntry struct {
	ID  uint64
	Key string
	Vec []float32
}

type sidecarData struct {
	Tools map[string][]sidecarEntry
}

func sidecarPath(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "psst.idx")
}

func (c *Cache) loadSidecar() error {
	path := sidecarPath(c.storePath)

	sInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	dbInfo, err := os.Stat(c.storePath)
	if err != nil {
		return err
	}
	if sInfo.ModTime().Before(dbInfo.ModTime()) {
		return errors.New("sidecar stale")
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var data sidecarData
	if err := gob.NewDecoder(f).Decode(&data); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for tool, entries := range data.Tools {
		idx, _ := vector.New(384, 0)
		ti := &toolIndex{flat: idx, idToKey: make(map[uint64]string, len(entries))}
		for _, e := range entries {
			_ = ti.flat.Add(e.ID, e.Vec)
			ti.idToKey[e.ID] = e.Key
			if e.ID >= ti.nextID {
				ti.nextID = e.ID + 1
			}
		}
		c.tools[tool] = ti
	}
	return nil
}

func (c *Cache) saveSidecar() error {
	c.mu.RLock()
	data := sidecarData{Tools: make(map[string][]sidecarEntry, len(c.tools))}
	for tool, ti := range c.tools {
		entries := make([]sidecarEntry, 0, len(ti.idToKey))
		for id, key := range ti.idToKey {
			vec, ok := ti.flat.Get(id)
			if !ok {
				continue
			}
			entries = append(entries, sidecarEntry{ID: id, Key: key, Vec: vec})
		}
		data.Tools[tool] = entries
	}
	c.mu.RUnlock()

	path := sidecarPath(c.storePath)
	tmp, err := os.CreateTemp(filepath.Dir(path), "psst-idx-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := gob.NewEncoder(tmp).Encode(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()
	return os.Rename(tmpPath, path)
}
