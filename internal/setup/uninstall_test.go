package setup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeInstall(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, ExeName()))
	writeFile(t, filepath.Join(dir, "settings.json"))
	for _, n := range KnownDataFiles() {
		writeFile(t, filepath.Join(dir, "data", n))
	}
	writeFile(t, filepath.Join(dir, "data", verifiedName))
	writeFile(t, filepath.Join(dir, "data", RerankFile+".part"))
	writeFile(t, filepath.Join(dir, "runtime", "venv", "bin", "python"))
	writeFile(t, filepath.Join(dir, "runtime", "huggingface", "hub", "model.safetensors"))
	writeFile(t, filepath.Join(dir, "runtime", "encoder", "encode.py"))
	writeFile(t, filepath.Join(dir, "runtime", "booruscope.log"))
}

func TestUninstallRemovesEverythingItOwns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "BooruScope")
	fakeInstall(t, dir)
	var gotExe string
	res, err := Uninstall(dir, "", func(exe string) error { gotExe = exe; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !res.DirRemoved || len(res.Kept) != 0 || !res.Shortcuts {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	if gotExe != filepath.Join(dir, ExeName()) {
		t.Fatalf("shortcut remover got %q", gotExe)
	}
}

func TestUninstallKeepsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	fakeInstall(t, dir)
	writeFile(t, filepath.Join(dir, "my notes.txt"))
	writeFile(t, filepath.Join(dir, "data", "other.bin"))
	writeFile(t, filepath.Join(dir, "runtime", "something-else", "keep.txt"))
	res, err := Uninstall(dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.DirRemoved {
		t.Fatal("folder with foreign files was removed")
	}
	for _, p := range []string{"my notes.txt", "data/other.bin", "runtime/something-else/keep.txt"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("%s was removed", p)
		}
	}
	for _, p := range []string{ExeName(), "settings.json", "data/posts.bin", "runtime/venv", "runtime/huggingface"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Fatalf("%s was kept", p)
		}
	}
	if len(res.Kept) != 3 {
		t.Fatalf("kept %v", res.Kept)
	}
}

func TestUninstallRefusesNonInstall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "data", PostsFile))
	if _, err := Uninstall(dir, "", nil); !errors.Is(err, ErrNotInstall) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", PostsFile)); err != nil {
		t.Fatal("touched a folder that is not an install")
	}
}

func TestUninstallRefusesRunningInstaller(t *testing.T) {
	dir := t.TempDir()
	fakeInstall(t, dir)
	if _, err := Uninstall(dir, filepath.Join(dir, ExeName()), nil); !errors.Is(err, ErrUninstallMe) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ExeName())); err != nil {
		t.Fatal("removed the running installer")
	}
}
