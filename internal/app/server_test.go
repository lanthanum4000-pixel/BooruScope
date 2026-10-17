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
)

var packDir = filepath.Join("..", "..", "testdata", "testpack")

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

type fakeEncoder struct {
	vec   []float32
	calls int
	state encoder.State
}

func (f *fakeEncoder) Status() encoder.Status { return encoder.Status{State: f.state} }
func (f *fakeEncoder) Restart()               {}
func (f *fakeEncoder) Encode(ctx context.Context, img []byte) ([]float32, error) {
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
		req.Header.Set(tokenHeader, hs.srv.Token())
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
	req.Header.Set(tokenHeader, hs.srv.Token())
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
	rec := hs.do("PUT", "/api/settings", strings.NewReader(`{"rowCap":5,"statusMode":"emoji","variant":128,"rerank":false,"lang":"zh"}`), true)
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
	if saved.RowCap != 5 || saved.StatusMode != "emoji" || saved.Lang != "zh" || saved.Rerank {
		t.Fatalf("saved %+v", saved)
	}
}

func TestSearchRejectsNonImage(t *testing.T) {
	hs := newHarness(t)
	rec := hs.do("POST", "/api/search/image", strings.NewReader("<html>nope</html>"), true)
	if rec.Code != http.StatusUnprocessableEntity || hs.enc.calls != 0 {
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

func TestThumbProxyUsesMD5URLAndHostReferer(t *testing.T) {
	hs := newHarness(t)
	eng := hs.srv.cfg.Library.Engine()
	md5 := eng.Posts.MD5Hex(7)
	rec := hs.do("GET", "/api/thumb/7?t="+hs.srv.Token(), nil, false)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("thumb: %d", rec.Code)
	}
	want := "https://img4.gelbooru.com/thumbnails/" + md5[:2] + "/" + md5[2:4] + "/thumbnail_" + md5 + ".jpg"
	if hs.fetches[0] != want || hs.referers[0] != "https://img4.gelbooru.com" {
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
	want := "https://gelbooru.com/index.php?page=post&s=view&id=" + jsonNum(eng.Posts.IDs[3])
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
	hs.enc.state = encoder.StateInstall
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
