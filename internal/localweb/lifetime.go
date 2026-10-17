package localweb

import (
	"context"
	"net/http"
	"sync"
	"time"
)

const (
	HandoffWindow = 10 * time.Second
	byeGrace      = 5 * time.Second
	pingTimeout   = 90 * time.Second
)

type Lifetime struct {
	mu          sync.Mutex
	now         func() time.Time
	started     time.Time
	lastPing    time.Time
	byeAt       time.Time
	browserDone bool
	handoff     bool
	quit        chan struct{}
	quitOnce    sync.Once
}

func NewLifetime(now func() time.Time) *Lifetime {
	if now == nil {
		now = time.Now
	}
	return &Lifetime{now: now, started: now(), quit: make(chan struct{})}
}

func (l *Lifetime) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ping", func(w http.ResponseWriter, r *http.Request) {
		l.Ping()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/bye", func(w http.ResponseWriter, r *http.Request) {
		l.Bye()
		w.WriteHeader(http.StatusNoContent)
	})
}

func (l *Lifetime) Ping() {
	l.mu.Lock()
	l.lastPing = l.now()
	l.mu.Unlock()
}

func (l *Lifetime) Bye() {
	l.mu.Lock()
	l.byeAt = l.now()
	l.mu.Unlock()
}

func (l *Lifetime) BrowserExited(ranFor time.Duration) {
	l.mu.Lock()
	l.browserDone = true
	l.handoff = ranFor < HandoffWindow
	l.mu.Unlock()
}

func (l *Lifetime) NoBrowserProcess() {
	l.mu.Lock()
	l.browserDone, l.handoff = true, true
	l.mu.Unlock()
}

func (l *Lifetime) Quit() { l.quitOnce.Do(func() { close(l.quit) }) }

func (l *Lifetime) Done() <-chan struct{} { return l.quit }

func (l *Lifetime) Reason() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.browserDone {
		return ""
	}
	if !l.handoff {
		return "window closed"
	}
	if !l.byeAt.IsZero() && !l.lastPing.After(l.byeAt) && now.Sub(l.byeAt) >= byeGrace {
		return "page closed"
	}
	last := l.lastPing
	if last.IsZero() {
		last = l.started
	}
	if now.Sub(last) >= pingTimeout {
		return "page stopped responding"
	}
	return ""
}

func (l *Lifetime) Wait(ctx context.Context) string {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "interrupted"
		case <-l.quit:
			return "quit requested"
		case <-t.C:
			if r := l.Reason(); r != "" {
				return r
			}
		}
	}
}
