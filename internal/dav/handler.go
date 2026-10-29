package dav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
	"golang.org/x/net/webdav"
)

type Pending struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	File       string    `json:"file"`
	Revision   string    `json:"revision"`
	OriginalID string    `json:"originalId"`
	State      string    `json:"state"`
	Error      string    `json:"error"`
	Size       int64     `json:"size"`
	Time       time.Time `json:"time"`
}
type Handler struct {
	FS      *FS
	DAV     *webdav.Handler
	Dir     string
	Event   func(string, string)
	mu      sync.Mutex
	pending map[string]*Pending
}

func NewHandler(fs *FS, dir string) *Handler {
	h := &Handler{FS: fs, Dir: dir, pending: map[string]*Pending{}}
	os.MkdirAll(dir, 0700)
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, file := range files {
		b, e := os.ReadFile(file)
		if e != nil {
			continue
		}
		var p Pending
		if json.Unmarshal(b, &p) == nil && p.ID != "" {
			if p.State == "receiving" {
				p.State = "interrupted"
				p.Error = "接收时服务中断，副本可能不完整，请从原文件重新复制"
			}
			if p.State == "uploading" {
				p.State = "failed"
				p.Error = "服务中断，请检查云端后重新复制；本地副本已保留"
			}
			h.pending[p.ID] = &p
		}
	}
	h.DAV = &webdav.Handler{Prefix: "/dav", FileSystem: fs, LockSystem: webdav.NewMemLS(), Logger: func(r *http.Request, e error) {
		if e != nil && !errors.Is(e, os.ErrNotExist) && !errors.Is(e, os.ErrPermission) && h.Event != nil {
			h.Event("error", e.Error())
		}
	}}
	return h
}
func (h *Handler) event(kind, text string) {
	if h.Event != nil {
		h.Event(kind, text)
	}
}
func (h *Handler) save(p *Pending) {
	b, _ := json.MarshalIndent(p, "", "  ")
	tmp := filepath.Join(h.Dir, p.ID+".json.tmp")
	if os.WriteFile(tmp, b, 0600) == nil {
		os.Rename(tmp, filepath.Join(h.Dir, p.ID+".json"))
	}
}
func (h *Handler) add(p *Pending) { h.mu.Lock(); h.pending[p.ID] = p; h.save(p); h.mu.Unlock() }
func (h *Handler) update(p *Pending, state string, err error) {
	h.mu.Lock()
	p.State = state
	if err != nil {
		p.Error = err.Error()
	}
	h.save(p)
	h.mu.Unlock()
}
func (h *Handler) done(p *Pending) {
	h.mu.Lock()
	delete(h.pending, p.ID)
	os.Remove(filepath.Join(h.Dir, p.ID+".json"))
	os.Remove(p.File)
	h.mu.Unlock()
}
func (h *Handler) Pending() []Pending {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := []Pending{}
	for _, p := range h.pending {
		r = append(r, *p)
	}
	return r
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := prefix(r)
	if !strings.HasPrefix(r.URL.Path, prefix+"/") && r.URL.Path != prefix {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case "PUT", "MKCOL", "DELETE", "MOVE", "COPY", "PROPPATCH":
		h.FS.Mutation.Lock()
		readOnly := h.FS.ReadOnly
		h.FS.Mutation.Unlock()
		if readOnly {
			h.fail(w, os.ErrPermission)
			return
		}
	}
	// x/net/webdav can emit a successful root-only response when Readdir fails.
	// Resolve a depth-one listing before it writes headers so an expired login
	// is reported as an error instead of appearing to erase the user's drive.
	if r.Method == "PROPFIND" && r.Header.Get("Depth") == "1" {
		entry, err := h.FS.Resolve(r.Context(), strings.TrimPrefix(r.URL.Path, prefix))
		if err != nil {
			h.fail(w, err)
			return
		}
		if entry.IsDir() {
			if _, err = h.FS.List(r.Context(), entry.ID); err != nil {
				h.fail(w, err)
				return
			}
		}
	}
	if r.Method == "GET" || r.Method == "HEAD" {
		if entry, err := h.FS.Resolve(r.Context(), strings.TrimPrefix(r.URL.Path, prefix)); err == nil && !entry.IsDir() {
			contentType, _ := (info{entry}).ContentType(r.Context())
			w.Header().Set("Content-Type", contentType)
		}
	}
	if r.Method == "PUT" {
		h.put(w, r)
		return
	}
	if r.Method == "MOVE" || r.Method == "COPY" {
		// x/net/webdav removes the destination before Rename. Handle overwrites here
		// so the previous file remains intact until the new contents are ready.
		h.transfer(w, r)
		return
	}
	if r.Method == "OPTIONS" {
		w.Header().Set("DAV", "1, 2")
		w.Header().Set("MS-Author-Via", "DAV")
		w.Header().Set("Allow", "OPTIONS, PROPFIND, GET, HEAD, PUT, MKCOL, DELETE, MOVE, COPY, LOCK, UNLOCK, PROPPATCH")
		w.WriteHeader(200)
		return
	}
	handler := *h.DAV
	handler.Prefix = prefix
	handler.ServeHTTP(w, r)
}
func (h *Handler) fail(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, os.ErrNotExist) {
		status = 404
	} else if errors.Is(err, os.ErrExist) {
		status = 412
	} else if errors.Is(err, os.ErrPermission) {
		status = 403
	} else if errors.Is(err, cloud.ErrAuth) {
		status = 503
	} else if errors.Is(err, os.ErrInvalid) {
		status = 409
	}
	h.event("error", err.Error())
	http.Error(w, err.Error(), status)
}
func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	name := clean(strings.TrimPrefix(r.URL.Path, prefix(r)))
	if ignored(name) {
		_, err := io.Copy(io.Discard, io.LimitReader(r.Body, 16<<20))
		if err != nil {
			h.fail(w, err)
			return
		}
		w.WriteHeader(201)
		return
	}
	h.FS.Mutation.Lock()
	defer h.FS.Mutation.Unlock()
	if h.FS.ReadOnly {
		h.fail(w, os.ErrPermission)
		return
	}
	parent, base, err := h.FS.Parent(r.Context(), name)
	if err != nil {
		h.fail(w, err)
		return
	}
	if r.ContentLength >= cloud.MaxFileSize {
		http.Error(w, "小米云盘单个文件需小于 4 GB", 413)
		return
	}
	old, existsErr := h.FS.Resolve(r.Context(), name)
	if existsErr != nil && !errors.Is(existsErr, os.ErrNotExist) {
		h.fail(w, existsErr)
		return
	}
	if old.IsDir() {
		h.fail(w, os.ErrExist)
		return
	}
	if r.Header.Get("If-None-Match") == "*" && existsErr == nil {
		w.WriteHeader(412)
		return
	}
	if match := r.Header.Get("If-Match"); match != "" && match != "*" && match != `"`+old.ID+"-"+old.Revision+`"` {
		w.WriteHeader(412)
		return
	}
	if match := r.Header.Get("If-Match"); match == "*" && existsErr != nil {
		w.WriteHeader(412)
		return
	}
	// Respect locks issued by the shared WebDAV lock system.
	release, lockErr := h.lock(r, name, "")
	if lockErr != nil {
		http.Error(w, "文件正在被其他操作使用", 423)
		return
	}
	defer release()
	idBytes := make([]byte, 12)
	rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)
	file, err := os.OpenFile(filepath.Join(h.Dir, id+".data"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		h.fail(w, err)
		return
	}
	p := &Pending{ID: id, Path: name, File: file.Name(), OriginalID: old.ID, Revision: old.Revision, State: "receiving", Time: time.Now()}
	h.add(p)
	n, err := io.Copy(file, io.LimitReader(r.Body, cloud.MaxFileSize))
	h.mu.Lock()
	p.Size = n
	h.mu.Unlock()
	if err == nil && r.ContentLength >= 0 && n != r.ContentLength {
		err = io.ErrUnexpectedEOF
	}
	if err == nil && n >= cloud.MaxFileSize {
		err = errors.New("小米云盘单个文件需小于 4 GB")
	}
	if err != nil {
		file.Close()
		h.update(p, "interrupted", err)
		h.fail(w, err)
		return
	}
	if err = file.Sync(); err != nil {
		file.Close()
		h.update(p, "failed", err)
		h.fail(w, err)
		return
	}
	h.update(p, "uploading", nil)
	h.event("transfer", "正在上传 "+base)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	entry, err := h.publish(ctx, parent.ID, base, old, file, id)
	file.Close()
	if err != nil {
		h.update(p, "failed", err)
		h.fail(w, err)
		return
	}
	h.done(p)
	h.FS.Invalidate()
	h.event("success", "已上传 "+base)
	w.Header().Set("ETag", `"`+entry.ID+"-"+entry.Revision+`"`)
	if existsErr == nil {
		w.WriteHeader(204)
	} else {
		w.WriteHeader(201)
	}
}
func (h *Handler) publish(ctx context.Context, parent, base string, old cloud.Entry, file *os.File, id string) (cloud.Entry, error) {
	stageName := ".米盘上传-" + id
	fresh, err := h.FS.Backend.Upload(ctx, parent, stageName, file)
	if err != nil {
		return cloud.Entry{}, err
	}
	// Re-read cloud state before replacing, including edits made in the official client.
	h.FS.Invalidate()
	items, err := h.FS.List(ctx, parent)
	if err != nil {
		return cloud.Entry{}, err
	}
	var current cloud.Entry
	for _, e := range items {
		if normName(e.Name) == normName(base) {
			current = e
			break
		}
	}
	if current.ID != old.ID || current.Revision != old.Revision {
		h.FS.Backend.Delete(ctx, fresh)
		return cloud.Entry{}, errors.New("云端同名文件已发生变化，已保留本地副本，请重新复制")
	}
	backup := ".米盘旧版本-" + id
	if old.ID != "" {
		if err = h.FS.Backend.Rename(ctx, old, backup); err != nil {
			h.FS.Backend.Delete(ctx, fresh)
			return cloud.Entry{}, err
		}
	}
	if err = h.FS.Backend.Rename(ctx, fresh, base); err != nil {
		if old.ID != "" {
			if rollback := h.FS.Backend.Rename(ctx, old, base); rollback != nil {
				err = fmt.Errorf("%w；旧文件保留为 %s，请在官方网页检查", err, backup)
			}
		}
		return cloud.Entry{}, err
	}
	if old.ID != "" {
		if err = h.FS.Backend.Delete(ctx, old); err != nil {
			h.event("warning", "新文件已保存，旧版本保留在云端："+backup)
		}
	}
	h.FS.Invalidate()
	return h.FS.Await(ctx, parent, fresh.ID, base, false)
}
func normName(n string) string { return clean(n) }
func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) {
	source := clean(strings.TrimPrefix(r.URL.Path, prefix(r)))
	dest, err := url.Parse(r.Header.Get("Destination"))
	if err != nil || dest.Path == "" || (dest.Host != "" && dest.Host != r.Host) || !strings.HasPrefix(dest.Path, prefix(r)+"/") {
		http.Error(w, "目标地址无效", 400)
		return
	}
	target := clean(strings.TrimPrefix(dest.Path, prefix(r)))
	if source == "/" || target == "/" || strings.HasPrefix(target+"/", source+"/") {
		http.Error(w, "目标文件夹无效", 409)
		return
	}
	if source == target {
		w.WriteHeader(204)
		return
	}
	h.FS.Mutation.Lock()
	defer h.FS.Mutation.Unlock()
	if h.FS.ReadOnly {
		h.fail(w, os.ErrPermission)
		return
	}
	release, err := h.lock(r, source, target)
	if err != nil {
		http.Error(w, "文件正在被其他操作使用", 423)
		return
	}
	defer release()
	e, err := h.FS.Resolve(r.Context(), source)
	if err != nil {
		h.fail(w, err)
		return
	}
	p, n, err := h.FS.Parent(r.Context(), target)
	if err != nil {
		h.fail(w, err)
		return
	}
	old, err := h.FS.Resolve(r.Context(), target)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		h.fail(w, err)
		return
	}
	if exists && r.Header.Get("Overwrite") == "F" {
		w.WriteHeader(412)
		return
	}
	if exists && old.IsDir() != e.IsDir() {
		http.Error(w, "目标文件类型不同", 409)
		return
	}
	idBytes := make([]byte, 12)
	rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	if r.Method == "MOVE" {
		backup := ".米盘旧版本-" + id
		if exists {
			if err = h.FS.Backend.Rename(ctx, old, backup); err != nil {
				h.fail(w, err)
				return
			}
		}
		h.FS.Invalidate()
		if exists {
			if _, err = h.FS.Await(ctx, p.ID, old.ID, backup, false); err != nil {
				h.fail(w, err)
				return
			}
		}
		if err = h.FS.rename(ctx, source, target); err != nil {
			if exists {
				if rollback := h.FS.Backend.Rename(ctx, old, old.Name); rollback != nil {
					err = fmt.Errorf("%w；旧文件保留为 %s", err, backup)
				}
			}
			h.FS.Invalidate()
			h.fail(w, err)
			return
		}
		if exists {
			if err = h.FS.Backend.Delete(ctx, old); err != nil {
				h.event("warning", "旧版本保留在云端："+backup)
			}
		}
		h.FS.Invalidate()
		h.event("success", "已移动 "+n)
		if exists {
			w.WriteHeader(204)
		} else {
			w.WriteHeader(201)
		}
		return
	}
	count := 0
	fresh, err := h.copyEntry(ctx, e, p.ID, ".米盘上传-"+id, r.Header.Get("Depth") == "0", 0, &count)
	if err != nil {
		h.FS.Invalidate()
		h.fail(w, err)
		return
	}
	h.FS.Invalidate()
	items, err := h.FS.List(ctx, p.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	var current cloud.Entry
	for _, item := range items {
		if normName(item.Name) == normName(n) {
			current = item
		}
	}
	if current.ID != old.ID || current.Revision != old.Revision {
		h.FS.Backend.Delete(ctx, fresh)
		h.FS.Invalidate()
		h.fail(w, errors.New("云端目标已发生变化，请重试复制"))
		return
	}
	backup := ".米盘旧版本-" + id
	if exists {
		if err = h.FS.Backend.Rename(ctx, old, backup); err != nil {
			h.fail(w, err)
			return
		}
	}
	if err = h.FS.Backend.Rename(ctx, fresh, n); err != nil {
		if exists {
			h.FS.Backend.Rename(ctx, old, old.Name)
		}
		h.FS.Invalidate()
		h.fail(w, err)
		return
	}
	if exists {
		if err = h.FS.Backend.Delete(ctx, old); err != nil {
			h.event("warning", "旧版本保留在云端："+backup)
		}
	}
	h.FS.Invalidate()
	if _, err = h.FS.Await(ctx, p.ID, fresh.ID, n, false); err != nil {
		h.fail(w, err)
		return
	}
	h.event("success", "已复制 "+n)
	if exists {
		w.WriteHeader(204)
	} else {
		w.WriteHeader(201)
	}
}
func (h *Handler) copyEntry(ctx context.Context, source cloud.Entry, parent, name string, shallow bool, depth int, count *int) (cloud.Entry, error) {
	*count++
	if depth > 128 || *count > 10000 {
		return cloud.Entry{}, errors.New("复制项目过多，请分批复制")
	}
	if source.IsDir() {
		folder, err := h.FS.Backend.Mkdir(ctx, parent, name)
		if err != nil {
			return cloud.Entry{}, err
		}
		if shallow {
			return folder, nil
		}
		items, err := h.FS.Backend.List(ctx, source.ID)
		if err != nil {
			return folder, err
		}
		for _, item := range items {
			if ignored(item.Name) {
				continue
			}
			if _, err = h.copyEntry(ctx, item, folder.ID, item.Name, false, depth+1, count); err != nil {
				h.FS.Backend.Delete(ctx, folder)
				return cloud.Entry{}, err
			}
		}
		return folder, nil
	}
	reader, err := h.FS.Backend.Download(ctx, source, 0)
	if err != nil {
		return cloud.Entry{}, err
	}
	defer reader.Close()
	file, err := os.CreateTemp(h.Dir, "copy-*")
	if err != nil {
		return cloud.Entry{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, reader)
	if err != nil || size != source.Size {
		return cloud.Entry{}, errors.New("文件未完整下载，请重试")
	}
	return h.FS.Backend.Upload(ctx, parent, name, file)
}
func (h *Handler) lock(r *http.Request, source, target string) (func(), error) {
	now := time.Now()
	unlocks := []func(){}
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	var conditions []webdav.Condition
	if value := r.Header.Get("If"); value != "" {
		if strings.Contains(value, "Not") {
			return nil, webdav.ErrConfirmationFailed
		}
		for _, part := range strings.Split(value, "<")[1:] {
			if end := strings.IndexByte(part, '>'); end >= 0 {
				token := part[:end]
				if !strings.Contains(token, "://") {
					conditions = append(conditions, webdav.Condition{Token: token})
				}
			}
		}
		if len(conditions) == 0 {
			return nil, webdav.ErrConfirmationFailed
		}
	}
	for _, name := range []string{source, target} {
		if name == "" {
			continue
		}
		if len(conditions) > 0 {
			if done, err := h.DAV.LockSystem.Confirm(now, name, "", conditions...); err == nil {
				unlocks = append(unlocks, done)
				continue
			}
		}
		token, err := h.DAV.LockSystem.Create(now, webdav.LockDetails{Root: name, Duration: -1, ZeroDepth: true})
		if err != nil {
			release()
			return nil, err
		}
		unlocks = append(unlocks, func() { h.DAV.LockSystem.Unlock(time.Now(), token) })
	}
	return release, nil
}

func prefix(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/小米云盘") {
		return "/小米云盘"
	}
	return "/dav"
}
