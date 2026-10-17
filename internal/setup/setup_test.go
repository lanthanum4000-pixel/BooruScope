package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/localweb"
)

type origin struct {
	srv      *httptest.Server
	files    map[string][]byte
	mu       sync.Mutex
	requests []string
	ranges   []string
	corrupt  map[string]bool
}

func blob(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func newOrigin(t *testing.T) (*origin, *Manifest) {
	o := &origin{files: map[string][]byte{}, corrupt: map[string]bool{}}
	m := &Manifest{Format: ManifestFormat, Packs: map[string]Pack{}}
	add := func(pack string, rows int, sizes map[string]int) {
		p := Pack{Rows: rows}
		seed := int64(len(pack))
		for name, n := range sizes {
			seed++
			data := blob(seed*int64(n), n)
			path := pack + "/" + name
			o.files[path] = data
			sum := sha256.Sum256(data)
			p.Files = append(p.Files, File{Name: name, Path: path, Size: int64(n), SHA256: hex.EncodeToString(sum[:])})
		}
		m.Packs[pack] = p
	}
	add(PackTrial, 1000, map[string]int{PostsFile: 2000, IndexFile(64): 3000, IndexFile(96): 3100, IndexFile(128): 3200, RerankFile: 4000})
	add(PackFull, 9000, map[string]int{PostsFile: 20000, IndexFile(64): 30000, IndexFile(96): 31000, IndexFile(128): 32000, RerankFile: 400000})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	mb, _ := json.Marshal(m)
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == ManifestName {
			w.Write(mb)
			return
		}
		data, ok := o.files[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		o.mu.Lock()
		o.requests = append(o.requests, p)
		o.ranges = append(o.ranges, r.Header.Get("Range"))
		bad := o.corrupt[p]
		o.mu.Unlock()
		if bad {
			data = append([]byte{data[0] ^ 0xff}, data[1:]...)
		}
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(o.srv.Close)
	return o, m
}

func (o *origin) reset() {
	o.mu.Lock()
	o.requests, o.ranges = nil, nil
	o.mu.Unlock()
}

func newInstaller(t *testing.T, o *origin) *Installer {
	self := filepath.Join(t.TempDir(), "BooruScope-Setup")
	os.WriteFile(self, []byte("program bytes"), 0o755)
	return &Installer{Source: o.srv.URL + "/", SelfExe: self, Logf: t.Logf}
}

func runJob(t *testing.T, in *Installer, m *Manifest, req Request) Progress {
	t.Helper()
	if err := in.Start(m, req); err != nil {
		t.Fatal(err)
	}
	<-in.done
	return in.Progress()
}

func dataFiles(t *testing.T, dir string) []string {
	var out []string
	for _, n := range KnownDataFiles() {
		if _, err := os.Stat(filepath.Join(dir, "data", n)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func checkContent(t *testing.T, o *origin, dir, pack, name string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "data", name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, o.files[pack+"/"+name]) {
		t.Fatalf("%s differs from %s/%s", name, pack, name)
	}
}

func TestTrialThenFull(t *testing.T) {
	o, m := newOrigin(t)
	in := newInstaller(t, o)
	dir := filepath.Join(t.TempDir(), "Booru Scope")

	p := runJob(t, in, m, Request{Dir: dir, Choice: Choice{Pack: PackTrial}})
	if p.State != JobDone {
		t.Fatalf("trial: %+v", p)
	}
	if got := dataFiles(t, dir); len(got) != 5 {
		t.Fatalf("trial files %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ExeName())); string(b) != "program bytes" {
		t.Fatalf("exe not copied: %q", b)
	}
	if p.Exe != filepath.Join(dir, ExeName()) {
		t.Fatalf("exe %q", p.Exe)
	}

	p = runJob(t, in, m, Request{Dir: dir, Choice: Choice{Pack: PackFull, Variant: 96, Rerank: true}})
	if p.State != JobDone {
		t.Fatalf("full: %+v", p)
	}
	got := dataFiles(t, dir)
	if strings.Join(got, ",") != "posts.bin,rerank_int8.bin,index_m96.pq" {
		t.Fatalf("full files %v", got)
	}
	for _, n := range got {
		checkContent(t, o, dir, PackFull, n)
	}
	var s map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	json.Unmarshal(b, &s)
	if s["variant"] != float64(96) {
		t.Fatalf("settings %v", s)
	}

	o.reset()
	p = runJob(t, in, m, Request{Dir: dir, Choice: Choice{Pack: PackFull, Variant: 128, Rerank: false}})
	if p.State != JobDone {
		t.Fatalf("switch: %+v", p)
	}
	if strings.Join(o.requests, ",") != "full/index_m128.pq" {
		t.Fatalf("requests %v", o.requests)
	}
	got = dataFiles(t, dir)
	if strings.Join(got, ",") != "posts.bin,rerank_int8.bin,index_m96.pq,index_m128.pq" {
		t.Fatalf("same-pack files should be kept: %v", got)
	}
	for _, f := range p.Files {
		if f.Name == PostsFile && f.State != FileSkipped {
			t.Fatalf("posts not skipped: %+v", f)
		}
	}
}

func TestResumeFromPartialFile(t *testing.T) {
	o, m := newOrigin(t)
	in := newInstaller(t, o)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "data"), 0o755)
	full := o.files["full/"+RerankFile]
	os.WriteFile(filepath.Join(dir, "data", RerankFile+".part"), full[:150000], 0o644)
	p := runJob(t, in, m, Request{Dir: dir, Choice: Choice{Pack: PackFull, Variant: 64, Rerank: true}})
	if p.State != JobDone {
		t.Fatalf("%+v", p)
	}
	checkContent(t, o, dir, PackFull, RerankFile)
	found := false
	for i, r := range o.requests {
		if r == "full/"+RerankFile && o.ranges[i] == "bytes=150000-" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no range request: %v %v", o.requests, o.ranges)
	}
}

func TestChecksumMismatchFails(t *testing.T) {
	o, m := newOrigin(t)
	o.corrupt["full/"+IndexFile(64)] = true
	in := newInstaller(t, o)
	dir := t.TempDir()
	p := runJob(t, in, m, Request{Dir: dir, Choice: Choice{Pack: PackFull, Variant: 64}})
	if p.State != JobFailed || p.Code != "checksum" {
		t.Fatalf("%+v", p)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", IndexFile(64)+".part")); err == nil {
		t.Fatal("bad part file kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "data", IndexFile(64))); err == nil {
		t.Fatal("bad file installed")
	}
}

func TestMissingFileIsNotRetriedForever(t *testing.T) {
	o, m := newOrigin(t)
	delete(o.files, "full/"+PostsFile)
	in := newInstaller(t, o)
	p := runJob(t, in, m, Request{Dir: t.TempDir(), Choice: Choice{Pack: PackFull, Variant: 64}})
	if p.State != JobFailed || p.Code != "download_failed" {
		t.Fatalf("%+v", p)
	}
}

func TestSelectRejectsUnknownVariant(t *testing.T) {
	_, m := newOrigin(t)
	if _, err := m.Select(Choice{Pack: PackFull, Variant: 32}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := m.Select(Choice{Pack: "nope"}); err == nil {
		t.Fatal("expected error")
	}
	bad := *m
	bad.Packs = map[string]Pack{"x": {Files: []File{{Name: PostsFile, Path: "../evil", Size: 1, SHA256: strings.Repeat("0", 64)}}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestServerFlow(t *testing.T) {
	o, _ := newOrigin(t)
	self := filepath.Join(t.TempDir(), "setup-bin")
	os.WriteFile(self, []byte("x"), 0o755)
	var launched []string
	s := NewServer(ServerConfig{
		Web:     fstest.MapFS{"setup.html": {Data: []byte("token={{TOKEN}}")}},
		Source:  o.srv.URL + "/",
		SelfExe: self,
		Logf:    t.Logf,
		Pick:    func(initial, prompt string) (string, error) { return "", ErrNoPicker },
		Launch: func(exe, dir string) error {
			launched = append(launched, exe)
			return nil
		},
	})
	s.SetAddr("127.0.0.1:6000")
	h := s.Handler()
	call := func(method, path string, body any) (int, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Host = "127.0.0.1:6000"
		req.Header.Set(localweb.TokenHeader, s.Token())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Manifest() == nil {
		if time.Now().After(deadline) {
			t.Fatal("manifest not loaded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, st := call("GET", "/api/setup/state", nil)
	if st["manifest"].(map[string]any)["full"].(map[string]any)["rows"] != float64(9000) {
		t.Fatalf("state %v", st)
	}
	if code, out := call("POST", "/api/setup/browse", map[string]string{"dir": "/"}); code != http.StatusNotImplemented || out["error"] != "no_picker" {
		t.Fatalf("browse %d %v", code, out)
	}
	dir := filepath.Join(t.TempDir(), "inst")
	if code, out := call("POST", "/api/setup/check", map[string]string{"dir": dir}); code != 200 || out["writable"] != true {
		t.Fatalf("check %d %v", code, out)
	}
	if code, _ := call("POST", "/api/setup/launch", map[string]any{}); code != http.StatusConflict {
		t.Fatalf("launch before install: %d", code)
	}
	if code, out := call("POST", "/api/setup/install", Request{Dir: dir, Choice: Choice{Pack: PackTrial}}); code != 200 {
		t.Fatalf("install %d %v", code, out)
	}
	for s.Installer().Progress().State == JobRunning {
		time.Sleep(10 * time.Millisecond)
	}
	if p := s.Installer().Progress(); p.State != JobDone {
		t.Fatalf("progress %+v", p)
	}
	if code, _ := call("POST", "/api/setup/launch", map[string]any{}); code != 200 || len(launched) != 1 {
		t.Fatalf("launch %d %v", code, launched)
	}
	select {
	case <-s.Quit():
	case <-time.After(3 * time.Second):
		t.Fatal("installer did not quit after launch")
	}
}

func TestCancelStopsDownload(t *testing.T) {
	o, m := newOrigin(t)
	block := make(chan struct{})
	slow := o.srv.Config.Handler
	o.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, RerankFile) {
			w.Header().Set("Content-Length", "400000")
			w.WriteHeader(200)
			w.Write(make([]byte, 1000))
			w.(http.Flusher).Flush()
			select {
			case <-block:
			case <-r.Context().Done():
			}
			return
		}
		slow.ServeHTTP(w, r)
	})
	defer close(block)
	in := newInstaller(t, o)
	if err := in.Start(m, Request{Dir: t.TempDir(), Choice: Choice{Pack: PackFull, Variant: 64, Rerank: true}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		p := in.Progress()
		if len(p.Files) == 3 && p.Files[2].State == FileDownloading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never started rerank: %+v", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
	in.Cancel()
	if p := in.Progress(); p.State != JobCancelled {
		t.Fatalf("%+v", p)
	}
}
