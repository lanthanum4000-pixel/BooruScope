package setup

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lanthanum4000-pixel/BooruScope/internal/fetch"
)

func TestAuthorizeOnlyHuggingFace(t *testing.T) {
	cases := map[string]bool{
		"https://huggingface.co/Lanthanum4000/BooruScope/resolve/main/manifest.json": true,
		"https://cdn-lfs.huggingface.co/x":                                           true,
		"http://huggingface.co/x":                                                    false,
		"https://huggingface.co.evil.test/x":                                         false,
		"https://cas-bridge.xethub.hf.co/x":                                          false,
		"https://assets.yande.re/data/preview":                                       false,
	}
	for u, want := range cases {
		req, _ := http.NewRequest("GET", u, nil)
		authorize(req, "tok")
		if got := req.Header.Get("Authorization") == "Bearer tok"; got != want {
			t.Errorf("%s: authorized=%v, want %v", u, got, want)
		}
	}
	req, _ := http.NewRequest("GET", "https://huggingface.co/x", nil)
	authorize(req, "")
	if req.Header.Get("Authorization") != "" {
		t.Error("empty token sent")
	}
}

func TestHFTokenSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HF_HOME", "")
	t.Setenv("HF_TOKEN", "")
	if tok := HFToken(); tok != "" {
		t.Fatalf("unexpected token %q", tok)
	}
	os.MkdirAll(filepath.Join(home, ".cache", "huggingface"), 0o755)
	os.WriteFile(filepath.Join(home, ".cache", "huggingface", "token"), []byte("hf_file\n"), 0o600)
	if tok := HFToken(); tok != "hf_file" {
		t.Fatalf("file token %q", tok)
	}
	hfHome := t.TempDir()
	os.WriteFile(filepath.Join(hfHome, "token"), []byte("hf_home"), 0o600)
	t.Setenv("HF_HOME", hfHome)
	if tok := HFToken(); tok != "hf_home" {
		t.Fatalf("HF_HOME token %q", tok)
	}
	t.Setenv("HF_TOKEN", "hf_env")
	if tok := HFToken(); tok != "hf_env" {
		t.Fatalf("env token %q", tok)
	}
}

func TestAuthErrorDetection(t *testing.T) {
	for code, want := range map[int]bool{401: true, 403: true, 404: true, 500: false} {
		if got := IsAuthError(&fetch.StatusError{Code: code}); got != want {
			t.Errorf("%d: %v", code, got)
		}
	}
}

func TestEncodeCommand(t *testing.T) {
	got := encodeCommand("ä'選")
	if got != "5AAnAHiQ" {
		t.Fatalf("%s", got)
	}
	if q := psQuote(`C:\it's 資料`); q != `'C:\it''s 資料'` {
		t.Fatalf("%s", q)
	}
}
