package dav

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
)

func uploadSize(r *http.Request) (int64, bool, error) {
	if r.ContentLength >= 0 {
		return r.ContentLength, false, nil
	}
	hint := strings.TrimSpace(r.Header.Get("X-Expected-Entity-Length"))
	if hint == "" {
		return -1, false, nil
	}
	size, err := strconv.ParseInt(hint, 10, 64)
	if err != nil || size < 0 {
		return -1, false, errors.New("文件大小声明无效")
	}
	return size, true, nil
}
func finderPlaceholder(r *http.Request) bool {
	return r.ContentLength == 0 && strings.Contains(strings.ToLower(r.UserAgent()), "webdavfs")
}
func receiveFile(w http.ResponseWriter, r *http.Request, file *os.File, expected int64, hinted bool) (int64, error) {
	if !hinted {
		return io.Copy(file, io.LimitReader(r.Body, cloud.MaxFileSize))
	}
	n, err := io.CopyN(file, r.Body, expected)
	if err != nil {
		return n, err
	}
	// WebDAVFS can leave its producer stream open after the declared file bytes.
	// Briefly check for extra data; never publish a silently truncated payload.
	controller := http.NewResponseController(w)
	deadlineSet := controller.SetReadDeadline(time.Now().Add(250*time.Millisecond)) == nil
	closing := false
	defer func() {
		if !closing {
			controller.SetReadDeadline(time.Time{})
		}
	}()
	var extra [1]byte
	count, readErr := r.Body.Read(extra[:])
	if count > 0 {
		if _, err = file.Write(extra[:count]); err != nil {
			return n + int64(count), err
		}
		controller.EnableFullDuplex()
		closing = true
		controller.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		return n + int64(count), errors.New("接收内容超过文件声明大小，本地副本已保留")
	}
	if errors.Is(readErr, io.EOF) {
		return n, nil
	}
	var timeout net.Error
	if deadlineSet && errors.As(readErr, &timeout) && timeout.Timeout() {
		// Respond before net/http attempts to drain the still-open request body.
		if err = controller.EnableFullDuplex(); err != nil {
			return n, err
		}
		closing = true
		w.Header().Set("Connection", "close")
		return n, nil
	}
	if readErr == nil {
		readErr = io.ErrNoProgress
	}
	return n, readErr
}
