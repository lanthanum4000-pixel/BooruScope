package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Kind string

const (
	Chrome Kind = "chrome"
	Edge   Kind = "edge"
)

type Found struct {
	Kind Kind
	Path string
}

func candidates() []Found {
	var out []Found
	add := func(k Kind, paths ...string) {
		for _, p := range paths {
			if p != "" {
				out = append(out, Found{Kind: k, Path: p})
			}
		}
	}
	switch runtime.GOOS {
	case "windows":
		pf, pf86, local := os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")
		join := func(base string, parts ...string) string {
			if base == "" {
				return ""
			}
			return filepath.Join(append([]string{base}, parts...)...)
		}
		add(Chrome,
			join(pf, "Google", "Chrome", "Application", "chrome.exe"),
			join(pf86, "Google", "Chrome", "Application", "chrome.exe"),
			join(local, "Google", "Chrome", "Application", "chrome.exe"))
		add(Edge,
			join(pf86, "Microsoft", "Edge", "Application", "msedge.exe"),
			join(pf, "Microsoft", "Edge", "Application", "msedge.exe"))
	case "darwin":
		add(Chrome, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
		add(Edge, "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge")
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if p, err := exec.LookPath(n); err == nil {
				add(Chrome, p)
			}
		}
		for _, n := range []string{"microsoft-edge", "microsoft-edge-stable"} {
			if p, err := exec.LookPath(n); err == nil {
				add(Edge, p)
			}
		}
	}
	return out
}

func Find(override string) (Found, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return Found{}, err
		}
		return Found{Kind: Chrome, Path: override}, nil
	}
	for _, c := range candidates() {
		if st, err := os.Stat(c.Path); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return Found{}, errors.New("browser: neither Chrome nor Edge was found")
}

func LaunchApp(b Found, appURL, profileDir string) (*exec.Cmd, error) {
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.Command(b.Path,
		"--app="+appURL,
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate,TranslateUI",
		"--disable-translate",
		"--window-size=1280,880",
	)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func OpenDefault(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
