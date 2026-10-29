package dav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
)

type fake struct {
	mu                     sync.Mutex
	entries                map[string]cloud.Entry
	parent                 map[string]string
	data                   map[string][]byte
	next                   int
	failUpload, failRename bool
	deleted                []string
}

func newFake() *fake {
	return &fake{entries: map[string]cloud.Entry{}, parent: map[string]string{}, data: map[string][]byte{}}
}
func (f *fake) List(ctx context.Context, id string) ([]cloud.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cloud.Entry
	for key, e := range f.entries {
		if f.parent[key] == id {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fake) Download(ctx context.Context, e cloud.Entry, offset int64) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.data[e.ID]
	if offset > int64(len(b)) {
		return nil, io.EOF
	}
	return io.NopCloser(bytes.NewReader(b[offset:])), nil
}
func (f *fake) Upload(ctx context.Context, parent, name string, file *os.File) (cloud.Entry, error) {
	if f.failUpload {
		return cloud.Entry{}, errors.New("network disconnected")
	}
	file.Seek(0, 0)
	b, _ := io.ReadAll(file)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.add(parent, name, "file", b), nil
}
func (f *fake) add(parent, name, kind string, b []byte) cloud.Entry {
	f.next++
	e := cloud.Entry{ID: fmt.Sprint(f.next), Name: name, Type: kind, Size: int64(len(b)), Revision: fmt.Sprint(f.next), Modified: time.Now().UnixMilli()}
	f.entries[e.ID] = e
	f.parent[e.ID] = parent
	f.data[e.ID] = b
	return e
}
func (f *fake) Mkdir(ctx context.Context, parent, name string) (cloud.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.add(parent, name, "folder", nil), nil
}
func (f *fake) Rename(ctx context.Context, e cloud.Entry, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRename && !strings.HasPrefix(name, ".米盘") {
		return errors.New("rename failed")
	}
	actual, ok := f.entries[e.ID]
	if !ok {
		return os.ErrNotExist
	}
	actual.Name = name
	f.entries[e.ID] = actual
	return nil
}
func (f *fake) Move(ctx context.Context, e cloud.Entry, parent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parent[e.ID] = parent
	return nil
}
func (f *fake) Delete(ctx context.Context, e cloud.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, e.ID)
	delete(f.data, e.ID)
	delete(f.parent, e.ID)
	f.deleted = append(f.deleted, e.ID)
	return nil
}
func fixture(t *testing.T) (*fake, *Handler, *httptest.Server) {
	t.Helper()
	backend := newFake()
	h := NewHandler(New(backend), t.TempDir())
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return backend, h, server
}
func request(t *testing.T, s *httptest.Server, method, p string, body io.Reader, headers map[string]string) (int, []byte, http.Header) {
	t.Helper()
	r, _ := http.NewRequest(method, s.URL+"/dav"+p, body)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}
