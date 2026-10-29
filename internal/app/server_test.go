package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
	"github.com/champlus/xiaomi-cloud-drive/internal/dav"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	c := cloud.New()
	s := &Server{Dir: dir, Client: c, Runtime: Runtime{Token: "admin-secret", Password: "mount-secret"}}
	s.FS = dav.New(c)
	s.DAV = dav.NewHandler(s.FS, filepath.Join(dir, "recovery"))
	return s
}
func TestLocalAccessBoundary(t *testing.T) {
	s := testServer(t)
	cases := []struct {
		name, host, origin, auth string
		want                     int
	}{{"authenticated", "127.0.0.1:1234", "", "Bearer admin-secret", 200}, {"unauthenticated", "127.0.0.1:1234", "", "", 401}, {"dns rebinding", "evil.example:1234", "", "Bearer admin-secret", 403}, {"cross origin", "127.0.0.1:1234", "https://evil.example", "Bearer admin-secret", 403}, {"null origin", "127.0.0.1:1234", "null", "Bearer admin-secret", 403}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1:1234/api/status", nil)
			r.Host = tc.host
			r.Header.Set("Authorization", tc.auth)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Errorf("want %d got %d", tc.want, w.Code)
			}
		})
	}
}
func TestSettingsDoNotExposeSecrets(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/api/status", nil)
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("status leaked credentials")
	}
}
func TestWebDAVAlwaysRequiresMountPassword(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("OPTIONS", "http://127.0.0.1:1234/dav/", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r.SetBasicAuth("xiaomi", "mount-secret")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestSaveSettingsPersistently(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/config", strings.NewReader(`{"autoMount":true,"readOnly":true}`))
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if json.Unmarshal(b, &config) != nil || !config.ReadOnly || !config.AutoMount || !s.FS.ReadOnly {
		t.Fatal("settings not applied")
	}
}
func TestSessionLogoutDoesNotBreakSubsequentLogin(t *testing.T) {
	s := testServer(t)
	original := s.Client
	s.clearSession()
	if original != s.Client || s.FS.Backend != original {
		t.Fatal("logout replaced backend instead of clearing its session")
	}
}
func TestConnectionCredentialsSurviveServiceRestart(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t)
	s.Dir = dir
	s.Runtime.Password = ""
	listener, server, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	_ = server
	first := s.Runtime
	listener.Close()
	s.Close()
	s2 := testServer(t)
	s2.Dir = dir
	s2.Runtime.Password = ""
	listener, _, err = s2.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer s2.Close()
	if first.Port != s2.Runtime.Port || first.Password != s2.Runtime.Password {
		t.Fatal("Finder credentials changed across restart")
	}
}

var _ http.Handler = (*Server)(nil)

func TestNamedLoopbackHostIsAccepted(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "http://xn--9kqt24a11u79e.local:1234/api/status", nil)
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("named local service refused: %d", w.Code)
	}
}

func TestMiCloudLoopbackHostIsAccepted(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "http://MiCloud.local:1234/api/status", nil)
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("MiCloud host refused: %d", w.Code)
	}
}

func TestAuthFailureMarksConnectionExpired(t *testing.T) {
	s := testServer(t)
	s.connected = true
	s.Event("error", cloud.ErrAuth.Error())
	if s.connected {
		t.Fatal("expired session still reported connected")
	}
}

func TestNativeSessionSnapshotRequiresLocalAuthentication(t *testing.T) {
	s := testServer(t)
	s.Client.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "synthetic-session-value", Domain: ".i.mi.com", Path: "/"}})
	for _, authorized := range []bool{false, true} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:1234/api/session/cookies", nil)
		if authorized {
			r.Header.Set("Authorization", "Bearer admin-secret")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if !authorized && (w.Code != 401 || strings.Contains(w.Body.String(), "synthetic-session-value")) {
			t.Fatal("session cookies escaped local authorization")
		}
		if authorized && (w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic-session-value")) {
			t.Fatal("native refresher cannot obtain current rotated cloud session")
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/api/status", nil)
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "synthetic-session-value") {
		t.Fatal("status leaked cloud session")
	}
}

func TestStatusReportsBuildVersion(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/api/status", nil)
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var state map[string]any
	json.Unmarshal(w.Body.Bytes(), &state)
	if state["version"] != Version {
		t.Fatal("status does not report the version injected at build time")
	}
}
