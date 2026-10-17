package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrNotInstall  = errors.New("no BooruScope installation in this folder")
	ErrUninstallMe = errors.New("this installer is running from that folder")
)

type UninstallResult struct {
	Dir         string   `json:"dir"`
	Removed     int      `json:"removed"`
	Kept        []string `json:"kept,omitempty"`
	DirRemoved  bool     `json:"dirRemoved"`
	Shortcuts   bool     `json:"shortcuts"`
	ShortcutErr string   `json:"shortcutError,omitempty"`
}

var runtimeEntries = []string{"venv", "encoder", "huggingface", "browser-profile", "booruscope.log", "requirements.txt", "window.json"}

func Uninstall(dir, selfExe string, removeShortcuts func(exe string) error) (UninstallResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return UninstallResult{}, err
	}
	res := UninstallResult{Dir: abs}
	exe := filepath.Join(abs, ExeName())
	if st, err := os.Stat(exe); err != nil || st.IsDir() {
		return res, ErrNotInstall
	}
	if selfExe != "" && sameFile(selfExe, exe) {
		return res, ErrUninstallMe
	}
	if err := os.Remove(exe); err != nil {
		return res, &InstallError{Code: "app_running", Err: err}
	}
	res.Removed++

	var failed []string
	remove := func(path string, all bool) {
		if _, err := os.Lstat(path); err != nil {
			return
		}
		var err error
		if all {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			failed = append(failed, path)
			return
		}
		res.Removed++
	}

	remove(exe+".new", false)
	remove(filepath.Join(abs, "settings.json"), false)
	remove(filepath.Join(abs, "settings.json.tmp"), false)

	dataDir := filepath.Join(abs, "data")
	for _, n := range KnownDataFiles() {
		remove(filepath.Join(dataDir, n), false)
		remove(filepath.Join(dataDir, n+".part"), false)
	}
	remove(filepath.Join(dataDir, verifiedName), false)
	removeIfEmpty(dataDir)

	runtimeDir := filepath.Join(abs, "runtime")
	for _, n := range runtimeEntries {
		remove(filepath.Join(runtimeDir, n), true)
	}
	removeIfEmpty(runtimeDir)

	if removeShortcuts != nil {
		if err := removeShortcuts(exe); err != nil {
			res.ShortcutErr = err.Error()
		} else {
			res.Shortcuts = true
		}
	}

	res.Kept = leftovers(abs)
	if len(res.Kept) == 0 {
		res.DirRemoved = os.Remove(abs) == nil
	}
	if len(failed) > 0 {
		return res, &InstallError{Code: "remove_failed", Err: fmt.Errorf("could not remove %s", strings.Join(failed, ", "))}
	}
	return res, nil
}

func removeIfEmpty(dir string) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
}

func leftovers(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
