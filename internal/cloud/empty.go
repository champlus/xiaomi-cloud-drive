package cloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Xiaomi rejects size=0. Persist Finder's empty create/truncate placeholders
// locally; the next non-empty write goes through the normal cloud upload path.
type emptyRecord struct {
	Account string `json:"account"`
	Parent  string `json:"parent"`
	Entry   Entry  `json:"entry"`
}
type EmptyAdapter struct {
	Backend
	dir     string
	scope   func() string
	mu      sync.Mutex
	records map[string]emptyRecord
}

func NewEmptyAdapter(base Backend, dir string, scope func() string) *EmptyAdapter {
	e := &EmptyAdapter{Backend: base, dir: dir, scope: scope, records: map[string]emptyRecord{}}
	os.MkdirAll(dir, 0700)
	if b, err := os.ReadFile(filepath.Join(dir, "empty-files.json")); err == nil {
		json.Unmarshal(b, &e.records)
	}
	return e
}
func (e *EmptyAdapter) save() error {
	b, err := json.Marshal(e.records)
	if err != nil {
		return err
	}
	p := filepath.Join(e.dir, "empty-files.json")
	if err = os.WriteFile(p+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}
func (e *EmptyAdapter) List(ctx context.Context, parent string) ([]Entry, error) {
	items, err := e.Backend.List(ctx, parent)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	scope := e.scope()
	for _, r := range e.records {
		if r.Account == scope && r.Parent == parent {
			duplicate := false
			for _, v := range items {
				if v.Name == r.Entry.Name {
					duplicate = true
				}
			}
			if !duplicate {
				items = append(items, r.Entry)
			}
		}
	}
	return items, nil
}
func (e *EmptyAdapter) Upload(ctx context.Context, parent, name string, f *os.File) (Entry, error) {
	s, err := f.Stat()
	if err != nil {
		return Entry{}, err
	}
	if s.Size() != 0 {
		return e.Backend.Upload(ctx, parent, name, f)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	b := make([]byte, 12)
	rand.Read(b)
	entry := Entry{ID: "local-empty-" + hex.EncodeToString(b), Name: name, Type: "file", Revision: hex.EncodeToString(b), Modified: time.Now().UnixMilli(), Created: time.Now().UnixMilli()}
	e.records[entry.ID] = emptyRecord{e.scope(), parent, entry}
	if err = e.save(); err != nil {
		delete(e.records, entry.ID)
		return Entry{}, err
	}
	return entry, nil
}
func (e *EmptyAdapter) Download(ctx context.Context, entry Entry, offset int64) (io.ReadCloser, error) {
	if !strings.HasPrefix(entry.ID, "local-empty-") {
		return e.Backend.Download(ctx, entry, offset)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.records[entry.ID]
	if !ok || r.Account != e.scope() {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader("")), nil
}
func (e *EmptyAdapter) Rename(ctx context.Context, entry Entry, name string) error {
	if !strings.HasPrefix(entry.ID, "local-empty-") {
		return e.Backend.Rename(ctx, entry, name)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.records[entry.ID]
	if !ok || r.Account != e.scope() {
		return os.ErrNotExist
	}
	r.Entry.Name = name
	e.records[entry.ID] = r
	return e.save()
}
func (e *EmptyAdapter) Move(ctx context.Context, entry Entry, parent string) error {
	if !strings.HasPrefix(entry.ID, "local-empty-") {
		return e.Backend.Move(ctx, entry, parent)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.records[entry.ID]
	if !ok || r.Account != e.scope() {
		return os.ErrNotExist
	}
	r.Parent = parent
	e.records[entry.ID] = r
	return e.save()
}
func (e *EmptyAdapter) Delete(ctx context.Context, entry Entry) error {
	if !strings.HasPrefix(entry.ID, "local-empty-") {
		return e.Backend.Delete(ctx, entry)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.records[entry.ID]
	if !ok || r.Account != e.scope() {
		return os.ErrNotExist
	}
	delete(e.records, entry.ID)
	return e.save()
}
