package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionPreservesRotatedCookieLifetimeOnRestart(t *testing.T) {
	expiry := time.Now().Add(60 * 24 * time.Hour).UTC().Truncate(time.Second)
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "serviceToken", Value: "rotated", Domain: ".i.mi.com", Path: "/", Expires: expiry, Secure: true, HttpOnly: true})
		io.WriteString(w, `{"code":0,"result":"ok","data":{"records":[]}}`)
	})
	initial := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "initial", Domain: ".i.mi.com", Path: "/", Expires: initial, Secure: true}})
	if _, err := c.List(context.Background(), "0"); err != nil {
		t.Fatal(err)
	}
	snapshot := c.Cookies()
	if len(snapshot) != 1 || !snapshot[0].Expires.Equal(expiry) || !snapshot[0].HttpOnly {
		t.Fatal("saved session discarded refreshed expiry or security attributes")
	}
	restarted := New()
	if err := restarted.SetCookies(snapshot); err != nil {
		t.Fatal(err)
	}
	if !restarted.HasSession() {
		t.Fatal("valid two-month session lost on restart")
	}
}

func TestSessionRetainsNewServerCookiesAndRemovesDeletedCookies(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "i.mi.com_slh", Value: "new-session-companion", Domain: ".i.mi.com", Path: "/", Secure: true})
		http.SetCookie(w, &http.Cookie{Name: "deviceId", Value: "", Domain: ".i.mi.com", Path: "/", MaxAge: -1, Secure: true})
		http.SetCookie(w, &http.Cookie{Name: "unrelated", Value: "do-not-persist", Domain: ".i.mi.com", Path: "/", Secure: true})
		io.WriteString(w, `{"code":0,"result":"ok","data":{"records":[]}}`)
	})
	c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "test", Domain: ".i.mi.com", Path: "/"}, {Name: "deviceId", Value: "old-device", Domain: ".i.mi.com", Path: "/"}})
	if _, err := c.List(context.Background(), "0"); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, cookie := range c.Cookies() {
		names[cookie.Name] = true
	}
	if !names["i.mi.com_slh"] || names["deviceId"] || names["unrelated"] {
		t.Fatal("snapshot must retain only current allowed cookies, including newly issued ones")
	}
}

func TestForbiddenAndInvalidResponsesDoNotDemandLogin(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"forbidden", 403, `<html>request denied</html>`},
		{"rate limited", 429, `too many requests`},
		{"gateway HTML", 200, `<html>temporary gateway</html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) })
			_, err := c.List(context.Background(), "0")
			if err == nil || errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "重新登录") {
				t.Fatalf("temporary/forbidden response misclassified as expired login: %v", err)
			}
		})
	}
}

type changingJar struct {
	http.CookieJar
	mu sync.Mutex
	n  int
}

func (j *changingJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.n++
	value := "before-rotation"
	if j.n > 1 {
		value = "after-rotation"
	}
	return []*http.Cookie{{Name: "serviceToken", Value: value}}
}
func TestPostCSRFTokenAndCookieUseSameSessionSnapshot(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		cookie, err := r.Cookie("serviceToken")
		if err != nil || cookie.Value != r.Form.Get("serviceToken") {
			t.Error("request combined credentials from two different session snapshots")
		}
		io.WriteString(w, `{"code":0,"result":"ok","data":{"id":"folder"}}`)
	})
	jar, _ := cookiejar.New(nil)
	c.HTTP.Jar = &changingJar{CookieJar: jar}
	if _, err := c.Mkdir(context.Background(), "0", "sample"); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaExceededIsNotExpiredLogin(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":10006,"result":"error","description":"full"}`)
	})
	_, err := c.List(context.Background(), "0")
	if err == nil || errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "空间") {
		t.Fatalf("cloud-space error was not identified correctly: %v", err)
	}
}
func TestCloudErrorsDoNotEchoUntrustedResponseDescriptions(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":123,"result":"error","description":"private-server-value"}`)
	})
	_, err := c.List(context.Background(), "0")
	if err == nil || strings.Contains(err.Error(), "private-server-value") {
		t.Fatal("diagnostic echoed untrusted response text")
	}
}

