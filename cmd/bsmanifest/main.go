package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lanthanum4000-pixel/BooruScope/internal/index"
	"github.com/lanthanum4000-pixel/BooruScope/internal/setup"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: bsmanifest <folder>\n\nThe folder holds one sub-folder per pack (%s, %s) with the data files.\nWrites <folder>/%s.\n", setup.PackFull, setup.PackTrial, setup.ManifestName)
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	root := flag.Arg(0)
	m := setup.Manifest{Format: setup.ManifestFormat, Packs: map[string]setup.Pack{}}
	for _, pack := range []string{setup.PackFull, setup.PackTrial} {
		dir := filepath.Join(root, pack)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		p, err := buildPack(dir, pack)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		m.Packs[pack] = p
		fmt.Printf("%s: %d rows, %d files\n", pack, p.Rows, len(p.Files))
	}
	if len(m.Packs) == 0 {
		fmt.Fprintln(os.Stderr, "no packs found")
		os.Exit(1)
	}
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	out := filepath.Join(root, setup.ManifestName)
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}

func buildPack(dir, pack string) (setup.Pack, error) {
	posts, err := index.LoadPosts(filepath.Join(dir, setup.PostsFile))
	if err != nil {
		return setup.Pack{}, err
	}
	p := setup.Pack{Rows: posts.N}
	names := setup.KnownDataFiles()
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if err := checkRows(path, name, posts.N); err != nil {
			return setup.Pack{}, fmt.Errorf("%s/%s: %w", pack, name, err)
		}
		fmt.Printf("hashing %s/%s (%d bytes)\n", pack, name, st.Size())
		sum, err := setup.HashFile(context.Background(), path)
		if err != nil {
			return setup.Pack{}, err
		}
		p.Files = append(p.Files, setup.File{Name: name, Path: pack + "/" + name, Size: st.Size(), SHA256: sum})
	}
	return p, nil
}

func checkRows(path, name string, rows int) error {
	switch name {
	case setup.PostsFile:
		return nil
	case setup.RerankFile:
		v, err := index.OpenInt8(path)
		if err != nil {
			return err
		}
		defer v.Close()
		if v.N != rows {
			return fmt.Errorf("%d rows, posts.bin has %d", v.N, rows)
		}
		return nil
	default:
		pq, err := index.LoadPQHeader(path)
		if err != nil {
			return err
		}
		if pq.N != rows {
			return fmt.Errorf("%d rows, posts.bin has %d", pq.N, rows)
		}
		return nil
	}
}
