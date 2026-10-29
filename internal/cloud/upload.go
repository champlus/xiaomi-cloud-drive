package cloud

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const BlockSize = 4 << 20
const MaxFileSize = 4 << 30

type blockInfo struct {
	SHA1 string `json:"sha1"`
	MD5  string `json:"md5"`
	Size int64  `json:"size"`
}

func (c *Client) Upload(ctx context.Context, parent, name string, file *os.File) (Entry, error) {
	stat, err := file.Stat()
	if err != nil {
		return Entry{}, err
	}
	if stat.Size() >= MaxFileSize {
		return Entry{}, errors.New("小米云盘单个文件需小于 4 GB")
	}
	if _, err = file.Seek(0, 0); err != nil {
		return Entry{}, err
	}
	hash := sha1.New()
	blocks := []blockInfo{}
	buf := make([]byte, BlockSize)
	for {
		n, e := io.ReadFull(file, buf)
		if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
			return Entry{}, e
		}
		if n > 0 {
			hash.Write(buf[:n])
			s := sha1.Sum(buf[:n])
			m := md5.Sum(buf[:n])
			blocks = append(blocks, blockInfo{hex.EncodeToString(s[:]), hex.EncodeToString(m[:]), int64(n)})
		}
		if e != nil {
			break
		}
	}
	// The service accepts an empty block for zero-byte files.
	if len(blocks) == 0 {
		s := sha1.Sum(nil)
		m := md5.Sum(nil)
		blocks = append(blocks, blockInfo{hex.EncodeToString(s[:]), hex.EncodeToString(m[:]), 0})
	}
	typ := mime.TypeByExtension(filepath.Ext(name))
	if typ == "" {
		typ = "application/octet-stream"
	}
	typ = strings.Split(typ, ";")[0]
	data, _ := json.Marshal(map[string]any{"fileName": name, "parentId": parent, "size": stat.Size(), "sha1": hex.EncodeToString(hash.Sum(nil)), "mimeType": typ, "channel": "WEB", "localCreateTime": time.Now().UnixMilli(), "localModifyTime": stat.ModTime().UnixMilli()})
	kss, _ := json.Marshal(map[string]any{"block_infos": blocks, "splitSize": BlockSize})
	var created struct {
		Exists   bool   `json:"exists"`
		ID       string `json:"id"`
		UploadID string `json:"uploadId"`
		KSS      struct {
			Nodes  []string `json:"node_urls"`
			Meta   string   `json:"file_meta"`
			Blocks []struct {
				Existed int    `json:"is_existed"`
				Commit  string `json:"commit_meta"`
				Meta    string `json:"block_meta"`
			} `json:"block_metas"`
		} `json:"kss"`
	}
	if err = c.call(ctx, "POST", "/drive/v2/user/files/create", url.Values{"data": {string(data)}, "kss": {string(kss)}}, &created); err != nil {
		return Entry{}, err
	}
	if created.Exists && created.ID != "" {
		return Entry{ID: created.ID, Name: name, Type: "file", Size: stat.Size(), SHA1: hex.EncodeToString(hash.Sum(nil)), Modified: time.Now().UnixMilli()}, nil
	}
	if len(created.KSS.Nodes) == 0 || !storageURL(created.KSS.Nodes[0]) || len(created.KSS.Blocks) != len(blocks) {
		return Entry{}, errors.New("小米返回了无效上传分片")
	}
	commits := []map[string]string{}
	for i, block := range created.KSS.Blocks {
		commit := block.Commit
		if block.Existed != 1 {
			var reply struct {
				Stat   string `json:"stat"`
				Commit string `json:"commit_meta"`
			}
			endpoint := strings.TrimSuffix(created.KSS.Nodes[0], "/") + "/upload_block_chunk?" + url.Values{"chunk_pos": {"0"}, "file_meta": {created.KSS.Meta}, "block_meta": {block.Meta}}.Encode()
			for attempt := 0; attempt < 3; attempt++ {
				r := io.NewSectionReader(file, int64(i)*BlockSize, blocks[i].Size)
				req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, r)
				req.ContentLength = blocks[i].Size
				req.Header.Set("Content-Type", "application/octet-stream")
				req.Header.Set("Origin", c.Base)
				resp, e := c.Storage.Do(req)
				err = e
				if e == nil {
					err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply)
					resp.Body.Close()
					if resp.StatusCode != 200 || reply.Stat != "BLOCK_COMPLETED" || reply.Commit == "" {
						err = errors.New("小米上传分片失败")
					}
				}
				if err == nil {
					break
				}
				select {
				case <-ctx.Done():
					return Entry{}, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * time.Second):
				}
			}
			if err != nil {
				return Entry{}, fmt.Errorf("第 %d 个分片上传失败，请重试", i+1)
			}
			commit = reply.Commit
		}
		if commit == "" {
			return Entry{}, errors.New("上传分片缺少确认信息")
		}
		commits = append(commits, map[string]string{"commit_meta": commit})
	}
	j, _ := json.Marshal(commits)
	var entry Entry
	err = c.call(ctx, "POST", "/drive/v2/user/files/commit", url.Values{"uploadId": {created.UploadID}, "file_meta": {created.KSS.Meta}, "commit_metas": {string(j)}, "parentId": {parent}}, &entry)
	if err != nil {
		return Entry{}, err
	}
	if entry.ID == "" {
		return Entry{}, errors.New("上传确认缺少文件 ID")
	}
	entry.Name = name
	entry.Size = stat.Size()
	entry.SHA1 = hex.EncodeToString(hash.Sum(nil))
	if entry.Type == "" {
		entry.Type = "file"
	}
	return entry, nil
}
