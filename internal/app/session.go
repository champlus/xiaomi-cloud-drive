package app

import (
	"context"
	"errors"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
	"github.com/champlus/xiaomi-cloud-drive/internal/keychain"
)

type sessionRefresh struct {
	done chan struct{}
	err  error
}

func (s *Server) installSessionHooks() {
	s.Client.OnSessionChanged = s.persistSession
	s.Client.RefreshSession = s.renewSession
}
func (s *Server) storeSession(b []byte) error {
	if s.nativeSession {
		return nil
	}
	if s.saveSession != nil {
		return s.saveSession(b)
	}
	return keychain.Save(b)
}
func (s *Server) renewSession(ctx context.Context, generation uint64) error {
	if s.Client.Generation() != generation {
		return nil
	}
	if !s.Client.HasSession() {
		return cloud.ErrAuth
	}
	s.mu.Lock()
	// A login can complete between the first generation check and this lock.
	if s.Client.Generation() != generation {
		s.mu.Unlock()
		return nil
	}
	flight := s.refreshFlight
	if flight == nil {
		flight = &sessionRefresh{done: make(chan struct{})}
		s.refreshFlight = flight
		s.authRequired = false
		go func() {
			timer := time.NewTimer(60 * time.Second)
			defer timer.Stop()
			select {
			case <-flight.done:
				return
			case <-timer.C:
				s.finishSessionRefresh(flight, errors.New("自动恢复连接超时，请重试"))
			}
		}()
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-flight.done:
		return flight.err
	}
}
func (s *Server) finishSessionRefresh(expected *sessionRefresh, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.refreshFlight
	if f == nil || (expected != nil && f != expected) {
		return
	}
	f.err = err
	s.refreshFlight = nil
	s.authRequired = errors.Is(err, cloud.ErrAuth)
	if err != nil {
		s.connected = false
		s.lastError = err.Error()
	} else {
		s.connected = true
		s.lastError = ""
	}
	close(f.done)
}
