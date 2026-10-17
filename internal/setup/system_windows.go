//go:build windows

package setup

import (
	"fmt"
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

func powershell(script string) ([]byte, error) {
	full := "[Console]::OutputEncoding = [Text.Encoding]::UTF8\n" + script
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodeCommand(full))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := proc.Hide(cmd).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			return nil, err
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

func PickFolder(initial, prompt string) (string, error) {
	script := "Add-Type -AssemblyName System.Windows.Forms\n" +
		"[System.Windows.Forms.Application]::EnableVisualStyles()\n" +
		"$o = New-Object System.Windows.Forms.Form\n" +
		"$o.TopMost = $true; $o.ShowInTaskbar = $false; $o.FormBorderStyle = 'None'; $o.Opacity = 0; $o.StartPosition = 'CenterScreen'\n" +
		"$o.Show(); $o.Activate()\n" +
		"$d = New-Object System.Windows.Forms.FolderBrowserDialog\n" +
		"$d.Description = " + psQuote(prompt) + "; $d.ShowNewFolderButton = $true\n" +
		"$d.SelectedPath = " + psQuote(initial) + "\n" +
		"$r = $d.ShowDialog($o)\n" +
		"$o.Close()\n" +
		"if ($r -eq [System.Windows.Forms.DialogResult]::OK) { $d.SelectedPath } else { exit 2 }"
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
	script := "$w = New-Object -ComObject WScript.Shell\n" +
		"foreach ($f in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) { " +
		"$s = $w.CreateShortcut((Join-Path $f 'BooruScope.lnk')); " +
		"$s.TargetPath = " + psQuote(exe) + "; $s.WorkingDirectory = " + psQuote(dir) + "; " +
		"$s.IconLocation = " + psQuote(exe+",0") + "; $s.Save() }"
	_, err := powershell(script)
	return err
}

func ShortcutsSupported() bool { return true }
