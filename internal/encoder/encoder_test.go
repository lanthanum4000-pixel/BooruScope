package encoder

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("BOORUSCOPE_FAKE_PY") != "1" {
		return
	}
	out := bufio.NewWriter(os.Stdout)
	emit := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	emit(map[string]any{"event": "loading"})
	fmt.Fprintln(os.Stderr, "model.safetensors: 42%|####")
	emit(map[string]any{"event": "ready", "device": "cpu"})
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var req struct {
			ID    int64  `json:"id"`
			Image string `json:"image"`
		}
		json.Unmarshal(sc.Bytes(), &req)
		img, _ := base64.StdEncoding.DecodeString(req.Image)
		if string(img) == "bad" {
			emit(map[string]any{"id": req.ID, "error": "input", "detail": "cannot decode image"})
			continue
		}
		v := make([]byte, 4*Dim)
		first := float32(len(img))
		if len(img) > 4 && string(img[1:4]) == "PNG" {
			first = 1
		}
		binary.LittleEndian.PutUint32(v, math.Float32bits(first))
		emit(map[string]any{"id": req.ID, "vector": base64.StdEncoding.EncodeToString(v)})
	}
	os.Exit(0)
}

func fakeEncoder(t *testing.T) *Encoder {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	dir := t.TempDir()
	e := New(Options{Dir: dir, TorchIndex: "pypi"})
	py := e.venvPython()
	if err := os.MkdirAll(filepath.Dir(py), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	shim := "#!/bin/sh\nBOORUSCOPE_FAKE_PY=1 exec \"" + exe + "\" -test.run=^TestHelperProcess$\n"
	if err := os.WriteFile(py, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.venvDir(), stampName), []byte(e.stamp()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return e
}

func waitState(t *testing.T, e *Encoder, want State) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for e.Status().State != want {
		if time.Now().After(deadline) {
			t.Fatalf("state %+v, want %s; log %v", e.Status(), want, e.Log())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	e := fakeEncoder(t)
	defer e.Close()
	e.Start()
	waitState(t, e, StateReady)
	if d := e.Status().Device; d != "cpu" {
		t.Fatalf("device %q", d)
	}
	v, err := e.Encode(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != Dim || v[0] != 5 {
		t.Fatalf("vector %v", v[:2])
	}
	if _, err := e.Encode(context.Background(), []byte("bad")); !errors.Is(err, ErrInput) {
		t.Fatalf("err %v", err)
	}
	if _, err := e.Encode(context.Background(), []byte("again")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.opt.Dir, "encoder", "encode.py")); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeBeforeReady(t *testing.T) {
	e := New(Options{Dir: t.TempDir()})
	if _, err := e.Encode(context.Background(), []byte("x")); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err %v", err)
	}
}

func TestMissingPython(t *testing.T) {
	e := New(Options{Dir: t.TempDir(), Python: filepath.Join(t.TempDir(), "no-python")})
	e.Start()
	waitState(t, e, StateError)
	if c := e.Status().Code; c != "no_python" {
		t.Fatalf("code %q", c)
	}
}

func TestRestartAfterExit(t *testing.T) {
	e := fakeEncoder(t)
	defer e.Close()
	e.Start()
	waitState(t, e, StateReady)
	e.mu.Lock()
	p := e.proc
	e.mu.Unlock()
	p.cmd.Process.Kill()
	waitState(t, e, StateError)
	if c := e.Status().Code; c != "exited" {
		t.Fatalf("code %q", c)
	}
	e.Restart()
	waitState(t, e, StateReady)
}

func TestVerifyRunsTestImage(t *testing.T) {
	e := fakeEncoder(t)
	defer e.Close()
	if err := e.Verify(context.Background()); err != nil {
		t.Fatalf("%v; log %v", err, e.Log())
	}
	if st := e.Status(); st.State != StateReady || st.Device != "cpu" {
		t.Fatalf("%+v", st)
	}
	if _, err := e.Encode(context.Background(), TestImage()); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWithoutPython(t *testing.T) {
	e := New(Options{Dir: t.TempDir(), Python: filepath.Join(t.TempDir(), "none")})
	if err := e.Verify(context.Background()); !errors.Is(err, ErrNoPython) {
		t.Fatalf("err %v", err)
	}
	if st := e.Status(); st.State != StateError || st.Code != "no_python" {
		t.Fatalf("%+v", st)
	}
}
