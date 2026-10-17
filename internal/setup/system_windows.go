//go:build windows

package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lanthanum4000-pixel/BooruScope/internal/proc"
)

var procFreeSpace = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

func freeBytes(path string) (int64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	r, _, e := procFreeSpace.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if r == 0 {
		return 0, e
	}
	return int64(avail), nil
}

func DefaultDir() string {
	if l := os.Getenv("LOCALAPPDATA"); l != "" {
		return filepath.Join(l, "Programs", "BooruScope")
	}
	return `C:\BooruScope`
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func powershell(script string) ([]byte, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-ExecutionPolicy", "Bypass", "-Command", "[Console]::OutputEncoding=[Text.Encoding]::UTF8; "+script)
	return proc.Hide(cmd).Output()
}

func PickFolder(initial, prompt string) (string, error) {
	script := "Add-Type -AssemblyName System.Windows.Forms; " +
		"$o = New-Object System.Windows.Forms.Form -Property @{TopMost=$true; ShowInTaskbar=$false}; " +
		"$d = New-Object System.Windows.Forms.FolderBrowserDialog; " +
		"$d.Description = " + psQuote(prompt) + "; $d.ShowNewFolderButton = $true; " +
		"$d.SelectedPath = " + psQuote(initial) + "; " +
		"if ($d.ShowDialog($o) -eq [System.Windows.Forms.DialogResult]::OK) { $d.SelectedPath } else { exit 2 }"
	out, err := powershell(script)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			return "", ErrPickCancelled
		}
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(string(out), "\ufeff")), nil
}

func CreateShortcuts(exe, dir string) error {
	script := "$w = New-Object -ComObject WScript.Shell; " +
		"foreach ($f in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) { " +
		"$s = $w.CreateShortcut((Join-Path $f 'BooruScope.lnk')); " +
		"$s.TargetPath = " + psQuote(exe) + "; $s.WorkingDirectory = " + psQuote(dir) + "; " +
		"$s.IconLocation = " + psQuote(exe+",0") + "; $s.Save() }"
	_, err := powershell(script)
	return err
}

func ShortcutsSupported() bool { return true }
