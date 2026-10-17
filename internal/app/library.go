package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/index"
)

var Variants = []int{64, 96, 128}

const (
	PostsFile  = "posts.bin"
	RerankFile = "rerank_int8.bin"
)

func VariantFile(m int) string { return fmt.Sprintf("index_m%d.pq", m) }

type LibraryState string

const (
	LibMissing LibraryState = "missing"
	LibLoading LibraryState = "loading"
	LibReady   LibraryState = "ready"
	LibError   LibraryState = "error"
)

type LibraryStatus struct {
	State     LibraryState `json:"state"`
	Dir       string       `json:"dir"`
	Available []int        `json:"available"`
	Variant   int          `json:"variant"`
	Rows      int          `json:"rows"`
	HasRerank bool         `json:"hasRerank"`
	Detail    string       `json:"detail,omitempty"`
	LoadMs    int64        `json:"loadMs,omitempty"`
}

type Library struct {
	dir string
	log func(string, ...any)

	mu      sync.RWMutex
	st      LibraryStatus
	engine  *index.Engine
	posts   *index.Posts
	rerank  *index.Int8
	loadGen int
}

func NewLibrary(dir string, logf func(string, ...any)) *Library {
	l := &Library{dir: dir, log: logf}
	l.st = LibraryStatus{State: LibMissing, Dir: dir}
	return l
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (l *Library) available() []int {
	var out []int
	for _, m := range Variants {
		if exists(filepath.Join(l.dir, VariantFile(m))) {
			out = append(out, m)
		}
	}
	return out
}

func pickVariant(avail []int, want int) int {
	for _, m := range avail {
		if m == want {
			return m
		}
	}
	if len(avail) == 0 {
		return 0
	}
	return avail[len(avail)-1]
}

func (l *Library) Status() LibraryStatus {
	l.mu.RLock()
	defer l.mu.RUnlock()
	st := l.st
	st.Available = append([]int(nil), st.Available...)
	return st
}

func (l *Library) Engine() *index.Engine {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.engine
}

func (l *Library) Load(want int) {
	avail := l.available()
	m := pickVariant(avail, want)
	hasPosts := exists(filepath.Join(l.dir, PostsFile))
	l.mu.Lock()
	if l.st.State == LibReady && l.st.Variant == m && l.engine != nil {
		l.st.Available = avail
		l.mu.Unlock()
		return
	}
	l.loadGen++
	gen := l.loadGen
	if m == 0 || !hasPosts {
		l.st = LibraryStatus{State: LibMissing, Dir: l.dir, Available: avail}
		l.mu.Unlock()
		return
	}
	l.st = LibraryStatus{State: LibLoading, Dir: l.dir, Available: avail, Variant: m, HasRerank: l.st.HasRerank}
	l.mu.Unlock()
	go l.load(gen, m, avail)
}

func (l *Library) load(gen, m int, avail []int) {
	t0 := time.Now()
	fail := func(err error) {
		l.log("library: %v", err)
		l.mu.Lock()
		if gen == l.loadGen {
			l.st = LibraryStatus{State: LibError, Dir: l.dir, Available: avail, Variant: m, Detail: err.Error()}
			l.engine = nil
		}
		l.mu.Unlock()
	}
	l.mu.RLock()
	posts, rerank := l.posts, l.rerank
	l.mu.RUnlock()
	var err error
	if posts == nil {
		if posts, err = index.LoadPosts(filepath.Join(l.dir, PostsFile)); err != nil {
			fail(err)
			return
		}
	}
	if rerank == nil && exists(filepath.Join(l.dir, RerankFile)) {
		if rerank, err = index.OpenInt8(filepath.Join(l.dir, RerankFile)); err != nil {
			l.log("library: rerank file ignored: %v", err)
			rerank = nil
		}
	}
	l.mu.Lock()
	l.posts, l.rerank = posts, rerank
	l.engine = nil
	l.mu.Unlock()
	pq, err := index.LoadPQ(filepath.Join(l.dir, VariantFile(m)))
	if err != nil {
		fail(err)
		return
	}
	eng, err := index.NewEngine(pq, posts, rerank)
	if err != nil && rerank != nil {
		l.log("library: rerank file ignored: %v", err)
		eng, err = index.NewEngine(pq, posts, nil)
	}
	if err != nil {
		fail(err)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if gen != l.loadGen {
		return
	}
	l.engine = eng
	l.st = LibraryStatus{State: LibReady, Dir: l.dir, Available: avail, Variant: m, Rows: pq.N, HasRerank: eng.Int8 != nil, LoadMs: time.Since(t0).Milliseconds()}
	l.log("library: loaded m%d, %d rows, rerank=%v, %v", m, pq.N, eng.Int8 != nil, time.Since(t0).Round(time.Millisecond))
}

func (l *Library) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.engine = nil
	if l.rerank != nil {
		l.rerank.Close()
		l.rerank = nil
	}
}
