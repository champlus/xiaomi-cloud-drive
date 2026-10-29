package cli

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Runner struct {
	BaseURL, Token, Password string
	HTTP                     *http.Client
}
type Entry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
	ETag     string `json:"etag,omitempty"`
}
type cliError struct {
	code, message string
	exit          int
}

func (e *cliError) Error() string { return e.message }
func invalid(text string) error   { return &cliError{"usage", text, 2} }

const Help = `米盘 · 命令行

用法：mipan [--json] <命令>

  status                       连接状态
  ls [远端路径]                列目录
  stat <远端路径>              文件属性
  cat <远端文件>               输出文件内容
  get <远端文件> <本地文件>    下载（--overwrite 允许覆盖本地文件）
  put <本地文件> <远端路径>    上传（--recursive 上传文件夹）
  mkdir <远端路径>             创建目录
  mv <源路径> <目标路径>       移动或重命名
  cp <源路径> <目标路径>       复制
  rm <远端路径>               删除到云端最近删除（目录须 --recursive）
  refresh                      刷新目录与连接状态
  recoveries                   列出未完成传输的本地副本

路径以云盘根目录为起点，含空格时请加引号。
--json 可放在任意位置；成功退出 0，操作失败 1，用法错误 2。
`

func (r *Runner) Run(args []string, out, errOut io.Writer) int {
	jsonMode, recursive, overwrite := false, false, false
	var words []string
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonMode = true
		case "--recursive", "-r":
			recursive = true
		case "--overwrite":
			overwrite = true
		default:
			words = append(words, arg)
		}
	}
	if len(words) == 0 || words[0] == "help" || words[0] == "--help" || words[0] == "-h" {
		fmt.Fprint(out, Help)
		return 0
	}
	if r.HTTP == nil {
		r.HTTP = &http.Client{Timeout: 30 * time.Minute}
	}
	data, err := r.execute(words, recursive, overwrite, out, jsonMode)
	if err != nil {
		exit, code := 1, "operation_failed"
		var typed *cliError
		if errors.As(err, &typed) {
			exit, code = typed.exit, typed.code
		}
		if jsonMode {
			json.NewEncoder(out).Encode(map[string]any{"schemaVersion": 1, "ok": false, "error": map[string]string{"code": code, "message": err.Error()}})
		} else {
			fmt.Fprintln(errOut, err)
		}
		return exit
	}
	if jsonMode {
		json.NewEncoder(out).Encode(map[string]any{"schemaVersion": 1, "ok": true, "data": data})
	} else if data != nil {
		switch v := data.(type) {
		case []Entry:
			for _, e := range v {
				kind := "file"
				if e.Type == "folder" {
					kind = "dir "
				}
				fmt.Fprintf(out, "%s\t%d\t%s\n", kind, e.Size, e.Name)
			}
		case Entry:
			fmt.Fprintf(out, "%s\t%d\t%s\n", v.Type, v.Size, v.Path)
		default:
			json.NewEncoder(out).Encode(v)
		}
	}
	return 0
}
func remote(p string) (string, error) {
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return "", invalid("远端路径不能包含 ..")
		}
	}
	if strings.ContainsRune(p, 0) {
		return "", invalid("路径无效")
	}
	return path.Clean("/" + p), nil
}
func (r *Runner) endpoint(p string) string {
	u, _ := url.Parse(r.BaseURL)
	u.Path = "/dav" + p
	return u.String()
}
func (r *Runner) request(method, p string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, r.BaseURL+p, body)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(p, "/api/") {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	} else {
		req.SetBasicAuth("xiaomi", r.Password)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("无法连接米盘，请先打开米盘 App")
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &cliError{"http_" + strconv.Itoa(resp.StatusCode), strings.TrimSpace(string(b)), 1}
	}
	return resp, nil
}
func (r *Runner) api(p string, post bool) (any, error) {
	method := "GET"
	if post {
		method = "POST"
	}
	resp, err := r.request(method, "/api/"+p, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var v any
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v, err
}
func (r *Runner) execute(a []string, recursive, overwrite bool, out io.Writer, jsonMode bool) (any, error) {
	command := a[0]
	args := a[1:]
	count := func(n int) error {
		if len(args) != n {
			return invalid("参数数量不正确。运行 mipan --help 查看用法。")
		}
		return nil
	}
	switch command {
	case "status":
		if err := count(0); err != nil {
			return nil, err
		}
		return r.api("status", false)
	case "refresh":
		if err := count(0); err != nil {
			return nil, err
		}
		return r.api("check", true)
	case "recoveries":
		if err := count(0); err != nil {
			return nil, err
		}
		v, err := r.api("status", false)
		if err != nil {
			return nil, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("服务响应无效")
		}
		return m["pending"], nil
	case "ls", "stat":
		if command == "ls" && len(args) == 0 {
			args = []string{"/"}
		}
		if len(args) != 1 {
			return nil, invalid("需要一个远端路径")
		}
		p, err := remote(args[0])
		if err != nil {
			return nil, err
		}
		entries, err := r.list(p, command == "ls")
		if err != nil {
			return nil, err
		}
		if command == "stat" {
			if len(entries) != 1 {
				return nil, errors.New("文件不存在")
			}
			return entries[0], nil
		}
		return entries, nil
	case "cat", "get":
		if command == "cat" {
			if err := count(1); err != nil {
				return nil, err
			}
		} else if err := count(2); err != nil {
			return nil, err
		}
		p, err := remote(args[0])
		if err != nil {
			return nil, err
		}
		resp, err := r.request("GET", escapedDAV(p), nil, nil)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if command == "cat" {
			if jsonMode {
				b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
				if err != nil {
					return nil, err
				}
				if len(b) > 16<<20 {
					return nil, errors.New("JSON 内容输出限 16 MB，请用 get 下载")
				}
				return map[string]string{"path": p, "contentBase64": base64.StdEncoding.EncodeToString(b)}, nil
			}
			_, err = io.Copy(out, resp.Body)
			return nil, err
		}
		dest, err := filepath.Abs(args[1])
		if err != nil {
			return nil, err
		}
		if _, err = os.Stat(dest); err == nil && !overwrite {
			return nil, errors.New("本地目标已存在，使用 --overwrite 覆盖")
		}
		temp, err := os.CreateTemp(filepath.Dir(dest), ".mipan-download-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(temp.Name())
		n, err := io.Copy(temp, resp.Body)
		if err == nil && resp.ContentLength >= 0 && n != resp.ContentLength {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			err = temp.Sync()
		}
		closeErr := temp.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		if err = os.Rename(temp.Name(), dest); err != nil {
			return nil, err
		}
		return map[string]any{"path": p, "localPath": dest, "bytes": n}, nil
	case "put":
		if err := count(2); err != nil {
			return nil, err
		}
		p, err := remote(args[1])
		if err != nil {
			return nil, err
		}
		s, err := os.Stat(args[0])
		if err != nil {
			return nil, err
		}
		if s.IsDir() {
			if !recursive {
				return nil, errors.New("上传文件夹需要 --recursive")
			}
			err = filepath.WalkDir(args[0], func(local string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.Type()&os.ModeSymlink != 0 {
					return errors.New("文件夹含符号链接，请单独选择文件上传")
				}
				rel, _ := filepath.Rel(args[0], local)
				target := p
				if rel != "." {
					target = path.Join(p, filepath.ToSlash(rel))
				}
				if d.IsDir() {
					_, err := r.mutate("MKCOL", target, nil)
					return err
				}
				return r.upload(local, target)
			})
			return map[string]string{"path": p}, err
		}
		if !s.Mode().IsRegular() {
			return nil, errors.New("仅支持普通文件")
		}
		err = r.upload(args[0], p)
		return map[string]any{"path": p, "bytes": s.Size()}, err
	case "mkdir":
		if err := count(1); err != nil {
			return nil, err
		}
		p, err := remote(args[0])
		if err != nil {
			return nil, err
		}
		return r.mutate("MKCOL", p, nil)
	case "mv", "cp":
		if err := count(2); err != nil {
			return nil, err
		}
		src, err := remote(args[0])
		if err != nil {
			return nil, err
		}
		dst, err := remote(args[1])
		if err != nil {
			return nil, err
		}
		if src == "/" || dst == "/" {
			return nil, invalid("不能替换云盘根目录")
		}
		method := "MOVE"
		if command == "cp" {
			method = "COPY"
		}
		return r.mutate(method, src, map[string]string{"Destination": r.endpoint(dst), "Depth": "infinity", "Overwrite": "T"})
	case "rm":
		if err := count(1); err != nil {
			return nil, err
		}
		p, err := remote(args[0])
		if err != nil {
			return nil, err
		}
		if p == "/" {
			return nil, invalid("不能删除云盘根目录")
		}
		if !recursive {
			entries, err := r.list(p, false)
			if err != nil {
				return nil, err
			}
			if len(entries) > 0 && entries[0].Type == "folder" {
				return nil, errors.New("删除目录需要 --recursive")
			}
		}
		return r.mutate("DELETE", p, nil)
	default:
		return nil, invalid("未知命令：" + command + "。运行 mipan --help 查看用法。")
	}
}
func escapedDAV(p string) string { u := url.URL{Path: "/dav" + p}; return u.String() }
func (r *Runner) mutate(method, p string, headers map[string]string) (any, error) {
	resp, err := r.request(method, escapedDAV(p), nil, headers)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return map[string]any{"path": p, "status": resp.StatusCode}, nil
}
func (r *Runner) upload(local, p string) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	resp, err := r.request("PUT", escapedDAV(p), f, map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
func (r *Runner) list(p string, children bool) ([]Entry, error) {
	depth := "0"
	if children {
		depth = "1"
	}
	resp, err := r.request("PROPFIND", escapedDAV(p), nil, map[string]string{"Depth": depth})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var multi struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Status string `xml:"status"`
				Prop   struct {
					Name     string `xml:"displayname"`
					Size     int64  `xml:"getcontentlength"`
					Modified string `xml:"getlastmodified"`
					ETag     string `xml:"getetag"`
					Resource struct {
						Collection *struct{} `xml:"collection"`
					} `xml:"resourcetype"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err = xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&multi); err != nil {
		return nil, err
	}
	entries := []Entry{}
	for _, item := range multi.Responses {
		u, err := url.Parse(item.Href)
		if err != nil {
			continue
		}
		remotePath := strings.TrimPrefix(u.Path, "/dav")
		remotePath = strings.TrimPrefix(remotePath, "/小米云盘")
		remotePath = path.Clean("/" + remotePath)
		if children && remotePath == p {
			continue
		}
		var e Entry
		e.Path = remotePath
		e.Name = path.Base(remotePath)
		e.Type = "file"
		for _, prop := range item.Props {
			if !strings.Contains(prop.Status, "200") {
				continue
			}
			if prop.Prop.Name != "" {
				e.Name = prop.Prop.Name
			}
			e.Size = prop.Prop.Size
			e.Modified = prop.Prop.Modified
			e.ETag = prop.Prop.ETag
			if prop.Prop.Resource.Collection != nil {
				e.Type = "folder"
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}
