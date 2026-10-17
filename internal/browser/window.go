package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

type Window struct {
	X   int  `json:"x"`
	Y   int  `json:"y"`
	W   int  `json:"w"`
	H   int  `json:"h"`
	Max bool `json:"max"`
}

const (
	DefaultWidth  = 1280
	DefaultHeight = 880
	minWidth      = 480
	minHeight     = 360
	maxEdge       = 16384
	maxOffset     = 32768
)

func (w Window) Valid() bool {
	return w.W >= minWidth && w.W <= maxEdge && w.H >= minHeight && w.H <= maxEdge &&
		w.X > -maxOffset && w.X < maxOffset && w.Y > -maxOffset && w.Y < maxOffset
}

func LoadWindow(path string) (Window, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Window{}, false
	}
	var w Window
	if json.Unmarshal(b, &w) != nil || !w.Valid() {
		return Window{}, false
	}
	return w, true
}

func SaveWindow(path string, w Window) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func windowArgs(w *Window) []string {
	if w == nil || !w.Valid() {
		return []string{"--window-size=" + strconv.Itoa(DefaultWidth) + "," + strconv.Itoa(DefaultHeight)}
	}
	args := []string{"--window-size=" + strconv.Itoa(w.W) + "," + strconv.Itoa(w.H)}
	if onScreen(w.X, w.Y, w.W, w.H) {
		args = append(args, "--window-position="+strconv.Itoa(w.X)+","+strconv.Itoa(w.Y))
	}
	if w.Max {
		args = append(args, "--start-maximized")
	}
	return args
}
