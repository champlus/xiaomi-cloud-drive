package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressHasRealSpeedAndOnlyFinishesAfterCommit(t *testing.T) {
	tr := New(filepath.Join(t.TempDir(), "history.json"), nil)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	tr.clock = func() time.Time { return now }
	task := tr.Begin("upload", "/资料/报告.pdf", 200)
	task.Report(Progress{Phase: "uploading", Total: 200})
	now = now.Add(time.Second)
	task.Report(Progress{Phase: "uploading", Bytes: 100, Total: 200, Delta: 100})
	s := tr.Snapshot()
	if len(s.Active) != 1 || s.Active[0].Bytes != 100 || s.Active[0].Speed != 100 || len(s.Completed) != 0 {
		t.Fatalf("missing live file progress: %+v", s)
	}
	now = now.Add(4 * time.Second)
	if tr.Snapshot().Active[0].Speed != 0 {
		t.Fatal("stalled transfer still reports a speed")
	}
	task.Report(Progress{Phase: "committing", Bytes: 200, Total: 200})
	s = tr.Snapshot()
	if len(s.Active) != 1 || s.Active[0].Phase != "committing" || s.Active[0].Speed != 0 || len(s.Completed) != 0 {
		t.Fatal("bytes sent must not imply that cloud commit succeeded")
	}
	task.Finish(nil)
	s = tr.Snapshot()
	if len(s.Active) != 0 || len(s.Completed) != 1 || s.Completed[0].Bytes != 200 || s.Completed[0].FinishedAt == nil {
		t.Fatalf("completion did not move one file to completed: %+v", s)
	}
	task.Report(Progress{Phase: "uploading", Bytes: 20, Total: 200, Delta: 20})
	if tr.Snapshot().Completed[0].Bytes != 200 {
		t.Fatal("late update changed a finished record")
	}
}

func TestHistoryPersistsAndInterruptedFilesDoNotBecomeCompleted(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "private", "history.json")
	account := "account-A"
	scope := func() string { return account }
	tr := New(filename, scope)
	completed := tr.Begin("upload", "/a.txt", 100)
	completed.Finish(nil)
	partial := tr.Begin("download", "/partial.pdf", 100)
	partial.Report(Progress{Phase: "downloading", Bytes: 20, Total: 100, Delta: 20})
	restarted := New(filename, scope)
	s := restarted.Snapshot()
	if len(s.Active) != 0 || len(s.Completed) != 1 || len(s.Failed) != 1 || s.Failed[0].State != "interrupted" || s.Failed[0].Speed != 0 {
		t.Fatalf("restart lost history or invented completion: %+v", s)
	}
	info, err := os.Stat(filename)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("history must be private", err)
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), account) {
		t.Fatal("status leaked account scope")
	}
	account = "account-B"
	s = restarted.Snapshot()
	if len(s.Active)+len(s.Completed)+len(s.Failed) != 0 {
		t.Fatal("another account sees previous filenames")
	}
	restarted.Begin("download", "/b.txt", 50).Finish(nil)
	account = "account-A"
	if len(restarted.Snapshot().Completed) != 1 {
		t.Fatal("switching accounts destroyed earlier history")
	}
}

func TestFailedAndInstantTransfersNeverInventThroughput(t *testing.T) {
	tr := New(filepath.Join(t.TempDir(), "history.json"), nil)
	failed := tr.Begin("upload", "/failed.bin", 100)
	failed.Report(Progress{Phase: "uploading", Bytes: 50, Total: 100, Delta: 50})
	failed.Finish(errors.New("synthetic network failure"))
	s := tr.Snapshot()
	if len(s.Failed) != 1 || len(s.Completed) != 0 || s.Failed[0].Bytes != 50 || s.Failed[0].Error == "" {
		t.Fatalf("failure shown as completion: %+v", s)
	}
	instant := tr.Begin("upload", "/instant.bin", 100)
	instant.Report(Progress{Phase: "uploading", Bytes: 100, Total: 100})
	if tr.Snapshot().Active[0].Speed != 0 {
		t.Fatal("server-side deduplication invented a network speed")
	}
}

func TestHistoryRetentionDoesNotDropActiveFiles(t *testing.T) {
	tr := New(filepath.Join(t.TempDir(), "history.json"), nil)
	active := tr.Begin("download", "/still-downloading.bin", 100)
	for i := 0; i < 205; i++ {
		tr.Begin("upload", "/done.txt", 1).Finish(nil)
	}
	s := tr.Snapshot()
	if len(s.Active) != 1 || s.Active[0].ID != active.ID() || len(s.Completed) != 200 {
		t.Fatalf("unbounded history or lost running file: %d active, %d completed", len(s.Active), len(s.Completed))
	}
}

func TestPreviewReadsCoalesceAndCanceledDownloadIsNotAFailure(t *testing.T) {
	tr := New(filepath.Join(t.TempDir(), "history.json"), nil)
	now := time.Now()
	tr.clock = func() time.Time { return now }
	partial := tr.Begin("download", "/photo.jpg", 100)
	partial.SetPartial(true)
	partial.Finish(nil)
	now = now.Add(time.Second)
	full := tr.Begin("download", "/photo.jpg", 400)
	full.Finish(nil)
	now = now.Add(time.Second)
	again := tr.Begin("download", "/photo.jpg", 100)
	again.SetPartial(true)
	again.Finish(nil)
	s := tr.Snapshot()
	if len(s.Completed) != 1 || s.Completed[0].Partial || s.Completed[0].Bytes != 400 {
		t.Fatalf("preview ranges duplicated or replaced a complete file: %+v", s.Completed)
	}
	canceled := tr.Begin("download", "/cancel.jpg", 100)
	canceled.Finish(context.Canceled)
	s = tr.Snapshot()
	if len(s.Failed) != 0 {
		t.Fatal("closing a preview was shown as a failed file transfer")
	}
	now = now.Add(20 * time.Second)
	tr.Begin("download", "/photo.jpg", 400).Finish(nil)
	if len(tr.Snapshot().Completed) != 2 {
		t.Fatal("coalescing erased a later intentional download")
	}
}
