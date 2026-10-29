package dav

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
	"github.com/champlus/xiaomi-cloud-drive/internal/transfer"
)

type progressBackend struct {
	*fake
	entered, release chan struct{}
	fail             bool
}

func (b *progressBackend) Upload(ctx context.Context, parent, name string, file *os.File) (cloud.Entry, error) {
	transfer.Report(ctx, transfer.Progress{Phase: "uploading", Bytes: 4, Total: 8, Delta: 4})
	close(b.entered)
	<-b.release
	transfer.Report(ctx, transfer.Progress{Phase: "committing", Bytes: 8, Total: 8})
	if b.fail {
		return cloud.Entry{}, errors.New("synthetic commit failure")
	}
	return b.fake.Upload(ctx, parent, name, file)
}
func TestUploadListsFileProgressAndPersistsOneCompletion(t *testing.T) {
	b := &progressBackend{fake: newFake(), entered: make(chan struct{}), release: make(chan struct{})}
	h := NewHandler(New(b), t.TempDir())
	done := make(chan struct{})
	w := httptest.NewRecorder()
	go func() {
		defer close(done)
		h.ServeHTTP(w, httptest.NewRequest("PUT", "/dav/report.pdf", strings.NewReader("12345678")))
	}()
	<-b.entered
	defer func() { close(b.release); <-done }()
	s := h.Transfers.Snapshot()
	if len(s.Active) != 1 || s.Active[0].Path != "/report.pdf" || s.Active[0].Phase != "uploading" || s.Active[0].Bytes != 4 || s.Active[0].Total != 8 || len(s.Completed) != 0 {
		t.Fatalf("upload has no live progress: %+v", s)
	}
}
func TestCompletedUploadSurvivesRestartAndFailedCommitDoesNotComplete(t *testing.T) {
	for _, fail := range []bool{false, true} {
		b := &progressBackend{fake: newFake(), entered: make(chan struct{}), release: make(chan struct{}), fail: fail}
		close(b.release)
		dir := t.TempDir()
		h := NewHandler(New(b), dir)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("PUT", "/dav/report.pdf", strings.NewReader("12345678")))
		s := NewHandler(New(b), dir).Transfers.Snapshot()
		if fail {
			if len(s.Completed) != 0 || len(s.Failed) != 1 || len(h.Pending()) != 1 {
				t.Fatalf("failed commit was completed or lost its recovery: %+v", s)
			}
		} else {
			if w.Code != 201 || len(s.Active) != 0 || len(s.Completed) != 1 || s.Completed[0].Bytes != 8 || len(h.Pending()) != 0 {
				t.Fatalf("missing persistent completed upload: %d %+v", w.Code, s)
			}
		}
	}
}

type gatedReader struct {
	io.Reader
	entered, release chan struct{}
	fail             bool
	started          bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		close(r.entered)
		<-r.release
	}
	if r.fail {
		return 0, errors.New("synthetic download interruption")
	}
	return r.Reader.Read(p)
}

type progressDownload struct {
	*fake
	entered, release chan struct{}
	fail             bool
}

func (b *progressDownload) Download(context.Context, cloud.Entry, int64) (io.ReadCloser, error) {
	return io.NopCloser(io.MultiReader(strings.NewReader("abc"), &gatedReader{Reader: strings.NewReader("def"), entered: b.entered, release: b.release, fail: b.fail})), nil
}
func TestDownloadShowsBytesAndInterruptedReadIsNotCompleted(t *testing.T) {
	for _, fail := range []bool{false, true} {
		b := &progressDownload{fake: newFake(), entered: make(chan struct{}), release: make(chan struct{}), fail: fail}
		b.add("0", "report.pdf", "file", []byte("abcdef"))
		h := NewHandler(New(b), t.TempDir())
		done := make(chan struct{})
		w := httptest.NewRecorder()
		go func() { defer close(done); h.ServeHTTP(w, httptest.NewRequest("GET", "/dav/report.pdf", nil)) }()
		<-b.entered
		s := h.Transfers.Snapshot()
		close(b.release)
		<-done
		if len(s.Active) != 1 || s.Active[0].Direction != "download" || s.Active[0].Bytes != 3 || s.Active[0].Total != 6 {
			t.Fatalf("missing download progress: %+v", s)
		}
		s = h.Transfers.Snapshot()
		if fail {
			if len(s.Completed) != 0 || len(s.Failed) != 1 {
				t.Fatalf("incomplete download shown as completed: %+v", s)
			}
		} else if len(s.Active) != 0 || len(s.Completed) != 1 || s.Completed[0].Bytes != 6 {
			t.Fatalf("download not completed: %+v", s)
		}
	}
}
func TestMetadataDoesNotCreateTransfersAndRangeHasActualPayloadSize(t *testing.T) {
	b := newFake()
	b.add("0", "sample.txt", "file", []byte("abcdef"))
	h := NewHandler(New(b), t.TempDir())
	for _, method := range []string{"HEAD", "PROPFIND"} {
		r := httptest.NewRequest(method, "/dav/sample.txt", nil)
		r.Header.Set("Depth", "0")
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	s := h.Transfers.Snapshot()
	if len(s.Active)+len(s.Completed)+len(s.Failed) != 0 {
		t.Fatal("metadata reads invented downloads")
	}
	r := httptest.NewRequest("GET", "/dav/sample.txt", nil)
	r.Header.Set("Range", "bytes=1-3")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	s = h.Transfers.Snapshot()
	if w.Code != 206 || len(s.Completed) != 1 || s.Completed[0].Bytes != 3 || s.Completed[0].Total != 3 || !s.Completed[0].Partial {
		t.Fatalf("range should show its actual payload, not a full-file download: %d %+v", w.Code, s)
	}
}
