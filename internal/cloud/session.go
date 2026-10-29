package cloud

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

func allowedCookie(c *http.Cookie) bool {
	domain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
	if domain != "" && domain != "mi.com" && domain != "i.mi.com" {
		return false
	}
	switch c.Name {
	case "serviceToken", "userId", "i.mi.com_ph", "i.mi.com_slh", "i.mi.com_isvalid_servicetoken", "i.mi.com_istrudev", "deviceId", "uLocale":
		return true
	}
	return false
}

type sessionJar struct {
	mu      sync.Mutex
	jar     http.CookieJar
	records map[string]*http.Cookie
}

func newSessionJar() *sessionJar {
	j, _ := cookiejar.New(nil)
	return &sessionJar{jar: j, records: map[string]*http.Cookie{}}
}
func (j *sessionJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.jar.Cookies(u)
}
func (j *sessionJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jar.SetCookies(u, cookies)
	now := time.Now()
	for _, c := range cookies {
		if !allowedCookie(c) {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if domain == "" {
			domain = u.Hostname()
		}
		if u.Hostname() != domain && !strings.HasSuffix(u.Hostname(), "."+domain) {
			continue
		}
		p := c.Path
		if !strings.HasPrefix(p, "/") {
			p = "/"
			if i := strings.LastIndex(u.Path, "/"); i > 0 {
				p = u.Path[:i]
			}
		}
		key := domain + "\n" + p + "\n" + c.Name
		if c.MaxAge < 0 || (c.MaxAge == 0 && !c.Expires.IsZero() && !c.Expires.After(now)) {
			delete(j.records, key)
			continue
		}
		copy := *c
		copy.Domain = domain
		copy.Path = p
		// A relative Max-Age must not start again when a saved session is restored.
		if copy.MaxAge > 0 {
			copy.Expires = now.Add(time.Duration(copy.MaxAge) * time.Second)
			copy.MaxAge = 0
		}
		j.records[key] = &copy
	}
}
func (j *sessionJar) snapshot() []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := []*http.Cookie{}
	for key, c := range j.records {
		if !c.Expires.IsZero() && !c.Expires.After(time.Now()) {
			delete(j.records, key)
			continue
		}
		copy := *c
		result = append(result, &copy)
	}
	sort.Slice(result, func(i, k int) bool {
		a, b := result[i], result[k]
		return a.Domain+"\n"+a.Path+"\n"+a.Name < b.Domain+"\n"+b.Path+"\n"+b.Name
	})
	return result
}
