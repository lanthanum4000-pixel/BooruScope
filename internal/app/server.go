package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/encoder"
	"github.com/lanthanum4000-pixel/BooruScope/internal/fetch"
	"github.com/lanthanum4000-pixel/BooruScope/internal/index"
)

const (
	ResultCount  = 60
	queryKeep    = 8
	PostURLTpl   = "https://gelbooru.com/index.php?page=post&s=view&id="
	tokenHeader  = "X-BooruScope-Token"
	encodeLimit  = 3 * time.Minute
	fetchTimeout = 45 * time.Second
)

type Encoder interface {
	Status() encoder.Status
	Encode(ctx context.Context, img []byte) ([]float32, error)
	Restart()
}

type Config struct {
	Library      *Library
	Encoder      Encoder
	Thumbs       *fetch.Thumbs
	Web          fs.FS
	SettingsPath string
	OpenURL      func(string) error
	FetchImage   func(ctx context.Context, u *url.URL, referer string) ([]byte, string, error)
	Logf         func(string, ...any)
}

type Server struct {
	cfg   Config
	token string
	addr  string

	mu       sync.Mutex
	settings Settings
	queries  []storedQuery
	qseq     int
}

type storedQuery struct {
	id   string
	data []byte
	ct   string
}

type apiError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

func New(cfg Config) *Server {
	if cfg.FetchImage == nil {
		cfg.FetchImage = fetch.Image
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	b := make([]byte, 24)
	rand.Read(b)
	return &Server{cfg: cfg, token: hex.EncodeToString(b), settings: loadSettings(cfg.SettingsPath)}
}

func (s *Server) Token() string { return s.token }

func (s *Server) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *Server) SetAddr(addr string) { s.addr = addr }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(s.cfg.Web)))
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("PUT /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/search/image", s.handleSearchImage)
	mux.HandleFunc("POST /api/search/url", s.handleSearchURL)
	mux.HandleFunc("GET /api/query/{id}", s.handleQueryImage)
	mux.HandleFunc("GET /api/thumb/{row}", s.handleThumb)
	mux.HandleFunc("POST /api/open", s.handleOpen)
	mux.HandleFunc("POST /api/encoder/restart", s.handleRestart)
	return s.guard(mux)
}

