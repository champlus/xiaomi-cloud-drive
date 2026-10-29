package transfer

import (
	"context"
	"io"
)

type observerKey struct{}
type observer struct {
	id     string
	report func(Progress)
}

func WithProgress(ctx context.Context, id string, report func(Progress)) context.Context {
	return context.WithValue(ctx, observerKey{}, observer{id, report})
}
func ID(ctx context.Context) string { value, _ := ctx.Value(observerKey{}).(observer); return value.id }
func Report(ctx context.Context, progress Progress) {
	if value, ok := ctx.Value(observerKey{}).(observer); ok && value.report != nil {
		value.report(progress)
	}
}

type Reader struct {
	io.Reader
	Context       context.Context
	Phase         string
	Offset, Total int64
	read          int64
}

func (r *Reader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.read += int64(n)
		Report(r.Context, Progress{Phase: r.Phase, Bytes: r.Offset + r.read, Total: r.Total, Delta: int64(n)})
	}
	return n, err
}
