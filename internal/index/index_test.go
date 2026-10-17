package index

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

var packDir = filepath.Join("..", "..", "testdata", "testpack")

type expHit struct {
	Row    int     `json:"row"`
	PostID uint32  `json:"post_id"`
	Score  float64 `json:"score"`
}

type expQuery struct {
	Query int      `json:"query"`
	Vec   string   `json:"query_vec_f32_le_b64"`
	Exact []expHit `json:"exact"`
	Int8  []expHit `json:"int8"`
	M64   []expHit `json:"none_m64"`
	M96   []expHit `json:"none_m96"`
	M128  []expHit `json:"none_m128"`
}

func loadExpected(t *testing.T) []expQuery {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(packDir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows    int        `json:"rows"`
		Queries []expQuery `json:"queries"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Queries) == 0 {
		t.Fatal("no queries in expected.json")
	}
	return doc.Queries
}

func decodeVec(t *testing.T, s string) []float32 {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func compareHits(t *testing.T, label string, got []Hit, want []expHit, posts *Posts) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d hits, want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i].Row != want[i].Row {
			t.Fatalf("%s: rank %d row %d, want %d", label, i, got[i].Row, want[i].Row)
		}
		if d := math.Abs(float64(got[i].Score) - want[i].Score); d > 1e-4 {
			t.Fatalf("%s: rank %d score %.6f, want %.6f", label, i, got[i].Score, want[i].Score)
		}
		if posts.IDs[got[i].Row] != want[i].PostID {
			t.Fatalf("%s: rank %d post %d, want %d", label, i, posts.IDs[got[i].Row], want[i].PostID)
		}
	}
}

func loadPack(t *testing.T, m int) *Engine {
	t.Helper()
	pq, err := LoadPQ(filepath.Join(packDir, fmt.Sprintf("index_m%d.pq", m)))
	if err != nil {
		t.Fatal(err)
	}
	posts, err := LoadPosts(filepath.Join(packDir, "posts.bin"))
	if err != nil {
		t.Fatal(err)
	}
	rr, err := OpenInt8(filepath.Join(packDir, "rerank_int8.bin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rr.Close() })
	e, err := NewEngine(pq, posts, rr)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (q expQuery) coarse(m int) []expHit {
	return map[int][]expHit{64: q.M64, 96: q.M96, 128: q.M128}[m]
}

func TestCoarseMatchesExpected(t *testing.T) {
	qs := loadExpected(t)
	for _, m := range []int{64, 96, 128} {
		e := loadPack(t, m)
		if e.PQ.M != m || e.PQ.Dim != 768 {
			t.Fatalf("m%d: header m=%d dim=%d", m, e.PQ.M, e.PQ.Dim)
		}
		for _, q := range qs {
			want := q.coarse(m)
			got, err := e.Search(decodeVec(t, q.Vec), len(want), false)
			if err != nil {
				t.Fatal(err)
			}
			compareHits(t, fmt.Sprintf("none_m%d query %d", m, q.Query), got, want, e.Posts)
		}
	}
}

func TestInt8FullScanMatchesExpected(t *testing.T) {
	qs := loadExpected(t)
	e := loadPack(t, 64)
	all := make([]int, e.Posts.N)
	for i := range all {
		all[i] = i
	}
	for _, q := range qs {
		v, err := Normalize(decodeVec(t, q.Vec))
		if err != nil {
			t.Fatal(err)
		}
		s, err := e.Int8.Score(v, all)
		if err != nil {
			t.Fatal(err)
		}
		hits := make([]Hit, len(s))
		for i := range s {
			hits[i] = Hit{Row: i, Score: s[i]}
		}
		sortHits(hits)
		compareHits(t, fmt.Sprintf("int8 query %d", q.Query), hits[:len(q.Int8)], q.Int8, e.Posts)
	}
}

func TestRerankPipelineMatchesExpected(t *testing.T) {
	qs := loadExpected(t)
	for _, m := range []int{64, 96, 128} {
		e := loadPack(t, m)
		for _, q := range qs {
			got, err := e.Search(decodeVec(t, q.Vec), len(q.Int8), true)
			if err != nil {
				t.Fatal(err)
			}
			compareHits(t, fmt.Sprintf("rerank m%d query %d", m, q.Query), got, q.Int8, e.Posts)
		}
	}
}

func TestRerankOverlapWithExact(t *testing.T) {
	qs := loadExpected(t)
	e := loadPack(t, 128)
	var sum float64
	for _, q := range qs {
		got, err := e.Search(decodeVec(t, q.Vec), 10, true)
		if err != nil {
			t.Fatal(err)
		}
		exact := map[int]bool{}
		for _, h := range q.Exact[:10] {
			exact[h.Row] = true
		}
		for _, h := range got {
			if exact[h.Row] {
				sum++
			}
		}
	}
	if r := sum / float64(10*len(qs)); r < 0.97 {
		t.Fatalf("recall@10 vs exact %.3f, want >= 0.97", r)
	}
}

func TestPostsAndMD5(t *testing.T) {
	p, err := LoadPosts(filepath.Join(packDir, "posts.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if p.N != 1000 {
		t.Fatalf("n=%d", p.N)
	}
	if h := p.MD5Hex(0); len(h) != 32 {
		t.Fatalf("md5 hex %q", h)
	}
}

func TestRejectsWrongMagic(t *testing.T) {
	if _, err := LoadPQ(filepath.Join(packDir, "posts.bin")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := OpenInt8(filepath.Join(packDir, "index_m64.pq")); err == nil {
		t.Fatal("expected error")
	}
}

func TestEngineRejectsMismatchedRows(t *testing.T) {
	pq, err := LoadPQ(filepath.Join(packDir, "index_m64.pq"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(pq, &Posts{N: 3}, nil); err == nil {
		t.Fatal("expected error")
	}
}