func (s *Server) hostAllowed(host string) bool {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	if h != "127.0.0.1" && h != "localhost" {
		return false
	}
	if s.addr == "" {
		return true
	}
	_, want, _ := net.SplitHostPort(s.addr)
	return port == want
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			tok := r.Header.Get(tokenHeader)
			if tok == "" {
				tok = r.URL.Query().Get("t")
			}
			if tok != s.token {
				writeErr(w, http.StatusForbidden, "forbidden", "")
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, kind, detail string) {
	writeJSON(w, code, apiError{Error: kind, Detail: detail})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.cfg.Web, "index.html")
	if err != nil {
		http.Error(w, "missing index.html", http.StatusInternalServerError)
		return
	}
	b = bytes.Replace(b, []byte("{{TOKEN}}"), []byte(s.token), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; style-src 'self'; script-src 'self'; connect-src 'self'")
	w.Write(b)
}

type stateResp struct {
	Encoder  encoder.Status `json:"encoder"`
	Library  LibraryStatus  `json:"library"`
	Settings Settings       `json:"settings"`
	Results  int            `json:"resultCount"`
	Factor   int            `json:"rerankFactor"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stateResp{
		Encoder:  s.cfg.Encoder.Status(),
		Library:  s.cfg.Library.Status(),
		Settings: s.Settings(),
		Results:  ResultCount,
		Factor:   index.RerankFactor,
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in = in.normalized()
	s.mu.Lock()
	prev := s.settings
	s.settings = in
	s.mu.Unlock()
	if err := saveSettings(s.cfg.SettingsPath, in); err != nil {
		s.cfg.Logf("settings: %v", err)
	}
	if in.Variant != prev.Variant {
		s.cfg.Library.Load(in.Variant)
	}
	writeJSON(w, http.StatusOK, in)
}

type resultItem struct {
	Row    int     `json:"row"`
	PostID uint32  `json:"id"`
	Score  float32 `json:"score"`
}

type searchResp struct {
	Query    string       `json:"query"`
	Variant  int          `json:"variant"`
	Reranked bool         `json:"reranked"`
	EncodeMs int64        `json:"encodeMs"`
	SearchMs int64        `json:"searchMs"`
	Results  []resultItem `json:"results"`
}

func (s *Server) storeQuery(data []byte, ct string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qseq++
	id := "q" + strconv.Itoa(s.qseq)
	s.queries = append(s.queries, storedQuery{id: id, data: data, ct: ct})
	if len(s.queries) > queryKeep {
		s.queries = s.queries[len(s.queries)-queryKeep:]
	}
	return id
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, img []byte, ct string) {
	eng := s.cfg.Library.Engine()
	if eng == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_index", string(s.cfg.Library.Status().State))
		return
	}
	if st := s.cfg.Encoder.Status(); st.State != encoder.StateReady {
		writeErr(w, http.StatusServiceUnavailable, "encoder_not_ready", string(st.State))
		return
	}
	qid := s.storeQuery(img, ct)
	ctx, cancel := context.WithTimeout(r.Context(), encodeLimit)
	defer cancel()
	t0 := time.Now()
	vec, err := s.cfg.Encoder.Encode(ctx, img)
	if err != nil {
		switch {
		case errors.Is(err, encoder.ErrInput):
			writeErr(w, http.StatusUnprocessableEntity, "bad_image", err.Error())
		case errors.Is(err, encoder.ErrNotReady):
			writeErr(w, http.StatusServiceUnavailable, "encoder_not_ready", "")
		default:
			writeErr(w, http.StatusInternalServerError, "encode_failed", err.Error())
		}
		return
	}
	t1 := time.Now()
	rerank := s.Settings().Rerank && eng.Int8 != nil
	hits, err := eng.Search(vec, ResultCount, rerank)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	out := searchResp{Query: qid, Variant: eng.PQ.M, Reranked: rerank, EncodeMs: t1.Sub(t0).Milliseconds(), SearchMs: time.Since(t1).Milliseconds(), Results: make([]resultItem, len(hits))}
	for i, h := range hits {
		out.Results[i] = resultItem{Row: h.Row, PostID: eng.Posts.IDs[h.Row], Score: h.Score}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSearchImage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, fetch.MaxImageSize+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(body) > fetch.MaxImageSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "")
		return
	}
	ct, ok := fetch.Sniff(body)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "not_image", ct)
		return
	}
	s.search(w, r, body, ct)
}

func refererFor(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if host == "i.pximg.net" || strings.HasSuffix(host, ".pximg.net") {
		return "https://www.pixiv.net/"
	}
	return fetch.OriginOf(u) + "/"
}

func (s *Server) handleSearchURL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	u, err := fetch.ParseHTTP(in.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_url", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fetchTimeout)
	defer cancel()
	data, ct, err := s.cfg.FetchImage(ctx, u, refererFor(u))
	if err != nil {
		var se *fetch.StatusError
		if errors.Is(err, fetch.ErrNotImage) || errors.As(err, &se) {
			data, ct, err = s.cfg.FetchImage(ctx, u, "")
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, fetch.ErrNotImage):
			writeErr(w, http.StatusBadGateway, "not_image", "")
		case errors.Is(err, fetch.ErrTooLarge):
			writeErr(w, http.StatusBadGateway, "too_large", "")
		default:
			writeErr(w, http.StatusBadGateway, "fetch_failed", err.Error())
		}
		return
	}
	s.search(w, r, data, ct)
}

func (s *Server) handleQueryImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	var q *storedQuery
	for i := range s.queries {
		if s.queries[i].id == id {
			q = &s.queries[i]
		}
	}
	s.mu.Unlock()
	if q == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", q.ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(q.data)
}

func (s *Server) rowParam(r *http.Request, eng *index.Engine, raw string) (int, bool) {
	row, err := strconv.Atoi(raw)
	if err != nil || row < 0 || row >= eng.Posts.N {
		return 0, false
	}
	return row, true
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	eng := s.cfg.Library.Engine()
	if eng == nil {
		http.Error(w, "no index", http.StatusServiceUnavailable)
		return
	}
	row, ok := s.rowParam(r, eng, r.PathValue("row"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	t, err := s.cfg.Thumbs.Get(r.Context(), fetch.ThumbnailURL(eng.Posts.MD5Hex(row)))
	if err != nil {
		if fetch.IsGone(err) {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		http.Error(w, "unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", t.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=600")
	w.Write(t.Data)
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Row int `json:"row"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	eng := s.cfg.Library.Engine()
	if eng == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_index", "")
		return
	}
	row, ok := s.rowParam(r, eng, strconv.Itoa(in.Row))
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_row", "")
		return
	}
	u := PostURLTpl + strconv.FormatUint(uint64(eng.Posts.IDs[row]), 10)
	if err := s.cfg.OpenURL(u); err != nil {
		writeErr(w, http.StatusInternalServerError, "open_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	s.cfg.Encoder.Restart()
	writeJSON(w, http.StatusOK, s.cfg.Encoder.Status())
}
