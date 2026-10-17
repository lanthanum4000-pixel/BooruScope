package localweb

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
)

const TokenHeader = "X-BooruScope-Token"

type Guard struct {
	token string
	mu    sync.RWMutex
	port  string
}

func NewGuard() *Guard {
	b := make([]byte, 24)
	rand.Read(b)
	return &Guard{token: hex.EncodeToString(b)}
}

func (g *Guard) Token() string { return g.token }

func (g *Guard) SetAddr(addr string) {
	_, port, _ := net.SplitHostPort(addr)
	g.mu.Lock()
	g.port = port
	g.mu.Unlock()
}

func (g *Guard) hostAllowed(host string) bool {
	h, port, err := net.SplitHostPort(host)
	if err != nil || (h != "127.0.0.1" && h != "localhost") {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.port == "" || port == g.port
}

func (g *Guard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.hostAllowed(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			tok := r.Header.Get(TokenHeader)
			if tok == "" {
				tok = r.URL.Query().Get("t")
			}
			if tok != g.token {
				WriteErr(w, http.StatusForbidden, "forbidden", "")
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) ServePage(w http.ResponseWriter, web fs.FS, name string) {
	b, err := fs.ReadFile(web, name)
	if err != nil {
		http.Error(w, "missing "+name, http.StatusInternalServerError)
		return
	}
	b = bytes.Replace(b, []byte("{{TOKEN}}"), []byte(g.token), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; style-src 'self'; script-src 'self'; connect-src 'self'")
	w.Write(b)
}

type APIError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func WriteErr(w http.ResponseWriter, code int, kind, detail string) {
	WriteJSON(w, code, APIError{Error: kind, Detail: detail})
}
