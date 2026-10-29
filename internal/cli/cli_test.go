package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runFixture(t *testing.T, h http.HandlerFunc, args ...string) (int, string, string) {
	t.Helper()
	server := httptest.NewServer(h)
	defer server.Close()
	var out, err bytes.Buffer
	r := Runner{BaseURL: server.URL, Token: "admin", Password: "mount", HTTP: server.Client()}
	code := r.Run(args, &out, &err)
	return code, out.String(), err.String()
}
func TestStatusJSONAndAuth(t *testing.T) {
	code, out, err := runFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin" {
			t.Error("missing auth")
		}
		io.WriteString(w, `{"connected":true}`)
	}, "status", "--json")
	var v struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal([]byte(out), &v) != nil || !v.OK || v.Data["connected"] != true || code != 0 || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	if strings.Contains(out, "admin") {
		t.Fatal("secret leaked")
	}
}

const directoryXML = `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>/dav/folder/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response><D:response><D:href>/dav/folder/%E4%BD%A0%E5%A5%BD.txt</D:href><D:propstat><D:prop><D:displayname>你好.txt</D:displayname><D:getcontentlength>3</D:getcontentlength><D:resourcetype/></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`

func TestListJSONDecodesUnicodeAndSkipsSelf(t *testing.T) {
	code, out, err := runFixture(t, func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "xiaomi" || p != "mount" {
			t.Error("wrong DAV auth")
		}
		if r.Method != "PROPFIND" || r.Header.Get("Depth") != "1" {
			t.Error("wrong method")
		}
		w.WriteHeader(207)
		io.WriteString(w, directoryXML)
	}, "--json", "ls", "/folder")
	if code != 0 || strings.Count(out, "你好.txt") != 2 || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestUploadReturnsCloudFailure(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "local.txt")
	os.WriteFile(file, []byte("upload me"), 0600)
	code, out, _ := runFixture(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != "upload me" {
			t.Error("wrong bytes")
		}
		http.Error(w, "upload failed", 502)
	}, "put", file, "/remote.txt", "--json")
	if code != 1 || !strings.Contains(out, `"ok":false`) || !strings.Contains(out, "upload failed") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestRecursiveDeleteRequiresExplicitFlag(t *testing.T) {
	deleted := false
	code, _, err := runFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = true
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(207)
		io.WriteString(w, `<multistatus xmlns="DAV:"><response><href>/dav/folder</href><propstat><prop><resourcetype><collection/></resourcetype></prop><status>HTTP/1.1 200 OK</status></propstat></response></multistatus>`)
	}, "rm", "/folder")
	if code != 1 || deleted || !strings.Contains(err, "--recursive") {
		t.Fatalf("%d deleted=%v %s", code, deleted, err)
	}
}
func TestDownloadIsAtomicOnTruncatedResponse(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "target.txt")
	code, _, _ := runFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.Write([]byte("part"))
	}, "get", "/remote.txt", file)
	if code != 1 {
		t.Fatal("truncated response accepted")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("incomplete file published")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("temporary download leaked")
	}
}
func TestRootDeletionAndTraversalAreRejected(t *testing.T) {
	for _, p := range []string{"/", "/../secret", "/a/../../secret"} {
		code, _, _ := runFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("invalid path reached server") }, "rm", "--recursive", p)
		if code == 0 {
			t.Errorf("accepted %s", p)
		}
	}
}
