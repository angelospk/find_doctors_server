package watch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Store is an in-memory watch registry backed by a write-through atomic JSON
// snapshot. All mutations persist the whole map via temp-file + rename.
type Store struct {
	path string

	mu      sync.RWMutex
	watches map[string]*Watch
}

// NewStore opens (or creates) a store backed by the snapshot at path. A missing
// file starts empty; a corrupt file is a hard error.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, watches: make(map[string]*Watch)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("watch: read snapshot: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var list []*Watch
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("watch: parse snapshot %s: %w", s.path, err)
	}
	for _, w := range list {
		s.watches[w.ID] = w
	}
	return nil
}

// persist writes the whole map to the snapshot atomically. Caller holds s.mu.
func (s *Store) persist() error {
	list := make([]*Watch, 0, len(s.watches))
	for _, w := range s.watches {
		list = append(list, w)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("watch: marshal snapshot: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("watch: write snapshot: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("watch: rename snapshot: %w", err)
	}
	return nil
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("watch: generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Create assigns an ID, timestamps, and active status, then persists.
func (s *Store) Create(ctx context.Context, w Watch) (Watch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := newID()
	if err != nil {
		return Watch{}, err
	}
	w.ID = id
	w.Status = StatusActive
	w.CreatedAt = time.Now()

	stored := w
	s.watches[w.ID] = &stored
	if err := s.persist(); err != nil {
		delete(s.watches, w.ID)
		return Watch{}, err
	}
	return stored, nil
}

// Get returns a copy of the watch, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Watch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.watches[id]
	if !ok {
		return Watch{}, ErrNotFound
	}
	return *w, nil
}

// Delete cancels a watch. It is idempotent: deleting an unknown or already
// cancelled watch is a no-op success.
func (s *Store) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.watches[id]
	if !ok || w.Status == StatusCancelled {
		return nil
	}
	w.Status = StatusCancelled
	return s.persist()
}

// ListActive returns copies of every watch that is active and not yet expired.
func (s *Store) ListActive(ctx context.Context) ([]Watch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	var out []Watch
	for _, w := range s.watches {
		if w.Status == StatusActive && w.ExpiresAt.After(now) {
			out = append(out, *w)
		}
	}
	return out, nil
}

// Apply mutates a watch under the write lock, but only while it is still active.
// fn returns whether it changed anything; Apply persists and reports changed.
// A missing or non-active watch is a no-op (changed=false, nil error) so a
// concurrent Delete can't be raced into a notification or resurrection.
func (s *Store) Apply(ctx context.Context, id string, fn func(*Watch) bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.watches[id]
	if !ok || w.Status != StatusActive {
		return false, nil
	}
	if !fn(w) {
		return false, nil
	}
	if err := s.persist(); err != nil {
		return false, err
	}
	return true, nil
}
