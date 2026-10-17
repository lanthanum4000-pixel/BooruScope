package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/encoder"
	"github.com/lanthanum4000-pixel/BooruScope/internal/fetch"
	"github.com/lanthanum4000-pixel/BooruScope/internal/localweb"
)

var packDir = filepath.Join("..", "..", "testdata", "testpack")

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

type fakeEncoder struct {
	mu       sync.Mutex
	vec      []float32
	calls    int
	state    encoder.State
	starts   int
	unloads  int
	env      []string
	loadTime time.Duration
}

func (f *fakeEncoder) Status() encoder.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return encoder.Status{State: f.state}
}
func (f *fakeEncoder) set(st encoder.State) { f.mu.Lock(); f.state = st; f.mu.Unlock() }
func (f *fakeEncoder) Restart()             {}
func (f *fakeEncoder) SetExtraEnv(env []string) {
	f.mu.Lock()
	f.env = env
	f.mu.Unlock()
}
func (f *fakeEncoder) Unload() {
	f.mu.Lock()
	f.unloads++
	f.state = encoder.StateUnloaded
	f.mu.Unlock()
}
func (f *fakeEncoder) Start() {
	f.mu.Lock()
	f.starts++
	f.state = encoder.StateLoading
	d := f.loadTime
	f.mu.Unlock()
	go func() {
		time.Sleep(d)
		f.set(encoder.StateReady)
	}()
}
func (f *fakeEncoder) Encode(ctx context.Context, img []byte) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.vec, nil
}

type expected struct {
	Queries []struct {
		Vec  string `json:"query_vec_f32_le_b64"`
		Int8 []struct {
			Row    int    `json:"row"`
			PostID uint32 `json:"post_id"`
		} `json:"int8"`
		M128 []struct {
			Row int `json:"row"`
		} `json:"none_m128"`
	} `json:"queries"`
}