func TestFinderFileLifecycle(t *testing.T) {
	_, h, s := fixture(t)
	status, b, _ := request(t, s, "MKCOL", "/资料", nil, nil)
	if status != 201 {
		t.Fatalf("MKCOL %d %s", status, b)
	}
	status, b, _ = request(t, s, "PUT", "/资料/你好.txt", strings.NewReader("你好，Finder\n"), nil)
	if status != 201 {
		t.Fatalf("PUT must succeed without a LOCK token: %d %s", status, b)
	}
	status, b, _ = request(t, s, "PROPFIND", "/资料", nil, map[string]string{"Depth": "1"})
	if status != 207 || !bytes.Contains(b, []byte("你好.txt")) {
		t.Fatalf("PROPFIND %d %s", status, b)
	}
	status, b, _ = request(t, s, "GET", "/资料/你好.txt", nil, nil)
	if status != 200 || string(b) != "你好，Finder\n" {
		t.Fatalf("GET %d %s", status, b)
	}
	status, b, _ = request(t, s, "MOVE", "/资料/你好.txt", nil, map[string]string{"Destination": s.URL + "/dav/资料/改名.txt"})
	if status != 201 {
		t.Fatalf("MOVE %d %s", status, b)
	}
	status, b, _ = request(t, s, "PUT", "/资料/改名.txt", strings.NewReader("new"), nil)
	if status != 204 {
		t.Fatalf("overwrite %d %s", status, b)
	}
	status, b, _ = request(t, s, "GET", "/资料/改名.txt", nil, nil)
	if status != 200 || string(b) != "new" {
		t.Fatalf("replace %d %s", status, b)
	}
	status, b, _ = request(t, s, "DELETE", "/资料/改名.txt", nil, nil)
	if status != 204 {
		t.Fatalf("DELETE %d %s", status, b)
	}
	if len(h.Pending()) != 0 {
		t.Fatal("successful upload should remove local staging")
	}
}
func TestRangeAndHead(t *testing.T) {
	f, _, s := fixture(t)
	f.add("0", "movie.mp4", "file", []byte("0123456789"))
	status, b, headers := request(t, s, "GET", "/movie.mp4", nil, map[string]string{"Range": "bytes=3-6"})
	if status != 206 || string(b) != "3456" || headers.Get("Content-Range") != "bytes 3-6/10" {
		t.Fatalf("range %d %s %v", status, b, headers)
	}
	status, b, headers = request(t, s, "HEAD", "/movie.mp4", nil, nil)
	if status != 200 || len(b) != 0 || headers.Get("Content-Length") != "10" {
		t.Fatalf("HEAD %d %s %v", status, b, headers)
	}
}
func TestFailedUploadPreservesOldVersionAndLocalCopy(t *testing.T) {
	f, h, s := fixture(t)
	old := f.add("0", "important.txt", "file", []byte("old"))
	f.failUpload = true
	status, _, _ := request(t, s, "PUT", "/important.txt", strings.NewReader("new"), nil)
	if status != 502 {
		t.Fatalf("want network error 502, got %d", status)
	}
	if string(f.data[old.ID]) != "old" || len(f.deleted) != 0 {
		t.Fatal("failed upload touched old version")
	}
	pending := h.Pending()
	if len(pending) != 1 || pending[0].State != "failed" {
		t.Fatalf("missing failed upload recovery: %+v", pending)
	}
	b, err := os.ReadFile(pending[0].File)
	if err != nil || string(b) != "new" {
		t.Fatalf("recovery lost bytes: %q %v", b, err)
	}
}
func TestReadonlyRejectsMutation(t *testing.T) {
	_, h, s := fixture(t)
	h.FS.ReadOnly = true
	for _, method := range []string{"PUT", "MKCOL", "DELETE", "MOVE", "COPY"} {
		status, _, _ := request(t, s, method, "/x", strings.NewReader("x"), map[string]string{"Destination": s.URL + "/dav/y"})
		if status != 403 {
			t.Errorf("%s: want 403, got %d", method, status)
		}
	}
}
func TestFinderUnicodeNormalization(t *testing.T) {
	f, _, s := fixture(t)
	f.add("0", "café.txt", "file", []byte("ok"))
	status, b, _ := request(t, s, "GET", "/café.txt", nil, nil)
	if status != 200 || string(b) != "ok" {
		t.Fatalf("NFD path %d %s", status, b)
	}
}
func TestMoveOverwritePreservesBothUntilPublish(t *testing.T) {
	f, _, s := fixture(t)
	source := f.add("0", "draft.txt", "file", []byte("new"))
	old := f.add("0", "final.txt", "file", []byte("old"))
	status, b, _ := request(t, s, "MOVE", "/draft.txt", nil, map[string]string{"Destination": s.URL + "/dav/final.txt", "Overwrite": "T"})
	if status != 204 {
		t.Fatalf("safe MOVE overwrite must work: %d %s", status, b)
	}
	if f.entries[source.ID].Name != "final.txt" {
		t.Fatal("new version not published")
	}
	if _, ok := f.entries[old.ID]; ok {
		t.Fatal("old version must be sent to cloud trash after publish")
	}
}
func TestLockPreventsUnrelatedPut(t *testing.T) {
	f, _, s := fixture(t)
	f.add("0", "locked.txt", "file", []byte("old"))
	status, b, headers := request(t, s, "LOCK", "/locked.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`), map[string]string{"Timeout": "Second-120"})
	if status != 200 {
		t.Fatalf("LOCK %d %s", status, b)
	}
	status, _, _ = request(t, s, "PUT", "/locked.txt", strings.NewReader("bad"), nil)
	if status != 423 {
		t.Fatalf("locked PUT %d", status)
	}
	status, b, _ = request(t, s, "PUT", "/locked.txt", strings.NewReader("good"), map[string]string{"If": "(" + headers.Get("Lock-Token") + ")"})
	if status != 204 {
		t.Fatalf("authorized PUT %d %s", status, b)
	}
}
func TestCopyDirectory(t *testing.T) {
	f, _, s := fixture(t)
	folder := f.add("0", "folder", "folder", nil)
	f.add(folder.ID, "file.txt", "file", []byte("content"))
	status, b, _ := request(t, s, "COPY", "/folder", nil, map[string]string{"Destination": s.URL + "/dav/duplicate", "Depth": "infinity"})
	if status != 201 {
		t.Fatalf("directory COPY %d %s", status, b)
	}
	status, b, _ = request(t, s, "GET", "/duplicate/file.txt", nil, nil)
	if status != 200 || string(b) != "content" {
		t.Fatalf("copy contents %d %s", status, b)
	}
}
func TestInterruptedBodyNeverPublishes(t *testing.T) {
	f, h, _ := fixture(t)
	req := httptest.NewRequest("PUT", "http://localhost/dav/partial", io.NopCloser(&brokenReader{}))
	req.ContentLength = 10
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code < 400 {
		t.Fatalf("partial body accepted: %d", w.Code)
	}
	if len(f.entries) != 0 {
		t.Fatal("partial upload published")
	}
	p := h.Pending()
	if len(p) != 1 || p[0].State != "interrupted" {
		t.Fatalf("missing partial recovery: %+v", p)
	}
}

type brokenReader struct{ sent bool }

func (b *brokenReader) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		copy(p, "part")
		return 4, nil
	}
	return 0, io.ErrUnexpectedEOF
}

