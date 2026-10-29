package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
	"github.com/champlus/xiaomi-cloud-drive/internal/dav"
	"github.com/champlus/xiaomi-cloud-drive/internal/keychain"
)

type Config struct {
	ReadOnly  bool `json:"readOnly"`
	AutoMount bool `json:"autoMount"`
}
type Runtime struct {
	Port     int    `json:"port"`
	Token    string `json:"token"`
	Password string `json:"password"`
	PID      int    `json:"pid"`
}
type Event struct {
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}
type Server struct {
	Dir          string
	Client       *cloud.Client
	FS           *dav.FS
	DAV          *dav.Handler
	Runtime      Runtime
	config       Config
	configMu     sync.Mutex
	events       []Event
	mu           sync.Mutex
	connected    bool
	lastError    string
	sessionMu    sync.Mutex
	sessionBytes []byte
	checking     bool
	lock         *os.File
}

func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func New(dir string) (*Server, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	os.Chmod(dir, 0700)
	lock, err := os.OpenFile(filepath.Join(dir, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("米盘服务已在运行")
	}
	s := &Server{Dir: dir, Client: cloud.New(), config: Config{AutoMount: true}, lock: lock}
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		json.Unmarshal(b, &s.config)
	}
	s.FS = dav.New(cloud.NewEmptyAdapter(s.Client, filepath.Join(dir, "metadata"), s.Client.AccountKey))
	s.FS.ReadOnly = s.config.ReadOnly
	s.DAV = dav.NewHandler(s.FS, filepath.Join(dir, "待恢复文件"))
	s.DAV.Event = s.Event
	if b, err := keychain.Load(); err == nil && len(b) > 0 {
		s.sessionBytes = append([]byte(nil), b...)
		var cookies []*http.Cookie
		if json.Unmarshal(b, &cookies) == nil {
			s.Client.SetCookies(cookies)
		}
	} else if err != nil {
		s.Event("error", err.Error())
	}
	s.Runtime = Runtime{Token: random(), Password: random(), PID: os.Getpid()}
	return s, nil
}
func (s *Server) Event(kind, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(text) > 500 {
		text = text[:500]
	}
	s.events = append(s.events, Event{time.Now(), kind, text})
	if len(s.events) > 40 {
		s.events = s.events[len(s.events)-40:]
	}
	if kind == "error" {
		s.lastError = text
		if text == cloud.ErrAuth.Error() {
			s.connected = false
		}
	}
	if kind == "success" {
		s.lastError = ""
	}
}
func (s *Server) Start() (net.Listener, *http.Server, error) {
	var connection struct {
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if b, e := os.ReadFile(filepath.Join(s.Dir, "connection.json")); e == nil {
		json.Unmarshal(b, &connection)
	}
	if connection.Port > 0 && connection.Password != "" {
		s.Runtime.Password = connection.Password
	} else {
		connection.Port = 0
		s.Runtime.Password = random()
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", connection.Port))
	if err != nil {
		return nil, nil, err
	}
	s.Runtime.Port = listener.Addr().(*net.TCPAddr).Port
	connection.Port = s.Runtime.Port
	connection.Password = s.Runtime.Password
	connBytes, _ := json.Marshal(connection)
	if err = os.WriteFile(filepath.Join(s.Dir, "connection.json"), connBytes, 0600); err != nil {
		listener.Close()
		return nil, nil, err
	}
	data, _ := json.Marshal(s.Runtime)
	if err = os.WriteFile(filepath.Join(s.Dir, "runtime.json"), data, 0600); err != nil {
		listener.Close()
		return nil, nil, err
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	go s.Check()
	return listener, server, nil
}
func (s *Server) Close() {
	os.Remove(filepath.Join(s.Dir, "runtime.json"))
	if s.lock != nil {
		s.lock.Close()
	}
}
func (s *Server) Check() {
	s.mu.Lock()
	if s.checking {
		s.mu.Unlock()
		return
	}
	s.checking = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var err error
	if s.Client.HasSession() {
		_, err = s.Client.List(ctx, "0")
	} else {
		err = cloud.ErrAuth
	}
	if err == nil {
		s.persistSession()
	}
	s.mu.Lock()
	s.checking = false
	s.connected = err == nil
	if err != nil {
		s.lastError = err.Error()
	} else {
		s.lastError = ""
	}
	s.mu.Unlock()
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil || (host != "127.0.0.1" && !strings.EqualFold(host, "MiCloud.local") && host != "xn--9kqt24a11u79e.local" && host != "小米云盘.local") {
		http.Error(w, "Invalid host", 403)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" && origin != "http://"+r.Host {
		http.Error(w, "Invalid origin", 403)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/dav") || strings.HasPrefix(r.URL.Path, "/小米云盘") {
		user, password, ok := r.BasicAuth()
		if !ok || user != "xiaomi" || subtle.ConstantTimeCompare([]byte(password), []byte(s.Runtime.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="小米云盘", charset="UTF-8"`)
			http.Error(w, "需要本地挂载凭据", 401)
			return
		}
		s.DAV.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Runtime.Token)) != 1 {
			http.Error(w, "Unauthorized", 401)
			return
		}
		s.api(w, r)
		return
	}
	http.NotFound(w, r)
}

func respond(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(data)
}
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(out)
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/status":
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		s.mu.Lock()
		events := append([]Event{}, s.events...)
		connected := s.connected
		lastError := s.lastError
		checking := s.checking
		s.mu.Unlock()
		s.configMu.Lock()
		config := s.config
		s.configMu.Unlock()
		respond(w, map[string]any{"version": "0.1.0", "connected": connected, "checking": checking, "error": lastError, "config": config, "events": events, "pending": s.DAV.Pending(), "davURL": "http://127.0.0.1:" + strconv.Itoa(s.Runtime.Port) + "/dav/"})
	case "/api/check":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		s.FS.Invalidate()
		go s.Check()
		respond(w, map[string]bool{"ok": true})
	case "/api/session":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		s.sessionMu.Lock()
		defer s.sessionMu.Unlock()
		var input struct {
			Cookies []*http.Cookie `json:"cookies"`
		}
		if err := decode(w, r, &input); err != nil {
			http.Error(w, "登录会话格式无效", 400)
			return
		}
		// Probe a new client first so a failed login never destroys a working session.
		candidate := cloud.New()
		if err := candidate.SetCookies(input.Cookies); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		if _, err := candidate.List(ctx, "0"); err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		b, _ := json.Marshal(candidate.Cookies())
		if err := keychain.Save(b); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.sessionBytes = append([]byte(nil), b...)
		s.Client.SetCookies(candidate.Cookies())
		s.FS.Invalidate()
		s.mu.Lock()
		s.connected = true
		s.lastError = ""
		s.mu.Unlock()
		s.Event("success", "已连接小米云盘")
		respond(w, map[string]bool{"ok": true})
	case "/api/logout":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		s.FS.Mutation.Lock()
		defer s.FS.Mutation.Unlock()
		s.sessionMu.Lock()
		defer s.sessionMu.Unlock()
		if err := keychain.Delete(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.clearSession()
		s.sessionBytes = nil
		s.mu.Lock()
		s.connected = false
		s.lastError = ""
		s.mu.Unlock()
		respond(w, map[string]bool{"ok": true})
	case "/api/config":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var config Config
		if err := decode(w, r, &config); err != nil {
			http.Error(w, "设置无效", 400)
			return
		}
		s.FS.Mutation.Lock()
		defer s.FS.Mutation.Unlock()
		s.configMu.Lock()
		defer s.configMu.Unlock()
		b, _ := json.MarshalIndent(config, "", "  ")
		tmp := filepath.Join(s.Dir, "settings.json.tmp")
		if err := os.WriteFile(tmp, b, 0600); err != nil {
			http.Error(w, "无法保存设置", 500)
			return
		}
		if err := os.Rename(tmp, filepath.Join(s.Dir, "settings.json")); err != nil {
			http.Error(w, "无法保存设置", 500)
			return
		}
		s.config = config
		s.FS.ReadOnly = config.ReadOnly
		respond(w, map[string]bool{"ok": true})
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) persistSession() {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if !s.Client.HasSession() {
		return
	}
	b, _ := json.Marshal(s.Client.Cookies())
	if bytes.Equal(b, s.sessionBytes) {
		return
	}
	if err := keychain.Save(b); err != nil {
		s.Event("error", err.Error())
		return
	}
	s.sessionBytes = append([]byte(nil), b...)
}
func (s *Server) clearSession() { s.Client.ClearCookies(); s.FS.Invalidate() }
func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "米盘")
}
func Run(ctx context.Context, dir string) error {
	s, err := New(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	listener, httpServer, err := s.Start()
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()
	fmt.Println("米盘本地服务已启动")
	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
