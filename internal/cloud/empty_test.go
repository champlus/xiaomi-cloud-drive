package cloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestEmptyFileIsLocalPlaceholderUntilItHasContent(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("empty file must not be sent to Xiaomi, which rejects size=0")
	}))
	defer upstream.Close()
	base := New()
	base.Base = upstream.URL
	dir := t.TempDir()
	adapter := NewEmptyAdapter(base, dir, func() string { return "account-a" })
	file, _ := os.CreateTemp(t.TempDir(), "empty")
	defer file.Close()
	e, err := adapter.Upload(context.Background(), "0", "empty.txt", file)
	if err != nil || e.Size != 0 || calls != 0 {
		t.Fatalf("%+v %v", e, err)
	}
	restarted := NewEmptyAdapter(base, dir, func() string { return "account-a" })
	b, err := restarted.Download(context.Background(), e, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(b)
	b.Close()
	if len(data) != 0 {
		t.Fatal("empty file gained content")
	}
	if err = restarted.Rename(context.Background(), e, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Delete(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}
func TestEmptyFilesAreAccountScoped(t *testing.T) {
	dir := t.TempDir()
	account := "a"
	adapter := NewEmptyAdapter(New(), dir, func() string { return account })
	f, _ := os.CreateTemp(t.TempDir(), "empty")
	defer f.Close()
	e, err := adapter.Upload(context.Background(), "0", "private.txt", f)
	if err != nil {
		t.Fatal(err)
	}
	account = "b"
	if _, err = adapter.Download(context.Background(), e, 0); err == nil {
		t.Fatal("account b read account a placeholder")
	}
}
