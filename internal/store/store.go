package store

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	bucketEntries = "entries"
	bucketVectors = "vectors"
)

type Entry struct {
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args"`
	Result    json.RawMessage `json:"result"`
	CachedAt  time.Time       `json:"cached_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	Key       string          `json:"key"`
}

type Store struct {
	db *bolt.DB
}

func Open(path string, timeout time.Duration) (*Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketEntries)); err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte(bucketVectors))
		return err
	}); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Path() string { return s.db.Path() }

func (s *Store) Get(key string) (*Entry, error) {
	var e Entry
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucketEntries)).Get([]byte(key))
		if v == nil {
			return nil
		}
		found = true
		return json.Unmarshal(v, &e)
	})
	if err != nil || !found {
		return nil, err
	}
	return &e, nil
}

func (s *Store) Put(e *Entry) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		v, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return tx.Bucket([]byte(bucketEntries)).Put([]byte(e.Key), v)
	})
}

func (s *Store) Delete(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketEntries)).Delete([]byte(key))
	})
}

func (s *Store) DeleteVector(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketVectors)).Delete([]byte(key))
	})
}

func (s *Store) DeleteTool(tool string) (int, error) {
	var count int
	err := s.db.Update(func(tx *bolt.Tx) error {
		eb := tx.Bucket([]byte(bucketEntries))
		vb := tx.Bucket([]byte(bucketVectors))
		var keys [][]byte
		if err := eb.ForEach(func(k, v []byte) error {
			var e Entry
			if err := json.Unmarshal(v, &e); err != nil {
				return nil
			}
			if e.Tool == tool {
				keys = append(keys, append([]byte{}, k...))
			}
			return nil
		}); err != nil {
			return err
		}
		count = len(keys)
		for _, k := range keys {
			if err := eb.Delete(k); err != nil {
				return err
			}
			_ = vb.Delete(k)
		}
		return nil
	})
	return count, err
}

func (s *Store) DeleteAll() (int, error) {
	var count int
	return count, s.db.Update(func(tx *bolt.Tx) error {
		count = tx.Bucket([]byte(bucketEntries)).Stats().KeyN
		for _, name := range []string{bucketEntries, bucketVectors} {
			if err := tx.DeleteBucket([]byte(name)); err != nil {
				return err
			}
			if _, err := tx.CreateBucket([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Scan(fn func(*Entry) error) error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketEntries)).ForEach(func(_, v []byte) error {
			var e Entry
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			return fn(&e)
		})
	})
}

func (s *Store) GetVector(key string) ([]float32, error) {
	var vec []float32
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucketVectors)).Get([]byte(key))
		if v == nil {
			return nil
		}
		vec = decodeVector(v)
		return nil
	})
	return vec, err
}

func (s *Store) PutVector(key string, vec []float32) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketVectors)).Put([]byte(key), encodeVector(vec))
	})
}

func (s *Store) ScanVectors(tool string, fn func(key string, vec []float32) error) error {
	// Collect keys for the given tool first
	var matchKeys [][]byte
	if err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketEntries)).ForEach(func(k, v []byte) error {
			var e Entry
			if err := json.Unmarshal(v, &e); err != nil {
				return nil
			}
			if e.Tool == tool {
				matchKeys = append(matchKeys, append([]byte{}, k...))
			}
			return nil
		})
	}); err != nil {
		return err
	}
	return s.db.View(func(tx *bolt.Tx) error {
		vb := tx.Bucket([]byte(bucketVectors))
		for _, k := range matchKeys {
			v := vb.Get(k)
			if v == nil {
				continue
			}
			if err := fn(string(k), decodeVector(v)); err != nil {
				return err
			}
		}
		return nil
	})
}

func encodeVector(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
