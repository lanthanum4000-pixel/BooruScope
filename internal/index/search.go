package index

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
)

const RerankFactor = 32

type Hit struct {
	Row   int
	Score float32
}

func better(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Row < b.Row
}

type worstFirst []Hit

func (h worstFirst) Len() int           { return len(h) }
func (h worstFirst) Less(i, j int) bool { return better(h[j], h[i]) }
func (h worstFirst) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *worstFirst) Push(x any)        { *h = append(*h, x.(Hit)) }
func (h *worstFirst) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

type topK struct {
	k int
	h worstFirst
}

func newTopK(k int) *topK { return &topK{k: k, h: make(worstFirst, 0, k)} }

func (t *topK) offer(x Hit) {
	if len(t.h) < t.k {
		heap.Push(&t.h, x)
		return
	}
	if better(x, t.h[0]) {
		t.h[0] = x
		heap.Fix(&t.h, 0)
	}
}

func (t *topK) sorted() []Hit {
	out := append([]Hit(nil), t.h...)
	sortHits(out)
	return out
}

func sortHits(h []Hit) {
	sort.Slice(h, func(i, j int) bool { return better(h[i], h[j]) })
}

func Normalize(q []float32) ([]float32, error) {
	var s float64
	for _, x := range q {
		s += float64(x) * float64(x)
	}
	if s == 0 || math.IsNaN(s) || math.IsInf(s, 0) {
		return nil, errors.New("index: query vector has no direction")
	}
	inv := 1 / math.Sqrt(s)
	out := make([]float32, len(q))
	for i, x := range q {
		out[i] = float32(float64(x) * inv)
	}
	return out, nil
}

func (p *PQ) table(q []float32) ([][codebookK]float32, float32) {
	qr := make([]float64, p.Dim)
	for i, x := range q {
		if x == 0 {
			continue
		}
		row := p.R[i*p.Dim : (i+1)*p.Dim]
		xf := float64(x)
		for j, r := range row {
			qr[j] += xf * float64(r)
		}
	}
	sub := p.Dim / p.M
	t := make([][codebookK]float32, p.M)
	for s := 0; s < p.M; s++ {
		qs := qr[s*sub : (s+1)*sub]
		for c := 0; c < codebookK; c++ {
			cen := p.Centroids[(s*codebookK+c)*sub : (s*codebookK+c+1)*sub]
			var acc float64
			for d, v := range cen {
				acc += qs[d] * float64(v)
			}
			t[s][c] = float32(acc)
		}
	}
	var qm float64
	for i, x := range q {
		qm += float64(x) * float64(p.Mean[i])
	}
	return t, float32(qm)
}

func (p *PQ) Coarse(q []float32, k int) []Hit {
	if k <= 0 || p.N == 0 {
		return nil
	}
	if k > p.N {
		k = p.N
	}
	t, qm := p.table(q)
	workers := runtime.GOMAXPROCS(0)
	if minRows := 1 << 15; p.N/workers < minRows {
		workers = max(1, p.N/minRows)
	}
	chunk := (p.N + workers - 1) / workers
	parts := make([]*topK, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo, hi := w*chunk, min(p.N, (w+1)*chunk)
		parts[w] = newTopK(k)
		wg.Add(1)
		go func(tk *topK, lo, hi int) {
			defer wg.Done()
			m := p.M
			for i := lo; i < hi; i++ {
				codes := p.Codes[i*m : (i+1)*m]
				tt := t[:len(codes)]
				var s float32
				for seg, c := range codes {
					s += tt[seg][c]
				}
				h := Hit{Row: i, Score: s + qm}
				if len(tk.h) == tk.k && !better(h, tk.h[0]) {
					continue
				}
				tk.offer(h)
			}
		}(parts[w], lo, hi)
	}
	wg.Wait()
	all := newTopK(k)
	for _, tk := range parts {
		for _, h := range tk.h {
			all.offer(h)
		}
	}
	return all.sorted()
}

func (v *Int8) Score(q []float32, rows []int) ([]float32, error) {
	if len(q) != v.Dim {
		return nil, fmt.Errorf("index: query dim %d, int8 dim %d", len(q), v.Dim)
	}
	w := make([]float32, v.Dim)
	for j := range w {
		w[j] = v.Scale[j] * q[j]
	}
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return rows[order[a]] < rows[order[b]] })
	out := make([]float32, len(rows))
	buf := make([]byte, 0, 64*v.Dim)
	for i := 0; i < len(order); {
		j := i + 1
		for j < len(order) && j-i < 64 && rows[order[j]] == rows[order[j-1]]+1 {
			j++
		}
		first := rows[order[i]]
		if first < 0 || rows[order[j-1]] >= v.N {
			return nil, fmt.Errorf("index: int8 row %d out of range", first)
		}
		buf = buf[:(j-i)*v.Dim]
		if _, err := v.f.ReadAt(buf, v.base+int64(first)*int64(v.Dim)); err != nil {
			return nil, fmt.Errorf("index: read int8 rows: %w", err)
		}
		for k := i; k < j; k++ {
			rb := buf[(k-i)*v.Dim : (k-i+1)*v.Dim]
			var s float32
			for d, b := range rb {
				s += float32(int8(b)) * w[d]
			}
			out[order[k]] = s
		}
		i = j
	}
	return out, nil
}

type Engine struct {
	PQ    *PQ
	Posts *Posts
	Int8  *Int8
}

func NewEngine(pq *PQ, posts *Posts, rr *Int8) (*Engine, error) {
	if pq.N != posts.N {
		return nil, fmt.Errorf("%w: index has %d rows, posts has %d", ErrFormat, pq.N, posts.N)
	}
	if rr != nil && (rr.N != pq.N || rr.Dim != pq.Dim) {
		return nil, fmt.Errorf("%w: rerank file has %d×%d, index has %d×%d", ErrFormat, rr.N, rr.Dim, pq.N, pq.Dim)
	}
	return &Engine{PQ: pq, Posts: posts, Int8: rr}, nil
}

func (e *Engine) Search(q []float32, k int, rerank bool) ([]Hit, error) {
	if len(q) != e.PQ.Dim {
		return nil, fmt.Errorf("index: query dim %d, index dim %d", len(q), e.PQ.Dim)
	}
	q, err := Normalize(q)
	if err != nil {
		return nil, err
	}
	if !rerank || e.Int8 == nil {
		return e.PQ.Coarse(q, k), nil
	}
	cand := e.PQ.Coarse(q, k*RerankFactor)
	rows := make([]int, len(cand))
	for i, h := range cand {
		rows[i] = h.Row
	}
	scores, err := e.Int8.Score(q, rows)
	if err != nil {
		return nil, err
	}
	for i := range cand {
		cand[i].Score = scores[i]
	}
	sortHits(cand)
	if len(cand) > k {
		cand = cand[:k]
	}
	return cand, nil
}
