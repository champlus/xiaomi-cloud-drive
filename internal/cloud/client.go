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
	"net/http/cookiejar"
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
	Base    string
	HTTP    *http.Client
	Storage *http.Client
	mu      sync.RWMutex
	cookies []*http.Cookie
}

func New() *Client {
	jar, _ := cookiejar.New(nil)
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
	var selected []*http.Cookie
	allowed := map[string]bool{"serviceToken": true, "userId": true, "i.mi.com_ph": true, "i.mi.com_slh": true, "i.mi.com_isvalid_servicetoken": true, "i.mi.com_istrudev": true, "deviceId": true, "uLocale": true}
	for _, x := range cookies {
		d := strings.TrimPrefix(x.Domain, ".")
		if allowed[x.Name] && (d == "mi.com" || d == "i.mi.com" || d == "") {
			v := *x
			v.Path = "/"
			v.Secure = true
			selected = append(selected, &v)
		}
	}
	token := ""
	for _, x := range selected {
		if x.Name == "serviceToken" {
			token = x.Value
		}
	}
	if token == "" {
		return ErrAuth
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(c.Base)
	jar.SetCookies(u, selected)
	c.HTTP.Jar = jar
	c.cookies = selected
	return nil
}
func (c *Client) AccountKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, x := range c.cookies {
		if x.Name == "userId" {
			h := sha256.Sum256([]byte(x.Value))
			return hex.EncodeToString(h[:16])
		}
	}
	return "signed-out"
}
func (c *Client) HasSession() bool { return c.token() != "" }
func (c *Client) ClearCookies() {
	c.mu.Lock()
	defer c.mu.Unlock()
	jar, _ := cookiejar.New(nil)
	c.HTTP.Jar = jar
	c.cookies = nil
}
func (c *Client) Cookies() []*http.Cookie {
	c.mu.RLock()
	defer c.mu.RUnlock()
	u, _ := url.Parse(c.Base)
	current := map[string]string{}
	for _, cookie := range c.HTTP.Jar.Cookies(u) {
		current[cookie.Name] = cookie.Value
	}
	result := []*http.Cookie{}
	for _, cookie := range c.cookies {
		if value, ok := current[cookie.Name]; ok {
			copy := *cookie
			copy.Value = value
			result = append(result, &copy)
		}
	}
	return result
}
func (c *Client) token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	u, _ := url.Parse(c.Base)
	for _, x := range c.HTTP.Jar.Cookies(u) {
		if x.Name == "serviceToken" {
			return x.Value
		}
	}
	return ""
}
func (c *Client) call(ctx context.Context, method, path string, v url.Values, out any) error {
	if v == nil {
		v = url.Values{}
	}
	var body io.Reader
	endpoint := c.Base + "/" + strings.TrimPrefix(path, "/")
	if method == http.MethodGet {
		v.Set("ts", fmt.Sprint(time.Now().UnixMilli()))
		endpoint += "?" + v.Encode()
	} else {
		v.Set("serviceToken", c.token())
		body = strings.NewReader(v.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Referer", c.Base+"/drive/h5")
	req.Header.Set("Origin", c.Base)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15")
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	c.mu.RLock()
	resp, err := c.HTTP.Do(req)
	c.mu.RUnlock()
	if err != nil {
		return errors.New("无法连接小米云盘，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode/100 == 3 {
		return ErrAuth
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("小米服务暂时不可用（HTTP %d）", resp.StatusCode)
	}
	var result struct {
		Code        int             `json:"code"`
		R           int             `json:"R"`
		Result      string          `json:"result"`
		Description string          `json:"description"`
		Data        json.RawMessage `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
		return errors.New("小米返回了无法识别的响应，请重新登录")
	}
	if result.Code == 401 || result.Code == 10006 || result.R == 401 {
		return ErrAuth
	}
	if result.Code == 11010 || result.Code == 11001 {
		return ErrEncrypted
	}
	if result.Code != 0 || result.Result != "ok" {
		return fmt.Errorf("小米云盘操作失败（代码 %d）：%s", result.Code, result.Description)
	}
	if out != nil {
		return json.Unmarshal(result.Data, out)
	}
	return nil
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