func TestExpiredRequestRenewsOnceThenRetriesWithFreshCredentials(t *testing.T) {
	calls, renewals := 0, 0
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		cookie, _ := r.Cookie("serviceToken")
		if cookie.Value == "test" {
			w.WriteHeader(401)
			return
		}
		if cookie.Value != "renewed" || r.Form.Get("serviceToken") != "renewed" {
			t.Error("retry did not use renewed session consistently")
		}
		io.WriteString(w, `{"code":0,"result":"ok","data":{"id":"created"}}`)
	})
	c.RefreshSession = func(ctx context.Context, generation uint64) error {
		renewals++
		return c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "renewed", Domain: ".i.mi.com", Path: "/"}})
	}
	e, err := c.Mkdir(context.Background(), "0", "sample")
	if err != nil || e.ID != "created" || calls != 2 || renewals != 1 {
		t.Fatalf("rejected request was not resumed after one renewal: calls=%d renewals=%d err=%v", calls, renewals, err)
	}
}
func TestRenewalDoesNotLoopAndDoesNotReplayQuotaOrForbidden(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		renewals int
	}{
		{"still unauthorized", 401, "", 1}, {"forbidden", 403, "", 0}, {"quota", 200, `{"code":10006,"result":"error"}`, 0}, {"gateway failure", 502, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, renewals := 0, 0
			c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			c.RefreshSession = func(context.Context, uint64) error { renewals++; return nil }
			_, err := c.Mkdir(context.Background(), "0", "sample")
			if err == nil || renewals != tc.renewals || calls != 1+tc.renewals {
				t.Fatalf("unexpected replay: calls=%d renewals=%d err=%v", calls, renewals, err)
			}
		})
	}
}

func TestRenewalCannotReplayOperationIntoDifferentAccount(t *testing.T) {
	calls := 0
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(401) })
	c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "initial", Domain: ".i.mi.com", Path: "/"}, {Name: "userId", Value: "account-A", Domain: ".i.mi.com", Path: "/"}})
	c.RefreshSession = func(context.Context, uint64) error {
		return c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "renewed", Domain: ".i.mi.com", Path: "/"}, {Name: "userId", Value: "account-B", Domain: ".i.mi.com", Path: "/"}})
	}
	_, err := c.Mkdir(context.Background(), "0", "personal-files")
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "账号") {
		t.Fatal("operation was replayed into an account different from the one that initiated it")
	}
}
func TestFailedRenewalRetainsSafeOriginalAuthDiagnostic(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	c.RefreshSession = func(context.Context, uint64) error { return ErrAuth }
	_, err := c.List(context.Background(), "0")
	var diagnostic *ResponseError
	if !errors.As(err, &diagnostic) || diagnostic.HTTPStatus != 401 || diagnostic.Endpoint != "/drive/v2/user/folders/children" {
		t.Fatal("renewal hid the original safe authentication diagnostic")
	}
}

func TestMaxAgeOverridesOldExpiresWithoutExtendingOnRestore(t *testing.T) {
	c := New()
	if err := c.SetCookies([]*http.Cookie{{Name: "serviceToken", Value: "synthetic", Domain: ".i.mi.com", Path: "/", MaxAge: 5184000, Expires: time.Now().Add(-time.Hour)}}); err != nil {
		t.Fatal("valid Max-Age was overridden by old Expires")
	}
	saved := c.Cookies()
	if len(saved) != 1 || saved[0].MaxAge != 0 || time.Until(saved[0].Expires) < 59*24*time.Hour {
		t.Fatal("relative lifetime was not persisted as an absolute expiry")
	}
	restored := New()
	if err := restored.SetCookies(saved); err != nil {
		t.Fatal(err)
	}
	if !restored.Cookies()[0].Expires.Equal(saved[0].Expires) {
		t.Fatal("restart reset the lifetime instead of retaining the original expiry")
	}
}

func TestCookieRotationAdvancesNativePersistenceVersion(t *testing.T) {
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "serviceToken", Value: "rotated", Domain: ".i.mi.com", Path: "/", Secure: true})
		io.WriteString(w, `{"code":0,"result":"ok","data":{"records":[]}}`)
	})
	before := c.SessionVersion()
	if _, err := c.List(context.Background(), "0"); err != nil {
		t.Fatal(err)
	}
	if c.SessionVersion() <= before {
		t.Fatal("native owner cannot detect newly rotated credentials to save them")
	}
}
