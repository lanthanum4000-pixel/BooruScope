package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lanthanum4000-pixel/BooruScope/internal/fetch"
)

const (
	DefaultSource  = "https://huggingface.co/Lanthanum4000/BooruScope/resolve/main/"
	ManifestName   = "manifest.json"
	ManifestFormat = "booruscope-manifest-v1"
	PackFull       = "full"
	PackTrial      = "trial"
	PostsFile      = "posts.bin"
	RerankFile     = "rerank_int8.bin"
)

var Variants = []int{64, 96, 128}

func IndexFile(m int) string { return fmt.Sprintf("index_m%d.pq", m) }

func KnownDataFiles() []string {
	out := []string{PostsFile, RerankFile}
	for _, m := range Variants {
		out = append(out, IndexFile(m))
	}
	return out
}

type File struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Pack struct {
	Rows  int    `json:"rows"`
	Files []File `json:"files"`
}

type Manifest struct {
	Format string          `json:"format"`
	Packs  map[string]Pack `json:"packs"`
}

func (p Pack) File(name string) (File, bool) {
	for _, f := range p.Files {
		if f.Name == name {
			return f, true
		}
	}
	return File{}, false
}

func (m *Manifest) Validate() error {
	if m.Format != ManifestFormat {
		return fmt.Errorf("manifest: format %q, want %q", m.Format, ManifestFormat)
	}
	known := map[string]bool{}
	for _, n := range KnownDataFiles() {
		known[n] = true
	}
	for name, p := range m.Packs {
		if _, ok := p.File(PostsFile); !ok {
			return fmt.Errorf("manifest: pack %q has no %s", name, PostsFile)
		}
		for _, f := range p.Files {
			if !known[f.Name] || f.Path == "" || f.Size <= 0 || len(f.SHA256) != 64 || strings.Contains(f.Path, "..") {
				return fmt.Errorf("manifest: pack %q: bad entry %+v", name, f)
			}
		}
	}
	return nil
}

type Choice struct {
	Pack    string `json:"pack"`
	Variant int    `json:"variant"`
	Rerank  bool   `json:"rerank"`
}

func (m *Manifest) Select(c Choice) ([]File, error) {
	p, ok := m.Packs[c.Pack]
	if !ok {
		return nil, fmt.Errorf("unknown pack %q", c.Pack)
	}
	if c.Pack == PackTrial {
		return append([]File(nil), p.Files...), nil
	}
	posts, _ := p.File(PostsFile)
	idx, ok := p.File(IndexFile(c.Variant))
	if !ok {
		return nil, fmt.Errorf("pack %q has no M%d index", c.Pack, c.Variant)
	}
	out := []File{posts, idx}
	if c.Rerank {
		rr, ok := p.File(RerankFile)
		if !ok {
			return nil, fmt.Errorf("pack %q has no rerank file", c.Pack)
		}
		out = append(out, rr)
	}
	return out, nil
}

func statusCode(err error) (int, bool) {
	var se *fetch.StatusError
	if errors.As(err, &se) {
		return se.Code, true
	}
	return 0, false
}

func FetchManifest(ctx context.Context, source, token string) (*Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source+ManifestName, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fetch.UserAgent)
	authorize(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &fetch.StatusError{Code: resp.StatusCode}
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, errors.New("manifest: " + err.Error())
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
