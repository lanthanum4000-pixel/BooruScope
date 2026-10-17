package setup

import (
	"errors"
	"os"
	"path/filepath"
)

var (
	ErrNoPicker      = errors.New("no folder picker available")
	ErrPickCancelled = errors.New("folder selection cancelled")
)

type DirCheck struct {
	Dir       string   `json:"dir"`
	Free      int64    `json:"free"`
	FreeKnown bool     `json:"freeKnown"`
	Writable  bool     `json:"writable"`
	HasApp    bool     `json:"hasApp"`
	Existing  []string `json:"existing"`
}

func nearestExisting(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			return d
		}
	}
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".booruscope-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func CheckDir(dir string) (DirCheck, error) {
	abs, err := filepath.Abs(dir)
	if err != nil || dir == "" {
		return DirCheck{}, errors.New("invalid folder")
	}
	c := DirCheck{Dir: abs}
	base := nearestExisting(abs)
	if n, err := freeBytes(base); err == nil {
		c.Free, c.FreeKnown = n, true
	}
	c.Writable = writable(base)
	if st, err := os.Stat(filepath.Join(abs, ExeName())); err == nil && !st.IsDir() {
		c.HasApp = true
	}
	for _, n := range KnownDataFiles() {
		if _, err := os.Stat(filepath.Join(abs, "data", n)); err == nil {
			c.Existing = append(c.Existing, n)
		}
	}
	return c, nil
}