func TestDirectoryListingNeverDownloadsUnknownFileTypes(t *testing.T) {
	backend := newFake()
	backend.add("0", "receipt.heic", "file", []byte("image bytes"))
	counted := &countingBackend{Backend: backend}
	h := NewHandler(New(counted), t.TempDir())
	s := httptest.NewServer(h)
	defer s.Close()
	status, body, _ := request(t, s, "PROPFIND", "/", nil, map[string]string{"Depth": "1"})
	if status != 207 {
		t.Fatalf("%d %s", status, body)
	}
	if counted.downloads != 0 {
		t.Fatalf("listing a directory downloaded %d files just to identify their types", counted.downloads)
	}
}

type countingBackend struct {
	cloud.Backend
	downloads int
}

func (c *countingBackend) Download(ctx context.Context, e cloud.Entry, offset int64) (io.ReadCloser, error) {
	c.downloads++
	return c.Backend.Download(ctx, e, offset)
}

func TestCanonicalVolumeURL(t *testing.T) {
	_, h, _ := fixture(t)
	r := httptest.NewRequest("PROPFIND", "http://127.0.0.1:1234/小米云盘/", nil)
	r.Header.Set("Depth", "0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 207 || !strings.Contains(w.Body.String(), "%E5%B0%8F%E7%B1%B3%E4%BA%91%E7%9B%98") {
		t.Fatalf("named volume route: %d %s", w.Code, w.Body.String())
	}
}

func TestConcurrentDirectoryReadsShareOneCloudRequest(t *testing.T) {
	backend := &slowListBackend{Backend: newFake(), release: make(chan struct{})}
	fs := New(backend)
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := fs.List(context.Background(), "0"); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(30 * time.Millisecond)
	close(backend.release)
	workers.Wait()
	backend.mu.Lock()
	calls := backend.calls
	backend.mu.Unlock()
	if calls != 1 {
		t.Fatalf("12 simultaneous Finder metadata reads made %d cloud calls", calls)
	}
}
func TestWarmDirectoryReturnsImmediatelyAndRefreshesInBackground(t *testing.T) {
	backend := &slowListBackend{Backend: newFake(), release: make(chan struct{})}
	fs := New(backend)
	fs.cache["0"] = cached{[]cloud.Entry{{ID: "known", Name: "cached.txt"}}, time.Now().Add(-35 * time.Second)}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	items, err := fs.List(ctx, "0")
	close(backend.release)
	if err != nil || len(items) != 1 || items[0].ID != "known" {
		t.Fatalf("cached directory blocked on network: %+v %v", items, err)
	}
}

