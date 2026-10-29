package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const historyLimit = 200
const speedWindow = 3 * time.Second

type Progress struct {
	Phase               string
	Bytes, Total, Delta int64
}
type Record struct {
	ID         string     `json:"id"`
	Path       string     `json:"path"`
	Direction  string     `json:"direction"`
	State      string     `json:"state"`
	Phase      string     `json:"phase"`
	Bytes      int64      `json:"bytes"`
	Total      int64      `json:"total"`
	Speed      float64    `json:"speed"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
	Partial    bool       `json:"partial"`
}
type Snapshot struct {
	Active    []Record `json:"active"`
	Completed []Record `json:"completed"`
	Failed    []Record `json:"failed"`
	Error     string   `json:"error,omitempty"`
}
type sample struct {
	at    time.Time
	bytes int64
}
type storedRecord struct {
	Record
	Account string `json:"account,omitempty"`
	samples []sample
	phaseAt time.Time
}
type Tracker struct {
	mu           sync.Mutex
	records      map[string]*storedRecord
	filename     string
	scope        func() string
	clock        func() time.Time
	storageError string
	unreadable   bool
}
type Task struct {
	id      string
	tracker *Tracker
}

func New(filename string, scope func() string) *Tracker {
	t := &Tracker{records: map[string]*storedRecord{}, filename: filename, scope: scope, clock: time.Now}
	data, err := os.ReadFile(filename)
	if os.IsNotExist(err) {
		return t
	}
	var saved []storedRecord
	if err != nil || json.Unmarshal(data, &saved) != nil {
		t.storageError = "无法读取传输记录，原文件已保留"
		t.unreadable = true
		return t
	}
	interrupted := false
	for _, item := range saved {
		if item.ID == "" {
			continue
		}
		item.Speed = 0
		if item.State == "active" {
			item.State = "interrupted"
			item.Error = "上次传输已中断"
			now := t.clock()
			item.FinishedAt = &now
			interrupted = true
		}
		t.records[item.ID] = &item
	}
	t.trimLocked()
	if interrupted {
		t.saveLocked()
	}
	return t
}
func (t *Tracker) account() string {
	if t.scope != nil {
		return t.scope()
	}
	return ""
}
func (t *Tracker) Begin(direction, path string, total int64) *Task {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	id := hex.EncodeToString(raw)
	account := t.account()
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()
	t.records[id] = &storedRecord{Record: Record{ID: id, Path: path, Direction: direction, State: "active", Phase: "queued", Total: total, StartedAt: now}, Account: account, phaseAt: now}
	t.trimLocked()
	t.saveLocked()
	return &Task{id: id, tracker: t}
}
func (t *Task) ID() string { return t.id }
func (t *Task) Report(p Progress) {
	tr := t.tracker
	tr.mu.Lock()
	defer tr.mu.Unlock()
	r := tr.records[t.id]
	if r == nil || r.State != "active" {
		return
	}
	now := tr.clock()
	if p.Phase != "" && p.Phase != r.Phase {
		r.Phase = p.Phase
		r.phaseAt = now
		r.samples = nil
	}
	r.Total = p.Total
	r.Bytes = p.Bytes
	if r.Bytes < 0 {
		r.Bytes = 0
	}
	if r.Total >= 0 && r.Bytes > r.Total {
		r.Bytes = r.Total
	}
	cutoff := now.Add(-speedWindow)
	start := 0
	for start < len(r.samples) && r.samples[start].at.Before(cutoff) {
		start++
	}
	if start > 0 {
		r.samples = append([]sample(nil), r.samples[start:]...)
	}
	if p.Delta > 0 {
		r.samples = append(r.samples, sample{now, p.Delta})
	}
}
func (t *Task) SetPartial(partial bool) {
	tr := t.tracker
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if r := tr.records[t.id]; r != nil && r.State == "active" {
		r.Partial = partial
	}
}
func (t *Task) Finish(err error) {
	tr := t.tracker
	tr.mu.Lock()
	defer tr.mu.Unlock()
	r := tr.records[t.id]
	if r == nil || r.State != "active" {
		return
	}
	if r.Direction == "download" && errors.Is(err, context.Canceled) {
		delete(tr.records, t.id)
		tr.saveLocked()
		return
	}
	now := tr.clock()
	r.FinishedAt = &now
	r.Speed = 0
	r.samples = nil
	if err != nil {
		r.State = "failed"
		r.Error = err.Error()
	} else {
		r.State = "completed"
		r.Phase = "completed"
		if r.Total < 0 {
			r.Total = r.Bytes
		} else {
			r.Bytes = r.Total
		}
	}
	tr.trimLocked()
	tr.saveLocked()
}
func (t *Task) Discard() {
	tr := t.tracker
	tr.mu.Lock()
	defer tr.mu.Unlock()
	delete(tr.records, t.id)
	tr.saveLocked()
}
func (t *Tracker) Snapshot() Snapshot {
	account := t.account()
	t.mu.Lock()
	defer t.mu.Unlock()
	s := Snapshot{Active: []Record{}, Completed: []Record{}, Failed: []Record{}, Error: t.storageError}
	now := t.clock()
	for _, value := range t.sortedLocked() {
		if value.Direction == "download" && value.Error == context.Canceled.Error() {
			continue
		}
		if value.Account != account {
			continue
		}
		r := value.Record
		r.Speed = 0
		switch r.State {
		case "active":
			if r.Phase == "receiving" || r.Phase == "uploading" || r.Phase == "downloading" {
				since := value.phaseAt
				if since.Before(now.Add(-speedWindow)) {
					since = now.Add(-speedWindow)
				}
				var bytes int64
				for _, part := range value.samples {
					if !part.at.Before(now.Add(-speedWindow)) {
						bytes += part.bytes
					}
				}
				seconds := now.Sub(since).Seconds()
				if seconds >= 0.01 {
					r.Speed = float64(bytes) / seconds
				}
			}
			s.Active = append(s.Active, r)
		case "completed":
			s.Completed = append(s.Completed, r)
		default:
			s.Failed = append(s.Failed, r)
		}
	}
	return s
}
func (t *Tracker) sortedLocked() []*storedRecord {
	rows := make([]*storedRecord, 0, len(t.records))
	for _, r := range t.records {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].StartedAt, rows[j].StartedAt
		if rows[i].FinishedAt != nil {
			a = *rows[i].FinishedAt
		}
		if rows[j].FinishedAt != nil {
			b = *rows[j].FinishedAt
		}
		if a.Equal(b) {
			return rows[i].ID > rows[j].ID
		}
		return a.After(b)
	})
	return rows
}
func (t *Tracker) trimLocked() {
	type recent struct {
		record *storedRecord
		latest time.Time
	}
	downloads := map[string]recent{}
	for _, r := range t.sortedLocked() {
		if r.Direction == "download" && r.Error == context.Canceled.Error() {
			delete(t.records, r.ID)
			continue
		}
		if r.State != "completed" || r.Direction != "download" || r.FinishedAt == nil {
			continue
		}
		key := r.Account + "\x00" + r.Path
		if old, ok := downloads[key]; ok && old.latest.Sub(*r.FinishedAt) <= 5*time.Second {
			if old.record.Partial && !r.Partial {
				delete(t.records, old.record.ID)
				downloads[key] = recent{r, old.latest}
			} else {
				delete(t.records, r.ID)
			}
		} else {
			downloads[key] = recent{r, *r.FinishedAt}
		}
	}
	count := 0
	for _, r := range t.sortedLocked() {
		if r.State != "active" {
			count++
			if count > historyLimit {
				delete(t.records, r.ID)
			}
		}
	}
}

func (t *Tracker) saveLocked() {
	if t.unreadable {
		return
	}
	if err := t.writeLocked(); err != nil {
		t.storageError = "无法保存传输记录"
	} else {
		t.storageError = ""
	}
}
func (t *Tracker) writeLocked() error {
	dir := filepath.Dir(t.filename)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(t.sortedLocked())
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), t.filename)
}
