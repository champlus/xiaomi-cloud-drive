package dav

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
	"golang.org/x/net/webdav"
	"golang.org/x/text/unicode/norm"
)

type cached struct {
	items []cloud.Entry
	at    time.Time
}
type listFlight struct {
	done  chan struct{}
	items []cloud.Entry
	err   error
}
type refreshFailure struct {
	err      error
	until    time.Time
	attempts int
}

type FS struct {
	Backend  cloud.Backend
	Root     string
	ReadOnly bool
	Mutation sync.Mutex
	mu       sync.Mutex
	cache    map[string]cached
	flight   map[string]*listFlight
	epoch    uint64
	failures map[string]refreshFailure
}

func New(backend cloud.Backend) *FS {
	return &FS{Backend: backend, Root: "0", cache: map[string]cached{}, flight: map[string]*listFlight{}, failures: map[string]refreshFailure{}}
}
func (f *FS) Invalidate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cache = map[string]cached{}
	f.flight = map[string]*listFlight{}
	f.failures = map[string]refreshFailure{}
	f.epoch++
}
func (f *FS) List(ctx context.Context, id string) ([]cloud.Entry, error) {
	f.mu.Lock()
	c, ok := f.cache[id]
	if ok && time.Since(c.at) < 30*time.Second {
		f.mu.Unlock()
		return c.items, nil
	}
	if ok && time.Since(c.at) < 5*time.Minute {
		if f.flight[id] == nil && !time.Now().Before(f.failures[id].until) {
			flight := &listFlight{done: make(chan struct{})}
			f.flight[id] = flight
			epoch := f.epoch
			go func() {
				bg, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				f.fetchList(bg, id, flight, epoch)
			}()
		}
		f.mu.Unlock()
		return c.items, nil
	}
	if failure := f.failures[id]; time.Now().Before(failure.until) {
		f.mu.Unlock()
		return nil, failure.err
	}
	if flight := f.flight[id]; flight != nil {
		f.mu.Unlock()
		select {
		case <-flight.done:
			return flight.items, flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	flight := &listFlight{done: make(chan struct{})}
	f.flight[id] = flight
	epoch := f.epoch
	f.mu.Unlock()
	f.fetchList(ctx, id, flight, epoch)
	return flight.items, flight.err
}
func (f *FS) fetchList(ctx context.Context, id string, flight *listFlight, epoch uint64) {
	items, err := f.Backend.List(ctx, id)
	f.mu.Lock()
	defer f.mu.Unlock()
	flight.items = items
	flight.err = err
	if epoch == f.epoch {
		if err == nil {
			f.cache[id] = cached{items, time.Now()}
			delete(f.failures, id)
		} else {
			attempts := f.failures[id].attempts + 1
			if attempts > 5 {
				attempts = 5
			}
			delay := 5 * time.Second * time.Duration(1<<(attempts-1))
			if delay > time.Minute {
				delay = time.Minute
			}
			f.failures[id] = refreshFailure{err: err, until: time.Now().Add(delay), attempts: attempts}
		}
	}
	if f.flight[id] == flight {
		delete(f.flight, id)
	}
	close(flight.done)
}

// Await makes acknowledged mutations immediately readable, despite the cloud's
// short delay between an operation response and its directory index updating.
func (f *FS) Await(ctx context.Context, parent, id, name string, deleted bool) (cloud.Entry, error) {
	deadline := time.Now().Add(5 * time.Second)
	delay := 200 * time.Millisecond
	for {
		items, err := f.Backend.List(ctx, parent)
		if err != nil {
			return cloud.Entry{}, err
		}
		var found cloud.Entry
		for _, e := range items {
			if e.ID == id {
				found = e
			}
		}
		if (deleted && found.ID == "") || (!deleted && found.ID != "" && found.Name == name) {
			f.mu.Lock()
			f.epoch++
			f.cache[parent] = cached{items, time.Now()}
			f.mu.Unlock()
			return found, nil
		}
		if time.Now().After(deadline) {
			return cloud.Entry{}, errors.New("小米目录索引尚未更新，请稍后重试；本地副本已保留")
		}
		select {
		case <-ctx.Done():
			return cloud.Entry{}, ctx.Err()
		case <-time.After(delay):
		}
		if delay < time.Second {
			delay *= 2
			if delay > time.Second {
				delay = time.Second
			}
		}
	}
}
func clean(s string) string { return norm.NFC.String(path.Clean("/" + s)) }
func ignored(s string) bool {
	n := path.Base(s)
	return n == ".DS_Store" || strings.HasPrefix(n, "._") || n == ".Spotlight-V100" || n == ".Trashes" || (strings.HasPrefix(n, ".米盘上传-") || strings.HasPrefix(n, ".米盘旧版本-"))
}
func ValidName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00\r\n") && len(name) <= 255
}
func (f *FS) Resolve(ctx context.Context, name string) (cloud.Entry, error) {
	name = clean(name)
	if name == "/" {
		return cloud.Entry{ID: f.Root, Name: "小米云盘", Type: "folder"}, nil
	}
	current := cloud.Entry{ID: f.Root, Type: "folder"}
	for _, part := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		if !current.IsDir() {
			return cloud.Entry{}, os.ErrNotExist
		}
		items, err := f.List(ctx, current.ID)
		if err != nil {
			return cloud.Entry{}, err
		}
		found := false
		for _, e := range items {
			if norm.NFC.String(e.Name) == part {
				if found {
					return cloud.Entry{}, errors.New("云盘含有重名文件，请先在官方网页整理")
				}
				current = e
				found = true
			}
		}
		if !found {
			return cloud.Entry{}, os.ErrNotExist
		}
	}
	return current, nil
}
func (f *FS) Parent(ctx context.Context, name string) (cloud.Entry, string, error) {
	name = clean(name)
	if name == "/" {
		return cloud.Entry{}, "", os.ErrPermission
	}
	base := path.Base(name)
	if !ValidName(base) {
		return cloud.Entry{}, "", os.ErrInvalid
	}
	e, err := f.Resolve(ctx, path.Dir(name))
	if err == nil && !e.IsDir() {
		err = os.ErrNotExist
	}
	return e, base, err
}
func (f *FS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	f.Mutation.Lock()
	defer f.Mutation.Unlock()
	if f.ReadOnly {
		return os.ErrPermission
	}
	p, n, err := f.Parent(ctx, name)
	if err != nil {
		return err
	}
	if _, err = f.Resolve(ctx, name); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	created, e := f.Backend.Mkdir(ctx, p.ID, n)
	f.Invalidate()
	if e != nil {
		return e
	}
	_, err = f.Await(ctx, p.ID, created.ID, n, false)
	return err
}
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	f.Mutation.Lock()
	defer f.Mutation.Unlock()
	if f.ReadOnly || clean(name) == "/" {
		return os.ErrPermission
	}
	e, err := f.Resolve(ctx, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	parent, _, err := f.Parent(ctx, name)
	if err != nil {
		return err
	}
	err = f.Backend.Delete(ctx, e)
	f.Invalidate()
	if err != nil {
		return err
	}
	_, err = f.Await(ctx, parent.ID, e.ID, "", true)
	return err
}
func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	f.Mutation.Lock()
	defer f.Mutation.Unlock()
	return f.rename(ctx, oldName, newName)
}
func (f *FS) rename(ctx context.Context, oldName, newName string) error {
	if f.ReadOnly || clean(oldName) == "/" || clean(newName) == "/" {
		return os.ErrPermission
	}
	if clean(oldName) == clean(newName) {
		return nil
	}
	if strings.HasPrefix(clean(newName)+"/", clean(oldName)+"/") {
		return os.ErrInvalid
	}
	source, err := f.Resolve(ctx, oldName)
	if err != nil {
		return err
	}
	p, n, err := f.Parent(ctx, newName)
	if err != nil {
		return err
	}
	if _, err = f.Resolve(ctx, newName); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oldParent, _, err := f.Parent(ctx, oldName)
	if err != nil {
		return err
	}
	// Rename first. If moving fails, restore the original name.
	if source.Name != n {
		if err = f.Backend.Rename(ctx, source, n); err != nil {
			return err
		}
	}
	if oldParent.ID != p.ID {
		if err = f.Backend.Move(ctx, source, p.ID); err != nil {
			if source.Name != n {
				rollback := f.Backend.Rename(ctx, source, source.Name)
				if rollback != nil {
					err = fmt.Errorf("%w；恢复原文件名失败，请在官方网页检查", err)
				}
			}
			f.Invalidate()
			return err
		}
	}
	f.Invalidate()
	_, err = f.Await(ctx, p.ID, source.ID, n, false)
	return err
}
func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if ignored(name) {
		return nil, os.ErrNotExist
	}
	e, err := f.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return info{e}, nil
}
func (f *FS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
		return nil, os.ErrPermission
	}
	if ignored(name) {
		return nil, os.ErrNotExist
	}
	e, err := f.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return &remoteFile{ctx: ctx, fs: f, entry: e}, nil
}

