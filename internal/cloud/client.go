package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var ErrAuth = errors.New("小米登录已过期，请在设置中重新登录")
var ErrEncrypted = errors.New("此账号开启了端到端加密，目前不支持此模式，请使用小米官方客户端")

type Entry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modifyTime"`
	Created  int64  `json:"createTime"`
	SHA1     string `json:"sha1"`
	Revision string `json:"revision"`
}

func (e Entry) IsDir() bool { return e.Type == "folder" }
func (e Entry) ModTime() time.Time {
	if e.Modified == 0 {
		return time.Unix(0, 0)
	}
	return time.UnixMilli(e.Modified)
}

type Backend interface {
	List(context.Context, string) ([]Entry, error)
	Download(context.Context, Entry, int64) (io.ReadCloser, error)
	Upload(context.Context, string, string, *os.File) (Entry, error)
	Mkdir(context.Context, string, string) (Entry, error)
	Rename(context.Context, Entry, string) error
	Move(context.Context, Entry, string) error
	Delete(context.Context, Entry) error
}

type Client struct {
	Base             string
	HTTP             *http.Client
	Storage          *http.Client
	mu               sync.RWMutex
	generation       uint64
	sessionVersion   uint64
	OnSessionChanged func()
	RefreshSession   func(context.Context, uint64) error
}

