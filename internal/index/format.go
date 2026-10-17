package index

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	magicPQ    = "BSPQ0001"
	magicPosts = "BSPOSTS1"
	magicInt8  = "BSI8V001"
	codebookK  = 256
	md5Size    = 16
	postSize   = 4 + md5Size
)

var ErrFormat = errors.New("index: bad file format")

type PQ struct {
	M         int
	Dim       int
	N         int
	Mean      []float32
	R         []float32
	Centroids []float32
	Codes     []byte
}

type Posts struct {
	N   int
	IDs []uint32
	MD5 []byte
}

type Int8 struct {
	f     *os.File
	N     int
	Dim   int
	Scale []float32
	base  int64
}

func formatErr(path, msg string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrFormat, path, fmt.Sprintf(msg, args...))
}

func openChecked(path, magic string, headerWords int) (*os.File, *bufio.Reader, []uint32, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, nil, 0, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	head := make([]byte, len(magic)+4*headerWords)
	if _, err := io.ReadFull(r, head); err != nil {
		f.Close()
		return nil, nil, nil, 0, formatErr(path, "short header")
	}
	if string(head[:len(magic)]) != magic {
		f.Close()
		return nil, nil, nil, 0, formatErr(path, "magic %q, want %q", head[:len(magic)], magic)
	}
	words := make([]uint32, headerWords)
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(head[len(magic)+4*i:])
	}
	return f, r, words, st.Size(), nil
}

func readFloats(r io.Reader, n int) ([]float32, error) {
	buf := make([]byte, 4*n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
	}
	return out, nil
}

func LoadPQ(path string) (*PQ, error) {
	f, r, w, size, err := openChecked(path, magicPQ, 4)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, dim, n, k := int(w[0]), int(w[1]), int(w[2]), int(w[3])
	if m <= 0 || dim <= 0 || dim%m != 0 || k != codebookK {
		return nil, formatErr(path, "m=%d dim=%d k=%d", m, dim, k)
	}
	want := int64(len(magicPQ)+16) + 4*int64(dim) + 4*int64(dim)*int64(dim) + 4*int64(m)*int64(k)*int64(dim/m) + int64(n)*int64(m)
	if size != want {
		return nil, formatErr(path, "size %d, want %d", size, want)
	}
	p := &PQ{M: m, Dim: dim, N: n}
	if p.Mean, err = readFloats(r, dim); err != nil {
		return nil, formatErr(path, "mean: %v", err)
	}
	if p.R, err = readFloats(r, dim*dim); err != nil {
		return nil, formatErr(path, "rotation: %v", err)
	}
	if p.Centroids, err = readFloats(r, m*k*(dim/m)); err != nil {
		return nil, formatErr(path, "centroids: %v", err)
	}
	p.Codes = make([]byte, n*m)
	if _, err := io.ReadFull(r, p.Codes); err != nil {
		return nil, formatErr(path, "codes: %v", err)
	}
	return p, nil
}

func LoadPosts(path string) (*Posts, error) {
	f, r, w, size, err := openChecked(path, magicPosts, 1)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n := int(w[0])
	if want := int64(len(magicPosts)+4) + int64(n)*postSize; size != want {
		return nil, formatErr(path, "size %d, want %d", size, want)
	}
	p := &Posts{N: n, IDs: make([]uint32, n), MD5: make([]byte, n*md5Size)}
	rec := make([]byte, postSize)
	for i := 0; i < n; i++ {
		if _, err := io.ReadFull(r, rec); err != nil {
			return nil, formatErr(path, "row %d: %v", i, err)
		}
		p.IDs[i] = binary.LittleEndian.Uint32(rec)
		copy(p.MD5[i*md5Size:], rec[4:])
	}
	return p, nil
}

func (p *Posts) MD5Hex(row int) string {
	return hex.EncodeToString(p.MD5[row*md5Size : (row+1)*md5Size])
}

func OpenInt8(path string) (*Int8, error) {
	f, r, w, size, err := openChecked(path, magicInt8, 2)
	if err != nil {
		return nil, err
	}
	n, dim := int(w[0]), int(w[1])
	base := int64(len(magicInt8)+8) + 4*int64(dim)
	if want := base + int64(n)*int64(dim); dim <= 0 || size != want {
		f.Close()
		return nil, formatErr(path, "size %d, want %d", size, want)
	}
	scale, err := readFloats(r, dim)
	if err != nil {
		f.Close()
		return nil, formatErr(path, "scale: %v", err)
	}
	return &Int8{f: f, N: n, Dim: dim, Scale: scale, base: base}, nil
}

func (v *Int8) Close() error {
	if v == nil || v.f == nil {
		return nil
	}
	return v.f.Close()
}
