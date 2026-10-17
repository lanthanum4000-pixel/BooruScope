//go:build !windows

package setup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "BooruScope"
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Applications", "BooruScope")
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "BooruScope")
	}
	return filepath.Join(home, ".local", "share", "BooruScope")
}

func PickFolder(initial, prompt string) (string, error) {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "`+strings.ReplaceAll(prompt, `"`, `'`)+`")`)
	case lookPath("zenity"):
		cmd = exec.Command("zenity", "--file-selection", "--directory", "--title="+prompt, "--filename="+initial+"/")
	case lookPath("kdialog"):
		cmd = exec.Command("kdialog", "--getexistingdirectory", initial, "--title", prompt)
	default:
		return "", ErrNoPicker
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", ErrPickCancelled
		}
		return "", err
	}
	return strings.TrimRight(strings.TrimSpace(string(out)), "/"), nil
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func CreateShortcuts(exe, dir string) error { return nil }

func RemoveShortcuts(exe string) error { return nil }

func ShortcutsSupported() bool { return false }
