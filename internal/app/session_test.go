package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
)

type sessionTransport func(*http.Request) (*http.Response, error)

func (f sessionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func sessionTestServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	s.Client.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "initial", Domain: ".i.mi.com", Path: "/"}, {Name: "userId", Value: "same-account", Domain: ".i.mi.com", Path: "/"}})
	s.saveSession = func([]byte) error { return nil }
	s.installSessionHooks()
	return s
}
func TestConcurrentRejectedRequestsShareRenewalAndResume(t *testing.T) {
	s := sessionTestServer(t)
	s.Client.HTTP.Transport = sessionTransport(func(r *http.Request) (*http.Response, error) {
		cookie, _ := r.Cookie("serviceToken")
		status := 401
		body := ""
		if cookie != nil && cookie.Value == "renewed" {
			status = 200
			body = `{"code":0,"result":"ok","data":{"records":[]}}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	entered := make(chan struct{}, 8)
	s.Client.RefreshSession = func(ctx context.Context, g uint64) error { entered <- struct{}{}; return s.renewSession(ctx, g) }
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := s.Client.List(ctx, "0")
			results <- err
		}()
	}
	for i := 0; i < 8; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("requests never reached renewal")
		}
	}
	// Wait for the renewal to become visible to the native application's heartbeat.
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		active := s.refreshFlight != nil
		s.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("renewal was never requested")
		}
		time.Sleep(time.Millisecond)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/api/status", nil)
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if status["renewing"] != true || strings.Contains(w.Body.String(), "initial") {
		t.Fatal("native renewal state missing or credentials exposed")
	}
	s.Client.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "renewed", Domain: ".i.mi.com", Path: "/"}, {Name: "userId", Value: "same-account", Domain: ".i.mi.com", Path: "/"}})
	s.finishSessionRefresh(nil, nil)
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatalf("request did not resume: %v", err)
		}
	}
}
func TestRenewalCancellationAndFailurePreserveStoredSession(t *testing.T) {
	for _, reason := range []string{"login_required", "network"} {
		t.Run(reason, func(t *testing.T) {
			s := sessionTestServer(t)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- s.renewSession(ctx, s.Client.Generation()) }()
			deadline := time.Now().Add(time.Second)
			for {
				s.mu.Lock()
				active := s.refreshFlight != nil
				s.mu.Unlock()
				if active {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("renewal was never requested")
				}
				time.Sleep(time.Millisecond)
			}
			r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/session/refresh-failed", strings.NewReader(`{"reason":"`+reason+`"}`))
			r.Header.Set("Authorization", "Bearer admin-secret")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			cancel()
			err := <-done
			if err == nil || !s.Client.HasSession() {
				t.Fatal("renewal failure cleared the saved account session")
			}
			if reason == "network" && errors.Is(err, cloud.ErrAuth) {
				t.Fatal("network failure incorrectly requires login")
			}
		})
	}
	s := sessionTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(s.renewSession(ctx, s.Client.Generation()), context.Canceled) {
		t.Fatal("cancelled request ignored its context")
	}
	s.finishSessionRefresh(nil, context.Canceled)
}
func TestRotatedSessionIsPersistedDuringOrdinaryRequests(t *testing.T) {
	s := sessionTestServer(t)
	var mu sync.Mutex
	var saved []byte
	s.saveSession = func(b []byte) error { mu.Lock(); defer mu.Unlock(); saved = append([]byte(nil), b...); return nil }
	s.Client.HTTP.Transport = sessionTransport(func(r *http.Request) (*http.Response, error) {
		header := http.Header{}
		header.Add("Set-Cookie", "serviceToken=rotated; Domain=.i.mi.com; Path=/; Secure; Max-Age=5184000")
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(`{"code":0,"result":"ok","data":{"records":[]}}`)), Request: r}, nil
	})
	if _, err := s.Client.List(context.Background(), "0"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var cookies []*http.Cookie
	if json.Unmarshal(saved, &cookies) != nil {
		t.Fatal("ordinary API response did not persist its rotated session")
	}
	fresh := cloud.New()
	if fresh.SetCookies(cookies) != nil || !fresh.HasSession() {
		t.Fatal("saved rotated session cannot survive restart")
	}
}
func TestBackgroundRefreshCannotSwitchAccounts(t *testing.T) {
	s := sessionTestServer(t)
	account := s.Client.AccountKey()
	r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/session", strings.NewReader(`{"background":true,"cookies":[{"Name":"serviceToken","Value":"other-token","Domain":".i.mi.com","Path":"/"},{"Name":"userId","Value":"other-account","Domain":".i.mi.com","Path":"/"}]}`))
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 409 || s.Client.AccountKey() != account {
		t.Fatal("background renewal silently switched accounts")
	}
}

func TestRejectedBackgroundProbeKeepsWorkingSessionAndSignalsVerification(t *testing.T) {
	s := sessionTestServer(t)
	s.connected = true
	s.Client.HTTP.Transport = sessionTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/session", strings.NewReader(`{"background":true,"cookies":[{"Name":"serviceToken","Value":"stale-browser-session","Domain":".i.mi.com","Path":"/"},{"Name":"userId","Value":"same-account","Domain":".i.mi.com","Path":"/"}]}`))
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("native cannot distinguish verification from network failure: HTTP %d", w.Code)
	}
	if !s.connected || !s.Client.HasSession() {
		t.Fatal("a failed background probe destroyed the working service session")
	}
}

func TestNativeSessionOwnerDoesNotWriteServiceKeychain(t *testing.T) {
	s := sessionTestServer(t)
	s.nativeSession = true
	writes := 0
	s.saveSession = func([]byte) error { writes++; return nil }
	s.persistSession()
	if writes != 0 {
		t.Fatal("background service accessed Keychain instead of leaving persistence to the native app")
	}
}

func TestUnloadedNativeSessionIsNotReportedAsCloudExpiry(t *testing.T) {
	s := testServer(t)
	s.nativeSession = true
	s.Check()
	if s.authRequired || s.lastError != "" {
		t.Fatal("an unloaded local session was incorrectly reported as Xiaomi rejecting authentication")
	}
}
