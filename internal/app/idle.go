package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/encoder"
)

const loadLimit = 10 * time.Minute

type IdleStatus struct {
	Enabled  bool  `json:"enabled"`
	Minutes  int   `json:"minutes"`
	UnloadAt int64 `json:"unloadAt"`
}

func (s *Server) idleDuration(set Settings) time.Duration {
	return time.Duration(set.IdleMinutes) * time.Minute
}

func (s *Server) touch() {
	s.mu.Lock()
	s.lastActive = s.now()
	s.mu.Unlock()
}

func (s *Server) beginWork() {
	s.mu.Lock()
	s.inflight++
	s.mu.Unlock()
}

func (s *Server) endWork() {
	s.mu.Lock()
	s.inflight--
	s.lastActive = s.now()
	s.mu.Unlock()
}

func (s *Server) idleStatus() IdleStatus {
	set := s.Settings()
	st := IdleStatus{Enabled: set.IdleUnload, Minutes: set.IdleMinutes}
	s.mu.Lock()
	defer s.mu.Unlock()
	if set.IdleUnload && s.inflight == 0 && s.cfg.Encoder.Status().State == encoder.StateReady {
		st.UnloadAt = s.lastActive.Add(s.idleDuration(set)).UnixMilli()
	}
	return st
}

func (s *Server) idleTick() {
	set := s.Settings()
	state := s.cfg.Encoder.Status().State
	s.mu.Lock()
	if state == encoder.StateReady && s.prevState != encoder.StateReady {
		s.lastActive = s.now()
	}
	s.prevState = state
	due := set.IdleUnload && state == encoder.StateReady && s.inflight == 0 && !s.now().Before(s.lastActive.Add(s.idleDuration(set)))
	if due {
		s.cfg.Library.SetInt8Loaded(false)
	}
	s.mu.Unlock()
	if due {
		s.cfg.Logf("encoder: idle for %d min, unloading CLIP and int8", set.IdleMinutes)
		s.cfg.Encoder.Unload()
	}
}

func (s *Server) RunIdle(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.idleTick()
		}
	}
}

func (s *Server) modelCached() bool {
	m := s.cfg.Model
	if m == "" {
		m = encoder.DefaultModel
	}
	if st, err := os.Stat(m); err == nil && st.IsDir() {
		return true
	}
	if s.cfg.HFHome == "" {
		return false
	}
	snaps := filepath.Join(s.cfg.HFHome, "hub", "models--"+strings.ReplaceAll(m, "/", "--"), "snapshots")
	matches, _ := filepath.Glob(filepath.Join(snaps, "*", "config.json"))
	return len(matches) > 0
}

func (s *Server) encoderEnv() []string {
	if s.Settings().Offline && s.modelCached() {
		return []string{"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1"}
	}
	return nil
}

func (s *Server) StartEncoder() {
	s.cfg.Library.SetInt8Loaded(true)
	s.cfg.Encoder.SetExtraEnv(s.encoderEnv())
	s.cfg.Encoder.Start()
}

func (s *Server) ensureEncoder(ctx context.Context) error {
	st := s.cfg.Encoder.Status()
	if st.State == encoder.StateUnloaded || st.State == encoder.StateIdle {
		s.cfg.Logf("encoder: loading CLIP for a search")
		s.StartEncoder()
	}
	ctx, cancel := context.WithTimeout(ctx, loadLimit)
	defer cancel()
	for {
		switch s.cfg.Encoder.Status().State {
		case encoder.StateReady:
			return nil
		case encoder.StateError:
			return encoder.ErrNotReady
		}
		select {
		case <-ctx.Done():
			return encoder.ErrNotReady
		case <-time.After(150 * time.Millisecond):
		}
	}
}
