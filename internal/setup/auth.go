package setup

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func HFToken() string {
	if t := strings.TrimSpace(os.Getenv("HF_TOKEN")); t != "" {
		return t
	}
	var paths []string
	if h := os.Getenv("HF_HOME"); h != "" {
		paths = append(paths, filepath.Join(h, "token"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".cache", "huggingface", "token"))
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			if t := strings.TrimSpace(string(b)); t != "" {
				return t
			}
		}
	}
	return ""
}

func isHFHost(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return u.Scheme == "https" && (h == "huggingface.co" || strings.HasSuffix(h, ".huggingface.co"))
}

func authorize(req *http.Request, token string) {
	if token != "" && isHFHost(req.URL) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func IsAuthError(err error) bool {
	code, ok := statusCode(err)
	return ok && (code == http.StatusUnauthorized || code == http.StatusForbidden || code == http.StatusNotFound)
}
