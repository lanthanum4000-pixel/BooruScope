package browser

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestWindowArgs(t *testing.T) {
	if got := windowArgs(nil); !slices.Equal(got, []string{"--window-size=1280,880"}) {
		t.Fatalf("default %v", got)
	}
	got := windowArgs(&Window{X: 200, Y: 100, W: 1000, H: 700, Max: true})
	want := []string{"--window-size=1000,700", "--window-position=200,100", "--start-maximized"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := windowArgs(&Window{X: 0, Y: 0, W: 100, H: 100}); !slices.Equal(got, []string{"--window-size=1280,880"}) {
		t.Fatalf("invalid window not ignored: %v", got)
	}
}

func TestWindowRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w", "window.json")
	if _, ok := LoadWindow(p); ok {
		t.Fatal("missing file loaded")
	}
	w := Window{X: -1500, Y: 40, W: 900, H: 640}
	if err := SaveWindow(p, w); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadWindow(p); !ok || got != w {
		t.Fatalf("got %+v %v", got, ok)
	}
}
