package localweb

import (
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestWindowClosedNormally(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLifetime(c.now)
	l.Ping()
	if r := l.Reason(); r != "" {
		t.Fatalf("early reason %q", r)
	}
	l.BrowserExited(5 * time.Minute)
	if r := l.Reason(); r != "window closed" {
		t.Fatalf("%q", r)
	}
}

func TestHandoffKeepsRunningWhilePageIsAlive(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLifetime(c.now)
	l.BrowserExited(800 * time.Millisecond)
	for i := 0; i < 60; i++ {
		c.add(4 * time.Second)
		l.Ping()
		if r := l.Reason(); r != "" {
			t.Fatalf("stopped at %d: %q", i, r)
		}
	}
	c.add(80 * time.Second)
	if r := l.Reason(); r != "" {
		t.Fatalf("throttled tab treated as gone: %q", r)
	}
	c.add(11 * time.Second)
	if r := l.Reason(); r != "page stopped responding" {
		t.Fatalf("%q", r)
	}
}

func TestHandoffByeAndReload(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLifetime(c.now)
	l.BrowserExited(time.Second)
	l.Ping()
	c.add(time.Second)
	l.Bye()
	c.add(time.Second)
	l.Ping()
	c.add(10 * time.Second)
	if r := l.Reason(); r != "" {
		t.Fatalf("reload treated as close: %q", r)
	}
	l.Bye()
	c.add(4 * time.Second)
	if r := l.Reason(); r != "" {
		t.Fatalf("closed too early: %q", r)
	}
	c.add(2 * time.Second)
	if r := l.Reason(); r != "page closed" {
		t.Fatalf("%q", r)
	}
}

func TestHandoffWithoutAnyPing(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLifetime(c.now)
	l.NoBrowserProcess()
	c.add(89 * time.Second)
	if r := l.Reason(); r != "" {
		t.Fatalf("%q", r)
	}
	c.add(2 * time.Second)
	if r := l.Reason(); r != "page stopped responding" {
		t.Fatalf("%q", r)
	}
}
