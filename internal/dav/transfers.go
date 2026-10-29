package dav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/champlus/xiaomi-cloud-drive/internal/transfer"
)

type transferResponse struct {
	http.ResponseWriter
	task              *transfer.Task
	direction         string
	status            int
	expected, written int64
	fileSize          int64
	problem           bytes.Buffer
}

func (w *transferResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *transferResponse) WriteHeader(status int) {
	if status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	if w.direction == "download" && status >= 200 && status < 300 {
		if size, err := strconv.ParseInt(w.Header().Get("Content-Length"), 10, 64); err == nil {
			w.expected = size
		}
		fullRange := fmt.Sprintf("bytes 0-%d/%d", w.fileSize-1, w.fileSize)
		w.task.SetPartial(status == http.StatusPartialContent && w.Header().Get("Content-Range") != fullRange)
		w.task.Report(transfer.Progress{Phase: "downloading", Total: w.expected})
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *transferResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	if w.status >= 400 && w.problem.Len() < 500 {
		take := len(p)
		if take > 500-w.problem.Len() {
			take = 500 - w.problem.Len()
		}
		w.problem.Write(p[:take])
	}
	if w.direction == "download" && w.status >= 200 && w.status < 300 && n > 0 {
		w.written += int64(n)
		w.task.Report(transfer.Progress{Phase: "downloading", Bytes: w.written, Total: w.expected, Delta: int64(n)})
	}
	return n, err
}
func (w *transferResponse) finish(ctx context.Context) {
	if w.status == http.StatusNotModified {
		w.task.Discard()
		return
	}
	if w.status < 200 || w.status >= 300 {
		message := strings.TrimSpace(w.problem.String())
		if message == "" {
			message = fmt.Sprintf("传输失败（HTTP %d）", w.status)
		}
		w.task.Finish(errors.New(message))
		return
	}
	if w.direction == "download" && w.expected >= 0 && w.written != w.expected {
		err := ctx.Err()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		w.task.Finish(err)
		return
	}
	w.task.Finish(nil)
}