func New() *Client {
	jar := newSessionJar()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 45 * time.Second
	return &Client{Base: "https://i.mi.com", HTTP: &http.Client{Jar: jar, Transport: transport, Timeout: 90 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Hostname() != "i.mi.com" {
			return http.ErrUseLastResponse
		}
		if len(via) > 5 {
			return errors.New("重定向过多")
		}
		return nil
	}}, Storage: &http.Client{Transport: transport, Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if !storageURL(r.URL.String()) {
			return errors.New("不受信任的存储节点")
		}
		if len(via) > 5 {
			return errors.New("重定向过多")
		}
		return nil
	}}}
}
func (c *Client) SetCookies(cookies []*http.Cookie) error {
	jar := newSessionJar()
	u, _ := url.Parse(c.Base)
	selected := []*http.Cookie{}
	for _, cookie := range cookies {
		if !allowedCookie(cookie) {
			continue
		}
		copy := *cookie
		if copy.Path == "" {
			copy.Path = "/"
		}
		copy.Secure = true
		selected = append(selected, &copy)
	}
	jar.SetCookies(u, selected)
	hasToken := false
	for _, cookie := range jar.snapshot() {
		if cookie.Name == "serviceToken" && cookie.Value != "" {
			hasToken = true
		}
	}
	if !hasToken {
		return ErrAuth
	}
	c.mu.Lock()
	c.HTTP.Jar = jar
	c.generation++
	c.sessionVersion++
	c.mu.Unlock()
	return nil
}
func (c *Client) AccountKey() string {
	for _, x := range c.Cookies() {
		if x.Name == "userId" {
			h := sha256.Sum256([]byte(x.Value))
			return hex.EncodeToString(h[:16])
		}
	}
	return "signed-out"
}
func (c *Client) HasSession() bool {
	for _, x := range c.Cookies() {
		if x.Name == "serviceToken" && x.Value != "" {
			return true
		}
	}
	return false
}
func (c *Client) ClearCookies() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.HTTP.Jar = newSessionJar()
	c.generation++
	c.sessionVersion++
}
func (c *Client) Cookies() []*http.Cookie {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if jar, ok := c.HTTP.Jar.(*sessionJar); ok {
		return jar.snapshot()
	}
	u, _ := url.Parse(c.Base)
	return c.HTTP.Jar.Cookies(u)
}
func (c *Client) SessionVersion() uint64 { c.mu.RLock(); defer c.mu.RUnlock(); return c.sessionVersion }
func (c *Client) Generation() uint64     { c.mu.RLock(); defer c.mu.RUnlock(); return c.generation }
func (c *Client) call(ctx context.Context, method, path string, v url.Values, out any) error {
	generation := c.Generation()
	account := c.AccountKey()
	err := c.callOnce(ctx, method, path, v, out)
	if !errors.Is(err, ErrAuth) || c.RefreshSession == nil {
		return err
	}
	if refreshErr := c.RefreshSession(ctx, generation); refreshErr != nil {
		if errors.Is(refreshErr, ErrAuth) {
			return err
		}
		return refreshErr
	}
	if c.AccountKey() != account {
		return errors.New("账号已切换，请重新执行原操作")
	}
	return c.callOnce(ctx, method, path, v, out)
}
func (c *Client) callOnce(ctx context.Context, method, path string, v url.Values, out any) error {
	if v == nil {
		v = url.Values{}
	}
	endpoint := c.Base + "/" + strings.TrimPrefix(path, "/")
	u, err := url.Parse(endpoint)
	if err != nil {
		return errors.New("小米接口地址无效")
	}
	c.mu.RLock()
	client := *c.HTTP
	jar := client.Jar
	cookies := jar.Cookies(u)
	generation := c.generation
	c.mu.RUnlock()
	// Freeze both the form token and Cookie header from one snapshot. Imports and
	// parallel responses may rotate the live jar while this request is in flight.
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var body io.Reader
	if method == http.MethodGet {
		v.Set("ts", fmt.Sprint(time.Now().UnixMilli()))
		endpoint += "?" + v.Encode()
	} else {
		v.Del("serviceToken")
		for _, cookie := range cookies {
			if cookie.Name == "serviceToken" {
				v.Set("serviceToken", cookie.Value)
				break
			}
		}
		body = strings.NewReader(v.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	req.Header.Set("Referer", c.Base+"/drive/h5")
	req.Header.Set("Origin", c.Base)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15")
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("无法连接小米云盘，请检查网络后重试")
	}
	defer resp.Body.Close()
	changed := false
	c.mu.Lock()
	if c.generation == generation && len(resp.Cookies()) > 0 {
		before, _ := json.Marshal(cookiesSnapshot(jar, u))
		jar.SetCookies(u, resp.Cookies())
		after, _ := json.Marshal(cookiesSnapshot(jar, u))
		changed = !bytes.Equal(before, after)
		if changed {
			c.sessionVersion++
		}
	}
	notify := c.OnSessionChanged
	c.mu.Unlock()
	if changed && notify != nil {
		notify()
	}
	failure := &ResponseError{HTTPStatus: resp.StatusCode, Endpoint: path}
	if resp.StatusCode == http.StatusUnauthorized {
		failure.Auth = true
		return failure
	}
	if resp.StatusCode/100 == 3 {
		target, _ := url.Parse(resp.Header.Get("Location"))
		if target != nil {
			target = u.ResolveReference(target)
		}
		if target != nil && (target.Hostname() == "account.xiaomi.com" || target.Hostname() == "account.xiaomi.cn" || (target.Hostname() == "i.mi.com" && strings.HasPrefix(target.Path, "/sts"))) {
			failure.Auth = true
		}
		return failure
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusForbidden {
		return failure
	}
	var result struct {
		Code   int             `json:"code"`
		R      int             `json:"R"`
		Result string          `json:"result"`
		Data   json.RawMessage `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
		return failure
	}
	failure.Code = result.Code
	failure.Auth = result.Code == 401 || result.R == 401
	if failure.Auth || resp.StatusCode != http.StatusOK {
		return failure
	}
	if result.Code == 11010 || result.Code == 11001 {
		return ErrEncrypted
	}
	if result.Code != 0 || result.Result != "ok" {
		return failure
	}
	if out != nil {
		return json.Unmarshal(result.Data, out)
	}
	return nil
}
func cookiesSnapshot(jar http.CookieJar, u *url.URL) []*http.Cookie {
	if saved, ok := jar.(*sessionJar); ok {
		return saved.snapshot()
	}
	return jar.Cookies(u)
}

// ResponseError retains only safe diagnostics, never response bodies or URLs
// containing authentication parameters.
type ResponseError struct {
	HTTPStatus int
	Code       int
	Endpoint   string
	Auth       bool
}

func (e *ResponseError) Is(target error) bool { return e.Auth && target == ErrAuth }
func (e *ResponseError) Error() string {
	if e.Auth {
		return fmt.Sprintf("%s（HTTP %d，代码 %d）", ErrAuth, e.HTTPStatus, e.Code)
	}
	if e.Code == 10006 {
		return "小米云空间已满，请清理空间或扩容后重试"
	}
	if e.HTTPStatus == 403 {
		return "小米暂时拒绝访问，请稍后重试（HTTP 403）"
	}
	if e.HTTPStatus == 429 {
		return "请求过于频繁，请稍后重试（HTTP 429）"
	}
	return fmt.Sprintf("小米服务响应异常，请稍后重试（HTTP %d，代码 %d）", e.HTTPStatus, e.Code)
}
func (c *Client) List(ctx context.Context, id string) ([]Entry, error) {
	all := []Entry{}
	seen := map[string]bool{}
	for page := 1; page <= 100000; page++ {
		var data struct {
			Records []Entry `json:"records"`
			HasMore bool    `json:"hasMore"`
		}
		err := c.call(ctx, "GET", "/drive/v2/user/folders/children", url.Values{"parentId": {id}, "pageNo": {fmt.Sprint(page)}, "limit": {"100"}, "order": {"NAME"}, "reverse": {"false"}}, &data)
		if err != nil {
			return nil, err
		}
		n := 0
		for _, e := range data.Records {
			if !seen[e.ID] {
				seen[e.ID] = true
				all = append(all, e)
				n++
			}
		}
		if !data.HasMore {
			return all, nil
		}
		if n == 0 {
			return nil, errors.New("小米分页响应异常，请稍后重试")
		}
	}
	return nil, errors.New("目录项目过多")
}
func (c *Client) Mkdir(ctx context.Context, parent, name string) (Entry, error) {
	var e Entry
	err := c.call(ctx, "POST", "/drive/v2/user/folders/create", url.Values{"parentId": {parent}, "name": {name}}, &e)
	return e, err
}
func (c *Client) Rename(ctx context.Context, e Entry, name string) error {
	return c.call(ctx, "POST", "/drive/v2/user/records/rename", url.Values{"id": {e.ID}, "newName": {name}, "channel": {"WEB"}}, nil)
}
func (c *Client) Move(ctx context.Context, e Entry, parent string) error {
	return c.task(ctx, "MOVE", map[string]string{"id": e.ID, "type": e.Type, "parentId": parent})
}
func (c *Client) Delete(ctx context.Context, e Entry) error {
	return c.task(ctx, "DELETE", map[string]string{"id": e.ID, "type": e.Type})
}
func (c *Client) task(ctx context.Context, op string, record any) error {
	records, _ := json.Marshal([]any{record})
	var task struct {
		ID       string `json:"taskId"`
		Interval int    `json:"interval"`
	}
	if err := c.call(ctx, "POST", "/drive/v2/user/records/filemanager", url.Values{"operateType": {op}, "operateRecords": {string(records)}}, &task); err != nil {
		return err
	}
	if task.ID == "" {
		return errors.New("小米未返回操作任务 ID")
	}
	delay := time.Duration(task.Interval) * time.Millisecond
	if delay < 200*time.Millisecond {
		delay = 200 * time.Millisecond
	}
	if delay > 3*time.Second {
		delay = 3 * time.Second
	}
	for {
		var r struct {
			Status  string            `json:"status"`
			ErrCode int               `json:"errCode"`
			Failed  []json.RawMessage `json:"failedRecords"`
		}
		if err := c.call(ctx, "GET", "/drive/v2/user/records/taskquery", url.Values{"taskId": {task.ID}}, &r); err != nil {
			return err
		}
		if r.Status != "RUNNING" {
			if r.Status == "FAILED" || r.ErrCode != 0 || len(r.Failed) > 0 {
				return fmt.Errorf("小米云盘%s失败（代码 %d）", op, r.ErrCode)
			}
			if r.Status != "SUCCESS" && r.Status != "SUCCEEDED" && r.Status != "FINISHED" {
				return fmt.Errorf("小米任务状态异常：%s", r.Status)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}
func storageURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	h := u.Hostname()
	for _, d := range []string{"xiaomi.net", "mi.com", "mi-img.com", "xiaomi.com"} {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}
func (c *Client) Download(ctx context.Context, e Entry, offset int64) (io.ReadCloser, error) {
	var data struct {
		Downloads []struct {
			URL         string          `json:"jsonpUrl"`
			EncryptInfo json.RawMessage `json:"encryptInfo"`
		} `json:"downLoads"`
	}
	ids, _ := json.Marshal([]string{e.ID})
	if err := c.call(ctx, "GET", "/drive/v2/user/files/download", url.Values{"ids": {string(ids)}, "jsonpCallback": {"midrive"}}, &data); err != nil {
		return nil, err
	}
	if len(data.Downloads) != 1 || !storageURL(data.Downloads[0].URL) {
		return nil, errors.New("小米未返回有效下载地址，可能开启了端到端加密")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", data.Downloads[0].URL, nil)
	resp, err := c.Storage.Do(req)
	if err != nil {
		return nil, errors.New("无法连接小米下载节点")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	start := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if start < 0 || end <= start {
		return nil, errors.New("下载地址响应无效")
	}
	var target struct {
		URL  string `json:"url"`
		Meta string `json:"meta"`
	}
	if json.Unmarshal(b[start+1:end], &target) != nil || !storageURL(target.URL) {
		return nil, errors.New("下载节点无效")
	}
	req, _ = http.NewRequestWithContext(ctx, "POST", target.URL, strings.NewReader(url.Values{"meta": {target.Meta}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err = c.Storage.Do(req)
	if err != nil {
		return nil, errors.New("无法下载文件，请检查网络")
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		resp.Body.Close()
		return nil, fmt.Errorf("下载失败（HTTP %d）", resp.StatusCode)
	}
	if offset > 0 && resp.StatusCode == 200 {
		if _, err = io.CopyN(io.Discard, resp.Body, offset); err != nil {
			resp.Body.Close()
			return nil, err
		}
	}
	if offset > 0 && resp.StatusCode == 206 && !strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset)) {
		resp.Body.Close()
		return nil, errors.New("小米返回了错误的文件分段")
	}
	return resp.Body, nil
}