func loadExpected(t *testing.T) expected {
	b, err := os.ReadFile(filepath.Join(packDir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e expected
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func vecOf(t *testing.T, s string) []float32 {
	b, _ := base64.StdEncoding.DecodeString(s)
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

type harness struct {
	srv      *Server
	h        http.Handler
	enc      *fakeEncoder
	opened   []string
	mu       sync.Mutex
	fetches  []string
	referers []string
	fetchErr map[string]error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	exp := loadExpected(t)
	hs := &harness{enc: &fakeEncoder{vec: vecOf(t, exp.Queries[0].Vec), state: encoder.StateReady}, fetchErr: map[string]error{}}
	fakeFetch := func(ctx context.Context, u *url.URL, ref string) ([]byte, string, error) {
		hs.mu.Lock()
		defer hs.mu.Unlock()
		hs.fetches = append(hs.fetches, u.String())
		hs.referers = append(hs.referers, ref)
		if err, ok := hs.fetchErr[ref]; ok {
			return nil, "", err
		}
		return pngBytes, "image/png", nil
	}
	lib := NewLibrary(packDir, t.Logf)
	lib.Load(128)
	deadline := time.Now().Add(10 * time.Second)
	for lib.Status().State != LibReady {
		if time.Now().After(deadline) {
			t.Fatalf("library state %+v", lib.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(lib.Close)
	hs.srv = New(Config{
		Library:      lib,
		Encoder:      hs.enc,
		Thumbs:       fetch.NewThumbs(1<<20, 2, fakeFetch),
		Web:          fstest.MapFS{"index.html": {Data: []byte("<meta content=\"{{TOKEN}}\">")}},
		SettingsPath: filepath.Join(t.TempDir(), "settings.json"),
		OpenURL: func(u string) error {
			hs.opened = append(hs.opened, u)
			return nil
		},
		FetchImage: fakeFetch,
		Logf:       t.Logf,
	})
	hs.srv.SetAddr("127.0.0.1:5555")
	hs.h = hs.srv.Handler()
	return hs
}

func (hs *harness) do(method, path string, body io.Reader, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Host = "127.0.0.1:5555"
	if auth {
		req.Header.Set(localweb.TokenHeader, hs.srv.Token())
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func TestRejectsMissingTokenAndForeignHost(t *testing.T) {
	hs := newHarness(t)
	if rec := hs.do("GET", "/api/state", nil, false); rec.Code != http.StatusForbidden {
		t.Fatalf("no token: %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Host = "evil.example:5555"
	req.Header.Set(localweb.TokenHeader, hs.srv.Token())
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host: %d", rec.Code)
	}
	if rec := hs.do("GET", "/api/state", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("with token: %d", rec.Code)
	}
}

func TestIndexInjectsToken(t *testing.T) {
	hs := newHarness(t)
	rec := hs.do("GET", "/", nil, false)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), hs.srv.Token()) {
		t.Fatalf("index: %d %q", rec.Code, rec.Body.String())
	}
}

func decodeSearch(t *testing.T, rec *httptest.ResponseRecorder) searchResp {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var out searchResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSearchImageReranked(t *testing.T) {
	hs := newHarness(t)
	exp := loadExpected(t)
	out := decodeSearch(t, hs.do("POST", "/api/search/image", bytes.NewReader(pngBytes), true))
	if !out.Reranked || out.Variant != 128 || len(out.Results) != ResultCount {
		t.Fatalf("resp %+v", out)
	}
	for i, h := range exp.Queries[0].Int8 {
		if out.Results[i].Row != h.Row || out.Results[i].PostID != h.PostID {
			t.Fatalf("rank %d: got row %d, want %d", i, out.Results[i].Row, h.Row)
		}
	}
	rec := hs.do("GET", "/api/query/"+out.Query, nil, true)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), pngBytes) {
		t.Fatalf("query image: %d", rec.Code)
	}
}

func TestSearchWithoutRerank(t *testing.T) {
	hs := newHarness(t)
	exp := loadExpected(t)
	rec := hs.do("PUT", "/api/settings", strings.NewReader(`{"rowCap":5,"variant":128,"rerank":false,"lang":"zh","idleUnload":true,"idleMinutes":3,"offline":false}`), true)
	if rec.Code != 200 {
		t.Fatalf("settings: %d", rec.Code)
	}
	out := decodeSearch(t, hs.do("POST", "/api/search/image", bytes.NewReader(pngBytes), true))
	if out.Reranked {
		t.Fatal("expected coarse only")
	}
	for i, h := range exp.Queries[0].M128 {
		if out.Results[i].Row != h.Row {
			t.Fatalf("rank %d: got row %d, want %d", i, out.Results[i].Row, h.Row)
		}
	}
	saved := loadSettings(hs.srv.cfg.SettingsPath)
	if saved.RowCap != 5 || saved.Lang != "zh" || saved.Rerank || !saved.IdleUnload || saved.IdleMinutes != 3 || saved.Offline {
		t.Fatalf("saved %+v", saved)
	}
}

func TestSearchRejectsNonImage(t *testing.T) {
	hs := newHarness(t)
	rec := hs.do("POST", "/api/search/image", strings.NewReader("<html>nope</html>"), true)
	if rec.Code != http.StatusUnprocessableEntity || hs.enc.Status().State != encoder.StateReady || hs.enc.calls != 0 {
		t.Fatalf("got %d, calls %d", rec.Code, hs.enc.calls)
	}
}

func TestSearchURLUsesOriginRefererThenFallsBack(t *testing.T) {
	hs := newHarness(t)
	decodeSearch(t, hs.do("POST", "/api/search/url", strings.NewReader(`{"url":"https://img.example.org/a/b.jpg"}`), true))
	if hs.referers[0] != "https://img.example.org/" {
		t.Fatalf("referer %q", hs.referers[0])
	}
	hs.fetchErr["https://cdn.example.net/"] = &fetch.StatusError{Code: 403}
	decodeSearch(t, hs.do("POST", "/api/search/url", strings.NewReader(`{"url":"https://cdn.example.net/x.png"}`), true))
	if last := hs.referers[len(hs.referers)-1]; last != "" {
		t.Fatalf("fallback referer %q", last)
	}
	if rec := hs.do("POST", "/api/search/url", strings.NewReader(`{"url":"file:///etc/passwd"}`), true); rec.Code != http.StatusBadRequest {
		t.Fatalf("file url: %d", rec.Code)
	}
}

func TestThumbProxyUsesMD5URLWithoutReferer(t *testing.T) {
	hs := newHarness(t)
	eng := hs.srv.cfg.Library.Engine()
	md5 := eng.Posts.MD5Hex(7)
	rec := hs.do("GET", "/api/thumb/7?t="+hs.srv.Token(), nil, false)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("thumb: %d", rec.Code)
	}
	want := "https://assets.yande.re/data/preview/" + md5[:2] + "/" + md5[2:4] + "/" + md5 + ".jpg"
	if hs.fetches[0] != want || hs.referers[0] != "" {
		t.Fatalf("fetched %q with referer %q", hs.fetches[0], hs.referers[0])
	}
	hs.do("GET", "/api/thumb/7?t="+hs.srv.Token(), nil, false)
	if len(hs.fetches) != 1 {
		t.Fatalf("cache miss: %d fetches", len(hs.fetches))
	}
	if rec := hs.do("GET", "/api/thumb/100000?t="+hs.srv.Token(), nil, false); rec.Code != http.StatusNotFound {
		t.Fatalf("out of range: %d", rec.Code)
	}
}

func TestOpenBuildsPostURL(t *testing.T) {
	hs := newHarness(t)
	eng := hs.srv.cfg.Library.Engine()
	rec := hs.do("POST", "/api/open", strings.NewReader(`{"row":3}`), true)
	if rec.Code != 200 || len(hs.opened) != 1 {
		t.Fatalf("open: %d %v", rec.Code, hs.opened)
	}
	want := "https://yande.re/post/show/" + jsonNum(eng.Posts.IDs[3])
	if hs.opened[0] != want {
		t.Fatalf("opened %q, want %q", hs.opened[0], want)
	}
	if rec := hs.do("POST", "/api/open", strings.NewReader(`{"row":-1}`), true); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad row: %d", rec.Code)
	}
}

func jsonNum(v uint32) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestEncoderNotReady(t *testing.T) {
	hs := newHarness(t)
	hs.enc.set(encoder.StateError)
	rec := hs.do("POST", "/api/search/image", bytes.NewReader(pngBytes), true)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "encoder_not_ready") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestLibraryMissing(t *testing.T) {
	lib := NewLibrary(t.TempDir(), t.Logf)
	lib.Load(0)
	if st := lib.Status(); st.State != LibMissing {
		t.Fatalf("state %s", st.State)
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestIdleUnloadCountsDownOnlyWhileIdle(t *testing.T) {
	hs := newHarness(t)
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	hs.srv.cfg.Now = clk.now
	hs.do("PUT", "/api/settings", strings.NewReader(`{"rowCap":4,"idleUnload":true,"idleMinutes":2,"rerank":true}`), true)
	hs.srv.idleTick()
	if at := hs.srv.idleStatus().UnloadAt; at != clk.now().Add(2*time.Minute).UnixMilli() {
		t.Fatalf("unloadAt %d", at)
	}
	clk.add(119 * time.Second)
	hs.srv.idleTick()
	if hs.enc.unloads != 0 {
		t.Fatal("unloaded early")
	}
	hs.srv.beginWork()
	clk.add(5 * time.Minute)
	hs.srv.idleTick()
	if hs.enc.unloads != 0 || hs.srv.idleStatus().UnloadAt != 0 {
		t.Fatal("countdown ran during a search")
	}
	hs.srv.endWork()
	clk.add(119 * time.Second)
	hs.srv.idleTick()
	if hs.enc.unloads != 0 {
		t.Fatal("countdown did not restart after the search")
	}
	clk.add(time.Second)
	hs.srv.idleTick()
	if hs.enc.unloads != 1 || hs.enc.Status().State != encoder.StateUnloaded {
		t.Fatalf("not unloaded: %d %s", hs.enc.unloads, hs.enc.Status().State)
	}
	if hs.srv.idleStatus().UnloadAt != 0 {
		t.Fatal("countdown shown while unloaded")
	}
}

func TestIdleUnloadDisabled(t *testing.T) {
	hs := newHarness(t)
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	hs.srv.cfg.Now = clk.now
	clk.add(48 * time.Hour)
	hs.srv.idleTick()
	if hs.enc.unloads != 0 || hs.srv.idleStatus().UnloadAt != 0 {
		t.Fatal("unloaded while disabled")
	}
}

func TestSearchReloadsUnloadedEncoder(t *testing.T) {
	hs := newHarness(t)
	hs.enc.loadTime = 200 * time.Millisecond
	hs.enc.Unload()
	out := decodeSearch(t, hs.do("POST", "/api/search/image", bytes.NewReader(pngBytes), true))
	if len(out.Results) != ResultCount || hs.enc.starts != 1 {
		t.Fatalf("starts %d, results %d", hs.enc.starts, len(out.Results))
	}
}

func TestOfflineEnvOnlyWhenModelCached(t *testing.T) {
	hs := newHarness(t)
	hf := t.TempDir()
	hs.srv.cfg.HFHome = hf
	hs.srv.StartEncoder()
	if len(hs.enc.env) != 0 {
		t.Fatalf("offline env without a cached model: %v", hs.enc.env)
	}
	snap := filepath.Join(hf, "hub", "models--openai--clip-vit-large-patch14", "snapshots", "abc")
	os.MkdirAll(snap, 0o755)
	os.WriteFile(filepath.Join(snap, "config.json"), []byte("{}"), 0o644)
	hs.srv.StartEncoder()
	if strings.Join(hs.enc.env, " ") != "HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1" {
		t.Fatalf("env %v", hs.enc.env)
	}
	hs.do("PUT", "/api/settings", strings.NewReader(`{"rowCap":4,"offline":false,"rerank":true}`), true)
	hs.srv.StartEncoder()
	if len(hs.enc.env) != 0 {
		t.Fatalf("offline env while disabled: %v", hs.enc.env)
	}
}

func TestSettingsAcceptJapanese(t *testing.T) {
	for in, want := range map[string]string{"ja": "ja", "zh": "zh", "en": "en", "fr": ""} {
		if got := (Settings{Lang: in}).normalized().Lang; got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}
