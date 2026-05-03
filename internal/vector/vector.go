package vector

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
)

const hnswThreshold = 2000

// Index is the interface for vector similarity search.
type Index interface {
	Add(id uint64, vector []float32) error
	Get(id uint64) ([]float32, bool)
	Search(vector []float32, k int) ([]Result, error)
	Len() int
	Save(w io.Writer) error
	Load(r io.Reader) error
	Type() string
}

type Result struct {
	ID    uint64
	Score float32
}

// flatIndex is a brute-force cosine search used for small indexes (<2000 dirs).
type flatIndex struct {
	dims    int
	ids     []uint64
	vectors [][]float32
}

func NewFlat(dims int) Index {
	return &flatIndex{dims: dims}
}

func (f *flatIndex) Type() string { return "flat" }
func (f *flatIndex) Len() int     { return len(f.ids) }

func (f *flatIndex) Get(id uint64) ([]float32, bool) {
	for i, fid := range f.ids {
		if fid == id {
			return f.vectors[i], true
		}
	}
	return nil, false
}

func (f *flatIndex) Add(id uint64, vec []float32) error {
	if len(vec) != f.dims {
		return fmt.Errorf("vector length %d != dims %d", len(vec), f.dims)
	}
	f.ids = append(f.ids, id)
	cp := make([]float32, len(vec))
	copy(cp, vec)
	f.vectors = append(f.vectors, cp)
	return nil
}

func (f *flatIndex) Search(query []float32, k int) ([]Result, error) {
	results := make([]Result, 0, len(f.ids))
	for i, vec := range f.vectors {
		results = append(results, Result{ID: f.ids[i], Score: cosine(query, vec)})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if k > len(results) {
		k = len(results)
	}
	return results[:k], nil
}

func (f *flatIndex) Save(w io.Writer) error {
	if err := binary.Write(w, binary.LittleEndian, int32(f.dims)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, int32(len(f.ids))); err != nil {
		return err
	}
	for i, id := range f.ids {
		if err := binary.Write(w, binary.LittleEndian, id); err != nil {
			return err
		}
		for _, v := range f.vectors[i] {
			if err := binary.Write(w, binary.LittleEndian, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *flatIndex) Load(r io.Reader) error {
	var dims, n int32
	if err := binary.Read(r, binary.LittleEndian, &dims); err != nil {
		return err
	}
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return err
	}
	f.dims = int(dims)
	f.ids = make([]uint64, n)
	f.vectors = make([][]float32, n)
	for i := int32(0); i < n; i++ {
		if err := binary.Read(r, binary.LittleEndian, &f.ids[i]); err != nil {
			return err
		}
		vec := make([]float32, dims)
		if err := binary.Read(r, binary.LittleEndian, vec); err != nil {
			return err
		}
		f.vectors[i] = vec
	}
	return nil
}

// hnswIndex wraps a flatIndex as a placeholder until USearch CGo binding is confirmed.
// Type() returns "hnsw" so callers can distinguish the two.
type hnswIndex struct {
	inner *flatIndex
}

func newHNSW(dims int) (Index, error) {
	return &hnswIndex{inner: &flatIndex{dims: dims}}, nil
}

func (h *hnswIndex) Type() string                                 { return "hnsw" }
func (h *hnswIndex) Len() int                                     { return h.inner.Len() }
func (h *hnswIndex) Add(id uint64, v []float32) error             { return h.inner.Add(id, v) }
func (h *hnswIndex) Get(id uint64) ([]float32, bool)              { return h.inner.Get(id) }
func (h *hnswIndex) Search(v []float32, k int) ([]Result, error)  { return h.inner.Search(v, k) }
func (h *hnswIndex) Save(w io.Writer) error                       { return h.inner.Save(w) }
func (h *hnswIndex) Load(r io.Reader) error                       { return h.inner.Load(r) }

// New returns a flat index for <2000 expected entries, HNSW wrapper for >=2000.
func New(dims int, expectedSize int) (Index, error) {
	if expectedSize >= hnswThreshold {
		return newHNSW(dims)
	}
	return NewFlat(dims), nil
}

func cosine(a, b []float32) float32 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
