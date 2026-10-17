package index

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
)

func syntheticPQ(n, m, dim int) *PQ {
	rng := rand.New(rand.NewSource(1))
	p := &PQ{M: m, Dim: dim, N: n, Mean: make([]float32, dim), R: make([]float32, dim*dim), Centroids: make([]float32, m*codebookK*(dim/m)), Codes: make([]byte, n*m)}
	for i := 0; i < dim; i++ {
		p.R[i*dim+i] = 1
	}
	for i := range p.Centroids {
		p.Centroids[i] = float32(rng.NormFloat64())
	}
	rng.Read(p.Codes)
	return p
}

func BenchmarkCoarse(b *testing.B) {
	n := 1_000_000
	if s := os.Getenv("BOORUSCOPE_BENCH_ROWS"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	for _, m := range []int{64, 96, 128} {
		p := syntheticPQ(n, m, 768)
		q := make([]float32, 768)
		for i := range q {
			q[i] = float32(i%7) - 3
		}
		q, _ = Normalize(q)
		b.Run("m"+strconv.Itoa(m)+"_k1920", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				p.Coarse(q, 1920)
			}
		})
	}
}