type info struct{ cloud.Entry }

func (i info) Name() string { return i.Entry.Name }
func (i info) Size() int64  { return i.Entry.Size }
func (i info) Mode() os.FileMode {
	if i.IsDir() {
		return os.ModeDir | 0755
	}
	return 0644
}
func (i info) ModTime() time.Time { return i.Entry.ModTime() }
func (i info) Sys() any           { return nil }
func (i info) ContentType(ctx context.Context) (string, error) {
	if i.IsDir() {
		return "httpd/unix-directory", nil
	}
	t := mime.TypeByExtension(filepath.Ext(i.Entry.Name))
	if t == "" {
		t = "application/octet-stream"
	}
	return t, nil
}
func (i info) ETag(ctx context.Context) (string, error) {
	return `"` + i.ID + "-" + i.Revision + `"`, nil
}

type remoteFile struct {
	ctx      context.Context
	fs       *FS
	entry    cloud.Entry
	offset   int64
	reader   io.ReadCloser
	children []os.FileInfo
	index    int
	loaded   bool
}

func (r *remoteFile) Close() error {
	if r.reader != nil {
		return r.reader.Close()
	}
	return nil
}
func (r *remoteFile) Stat() (os.FileInfo, error) { return info{r.entry}, nil }
func (r *remoteFile) Write([]byte) (int, error)  { return 0, os.ErrPermission }
func (r *remoteFile) Read(b []byte) (int, error) {
	if r.entry.IsDir() {
		return 0, os.ErrInvalid
	}
	if r.offset >= r.entry.Size {
		return 0, io.EOF
	}
	if r.reader == nil {
		var err error
		r.reader, err = r.fs.Backend.Download(r.ctx, r.entry, r.offset)
		if err != nil {
			return 0, err
		}
	}
	n, err := r.reader.Read(b)
	r.offset += int64(n)
	return n, err
}
func (r *remoteFile) Seek(offset int64, whence int) (int64, error) {
	var n int64
	switch whence {
	case io.SeekStart:
		n = offset
	case io.SeekCurrent:
		n = r.offset + offset
	case io.SeekEnd:
		n = r.entry.Size + offset
	default:
		return 0, os.ErrInvalid
	}
	if n < 0 {
		return 0, os.ErrInvalid
	}
	if n != r.offset && r.reader != nil {
		r.reader.Close()
		r.reader = nil
	}
	r.offset = n
	return n, nil
}
func (r *remoteFile) Readdir(count int) ([]os.FileInfo, error) {
	if !r.entry.IsDir() {
		return nil, os.ErrInvalid
	}
	if !r.loaded {
		items, err := r.fs.List(r.ctx, r.entry.ID)
		if err != nil {
			return nil, err
		}
		for _, e := range items {
			if !ignored(e.Name) {
				r.children = append(r.children, info{e})
			}
		}
		r.loaded = true
	}
	if count > 0 && r.index >= len(r.children) {
		return nil, io.EOF
	}
	end := len(r.children)
	if count > 0 && r.index+count < end {
		end = r.index + count
	}
	items := r.children[r.index:end]
	r.index = end
	return items, nil
}
