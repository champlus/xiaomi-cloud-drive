package dav

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFinderSizedStreamDoesNotWaitForFinalChunk(t *testing.T) {
	b := newFake()
	h := NewHandler(New(b), t.TempDir())
	server := httptest.NewServer(h)
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Finder declares the file length separately and may await our response before
	// closing the producer stream. The content is complete; the chunk terminator is not sent.
	fmt.Fprintf(conn, "PUT /dav/photo.jpg HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nX-Expected-Entity-Length: 6\r\n\r\n6\r\nabcdef\r\n")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal("complete Finder payload remained stuck waiting for its closing chunk:", err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != 201 {
		t.Fatal("Finder stream was not published", response.StatusCode)
	}
	entries, _ := b.List(t.Context(), "0")
	if len(entries) != 1 || entries[0].Size != 6 {
		t.Fatal("Finder stream lost bytes", entries)
	}
}
func TestFinderSizeHintNeverSilentlyTruncatesExtraData(t *testing.T) {
	b := newFake()
	old := b.add("0", "photo.jpg", "file", []byte("old"))
	h := NewHandler(New(b), t.TempDir())
	server := httptest.NewServer(h)
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "PUT /dav/photo.jpg HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nX-Expected-Entity-Length: 3\r\n\r\n6\r\nabcdef\r\n0\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		t.Fatal("incorrect size hint silently truncated or accepted the file")
	}
	if len(h.Pending()) != 1 {
		t.Fatal("size mismatch lost its local recovery copy")
	}
	if _, ok := b.entries[old.ID]; !ok {
		t.Fatal("size mismatch replaced the original")
	}
}

func TestFinderEmptyCreateIsNotACompletedPhotoTransfer(t *testing.T) {
	h := NewHandler(New(newFake()), t.TempDir())
	r := httptest.NewRequest("PUT", "/dav/photo.jpg", strings.NewReader(""))
	r.Header.Set("User-Agent", "WebDAVFS/3.0 Darwin/24.6.0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal("Finder placeholder create failed", w.Code)
	}
	s := h.Transfers.Snapshot()
	if len(s.Active)+len(s.Completed)+len(s.Failed) != 0 {
		t.Fatal("Finder's empty create was shown as a completed photo transfer")
	}
}
