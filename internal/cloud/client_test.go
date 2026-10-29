package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/champlus/xiaomi-cloud-drive/internal/transfer"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

type rewrite struct {
	target string
	base   http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u, _ := url.Parse(r.target)
	clone.URL.Scheme = u.Scheme
	clone.URL.Host = u.Host
	return r.base.RoundTrip(clone)
}
func clientFixture(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c := New()
	c.HTTP.Transport = rewrite{s.URL, http.DefaultTransport}
	c.Storage.Transport = rewrite{s.URL, http.DefaultTransport}
	if err := c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "test", Domain: ".i.mi.com"}}); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestListPaginatesWithoutLosingIDs(t *testing.T) {
	calls := 0
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/drive/v2/user/folders/children" {
			t.Error(r.URL.Path)
		}
		if r.URL.Query().Get("pageNo") == "1" {
			io.WriteString(w, `{"result":"ok","code":0,"data":{"records":[{"id":"9007199254740993123","name":"first","type":"file"}],"hasMore":true}}`)
		} else {
			io.WriteString(w, `{"result":"ok","code":0,"data":{"records":[{"id":"2","name":"last","type":"file"}],"hasMore":false}}`)
		}
	})
	entries, err := c.List(context.Background(), "0")
	if err != nil || calls != 2 || len(entries) != 2 || entries[0].ID != "9007199254740993123" {
		t.Fatalf("%+v %v calls=%d", entries, err, calls)
	}
}
func TestAuthExpired(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	_, err := c.List(context.Background(), "0")
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}
func TestRejectsRepeatedPagination(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":"ok","code":0,"data":{"records":[{"id":"1"}],"hasMore":true}}`)
	})
	_, err := c.List(context.Background(), "0")
	if err == nil {
		t.Fatal("must reject endless repeating pages")
	}
}
func TestUploadV2UsesBoundedBlocksAndCommits(t *testing.T) {
	size := BlockSize + 123
	file, err := os.CreateTemp(t.TempDir(), "upload")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.Write(make([]byte, size))
	blocks := 0
	committed := false
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/drive/v2/user/files/create":
			r.ParseForm()
			if r.Form.Get("serviceToken") != "test" {
				t.Error("missing CSRF service token")
			}
			var data map[string]any
			json.Unmarshal([]byte(r.Form.Get("data")), &data)
			if data["fileName"] != "sample.bin" {
				t.Errorf("wrong metadata %v", data)
			}
			io.WriteString(w, `{"result":"ok","code":0,"data":{"exists":false,"uploadId":"u","kss":{"node_urls":["https://storage.xiaomi.net"],"file_meta":"meta","block_metas":[{"block_meta":"a"},{"block_meta":"b"}]}}}`)
		case "/upload_block_chunk":
			b, _ := io.ReadAll(r.Body)
			if len(b) > BlockSize {
				t.Error("unbounded block")
			}
			if r.Header.Get("Cookie") != "" {
				t.Error("session leaked to storage node")
			}
			blocks++
			fmt.Fprintf(w, `{"stat":"BLOCK_COMPLETED","commit_meta":"c%d"}`, blocks)
		case "/drive/v2/user/files/commit":
			r.ParseForm()
			if r.Form.Get("parentId") != "42" {
				t.Error("missing parent")
			}
			committed = true
			io.WriteString(w, `{"result":"ok","code":0,"data":{"id":"99"}}`)
		default:
			t.Error("unexpected " + r.URL.Path)
			w.WriteHeader(404)
		}
	})
	var progressMu sync.Mutex
	var progress []transfer.Progress
	ctx := transfer.WithProgress(context.Background(), "fixture", func(p transfer.Progress) { progressMu.Lock(); progress = append(progress, p); progressMu.Unlock() })
	e, err := c.Upload(ctx, "42", "sample.bin", file)
	if err != nil || e.ID != "99" || blocks != 2 || !committed {
		t.Fatalf("entry=%+v err=%v blocks=%d commit=%v", e, err, blocks, committed)
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	var sent int64
	for _, p := range progress {
		sent += p.Delta
		if p.Bytes > int64(size) || p.Total != int64(size) {
			t.Fatalf("invalid upload progress: %+v", p)
		}
	}
	if len(progress) == 0 || sent != int64(size) || progress[len(progress)-1].Phase != "committing" {
		t.Fatalf("missing actual network progress: %d bytes in %d updates", sent, len(progress))
	}

}
func TestDownloadRangesAndDoesNotLeakCookies(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/drive/v2/user/files/download":
			io.WriteString(w, `{"result":"ok","code":0,"data":{"downLoads":[{"jsonpUrl":"https://storage.xiaomi.net/rd"}]}}`)
		case "/rd":
			io.WriteString(w, `midrive({"url":"https://storage.xiaomi.net/download","meta":"secret"})`)
		case "/download":
			if r.Header.Get("Range") != "bytes=4-" {
				t.Error("range missing")
			}
			if r.Header.Get("Cookie") != "" {
				t.Error("cookie leaked")
			}
			w.Header().Set("Content-Range", "bytes 4-9/10")
			w.WriteHeader(206)
			io.WriteString(w, "456789")
		}
	})
	body, err := c.Download(context.Background(), Entry{ID: "1"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	b, _ := io.ReadAll(body)
	if string(b) != "456789" {
		t.Fatalf("bad range %s", b)
	}
}
func TestStorageURLTrustBoundary(t *testing.T) {
	for _, s := range []string{"http://storage.xiaomi.net/x", "https://xiaomi.net.attacker.com/x", "https://127.0.0.1/x", "https://user:pass@storage.xiaomi.net/x", "https://storage.xiaomi.net:444/x"} {
		if storageURL(s) {
			t.Errorf("trusted %s", s)
		}
	}
	if !storageURL("https://c3.xmssmc.mws.xiaomi.net/kss_web/rd") {
		t.Fatal("rejected real storage host")
	}
}
func TestCookieScope(t *testing.T) {
	c := New()
	err := c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "bad", Domain: "attacker.com"}})
	if err == nil {
		t.Fatal("accepted foreign cookie")
	}
	err = c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "ok", Domain: ".i.mi.com"}, {Name: "accountToken", Value: "never", Domain: ".mi.com"}})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := json.Marshal(c.Cookies())
	if strings.Contains(string(j), "never") {
		t.Fatal("imported unnecessary account credentials")
	}
}

func TestSessionSnapshotIncludesRefreshedServerCookies(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "serviceToken", Value: "refreshed", Domain: ".i.mi.com", Path: "/", Secure: true})
		io.WriteString(w, `{"result":"ok","code":0,"data":{"records":[],"hasMore":false}}`)
	})
	if _, err := c.List(context.Background(), "0"); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range c.Cookies() {
		if cookie.Name == "serviceToken" {
			if cookie.Value != "refreshed" {
				t.Fatal("persisted session would restore the expired token")
			}
			return
		}
	}
	t.Fatal("snapshot lost service token")
}