type slowListBackend struct {
	cloud.Backend
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (b *slowListBackend) List(ctx context.Context, id string) ([]cloud.Entry, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	select {
	case <-b.release:
		return b.Backend.List(ctx, id)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestHeadForUnknownTypeDoesNotDownloadContent(t *testing.T) {
	backend := newFake()
	backend.add("0", "camera.rawxyz", "file", []byte("raw data"))
	counted := &countingBackend{Backend: backend}
	h := NewHandler(New(counted), t.TempDir())
	s := httptest.NewServer(h)
	defer s.Close()
	status, _, _ := request(t, s, "HEAD", "/camera.rawxyz", nil, nil)
	if status != 200 || counted.downloads != 0 {
		t.Fatalf("HEAD status=%d downloaded %d files", status, counted.downloads)
	}
}
func TestDirectoryBrowsingContinuesDuringUpload(t *testing.T) {
	backend := &blockedUpload{Backend: newFake(), entered: make(chan struct{}), release: make(chan struct{})}
	h := NewHandler(New(backend), t.TempDir())
	s := httptest.NewServer(h)
	defer s.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, _ := http.NewRequest("PUT", s.URL+"/dav/file.txt", strings.NewReader("data"))
		resp, err := http.DefaultClient.Do(r)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-backend.entered
	r, _ := http.NewRequest("PROPFIND", s.URL+"/dav/", nil)
	r.Header.Set("Depth", "1")
	client := http.Client{Timeout: 100 * time.Millisecond}
	resp, err := client.Do(r)
	close(backend.release)
	<-done
	if err != nil {
		t.Fatalf("browsing blocked behind upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 207 {
		t.Fatal(resp.StatusCode)
	}
}

type blockedUpload struct {
	cloud.Backend
	entered, release chan struct{}
}

func (b *blockedUpload) Upload(ctx context.Context, parent, name string, f *os.File) (cloud.Entry, error) {
	close(b.entered)
	<-b.release
	return b.Backend.Upload(ctx, parent, name, f)
}

func TestCopyAcknowledgementWaitsForCloudDirectoryIndex(t *testing.T) {
	base := newFake()
	base.add("0", "source.txt", "file", []byte("content"))
	backend := &laggingRenameBackend{Backend: base, old: map[string]string{}, remaining: map[string]int{}}
	h := NewHandler(New(backend), t.TempDir())
	s := httptest.NewServer(h)
	defer s.Close()
	status, body, _ := request(t, s, "COPY", "/source.txt", nil, map[string]string{"Destination": s.URL + "/dav/copied.txt"})
	if status != 201 {
		t.Fatalf("COPY %d %s", status, body)
	}
	status, body, _ = request(t, s, "GET", "/copied.txt", nil, nil)
	if status != 200 || string(body) != "content" {
		t.Fatalf("acknowledged copy is not readable: %d %s", status, body)
	}
}

type laggingRenameBackend struct {
	cloud.Backend
	mu        sync.Mutex
	old       map[string]string
	remaining map[string]int
}

func (b *laggingRenameBackend) Rename(ctx context.Context, e cloud.Entry, name string) error {
	if err := b.Backend.Rename(ctx, e, name); err != nil {
		return err
	}
	b.mu.Lock()
	b.old[e.ID] = e.Name
	b.remaining[e.ID] = 2
	b.mu.Unlock()
	return nil
}
func (b *laggingRenameBackend) List(ctx context.Context, parent string) ([]cloud.Entry, error) {
	items, err := b.Backend.List(ctx, parent)
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range items {
		if b.remaining[items[i].ID] > 0 {
			items[i].Name = b.old[items[i].ID]
			b.remaining[items[i].ID]--
		}
	}
	return items, err
}
