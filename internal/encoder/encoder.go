package encoder

import (
	"bufio"
	"context"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/proc"
)

//go:embed py/encode.py py/requirements.txt
var assets embed.FS

type State string

const (
	StateIdle    State = "idle"
	StatePython  State = "python"
	StateVenv    State = "venv"
	StateInstall State = "install"
	StateLoading State = "loading"
	StateReady   State = "ready"
	StateError   State = "error"
)

const Dim = 768

var (
	ErrNotReady = errors.New("encoder: not ready")
	ErrInput    = errors.New("encoder: image could not be read")
	ErrNoPython = errors.New("no Python 3.9+ with the venv module was found; install Python from python.org and restart")
)

type Status struct {
	State  State  `json:"state"`
	Step   string `json:"step,omitempty"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
	Device string `json:"device,omitempty"`
}

type Options struct {
	Dir        string
	Python     string
	TorchIndex string
	Model      string
	Device     string
	Logf       func(format string, args ...any)
}

type Encoder struct {
	opt Options

	mu     sync.Mutex
	st     Status
	logBuf []string
	proc   *process
	cancel context.CancelFunc

	reqMu  sync.Mutex
	nextID int64
}

type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan []byte
	done  chan struct{}
}

type message struct {
	Event  string `json:"event"`
	ID     *int64 `json:"id"`
	Vector string `json:"vector"`
	Error  string `json:"error"`
	Detail string `json:"detail"`
	Device string `json:"device"`
	Half   bool   `json:"half"`
}

func New(opt Options) *Encoder {
	if opt.Model == "" {
		opt.Model = DefaultModel
	}
	if opt.Device == "" {
		opt.Device = "auto"
	}
	return &Encoder{opt: opt, st: Status{State: StateIdle}}
}

func (e *Encoder) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st
}

func (e *Encoder) Log() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.logBuf...)
}

func (e *Encoder) setState(s State, step string) {
	e.mu.Lock()
	e.st = Status{State: s, Step: step, Device: e.st.Device}
	e.mu.Unlock()
}

func (e *Encoder) fail(err error) {
	e.logLine(err.Error())
	code := "failed"
	if errors.Is(err, ErrNoPython) {
		code = "no_python"
	}
	e.mu.Lock()
	e.st = Status{State: StateError, Code: code, Detail: err.Error()}
	e.mu.Unlock()
}

func (e *Encoder) setDetail(d string) {
	e.mu.Lock()
	if e.st.State != StateReady && e.st.State != StateError {
		if len(d) > 200 {
			d = d[:200]
		}
		e.st.Detail = d
	}
	e.mu.Unlock()
}

func (e *Encoder) logLine(s string) {
	e.mu.Lock()
	e.logBuf = append(e.logBuf, s)
	if len(e.logBuf) > 300 {
		e.logBuf = e.logBuf[len(e.logBuf)-300:]
	}
	e.mu.Unlock()
	if e.opt.Logf != nil {
		e.opt.Logf("encoder: %s", s)
	}
}

func (e *Encoder) Start() {
	e.mu.Lock()
	if e.st.State != StateIdle && e.st.State != StateError {
		e.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.st = Status{State: StatePython}
	e.mu.Unlock()
	go func() {
		if err := e.boot(ctx); err != nil && ctx.Err() == nil {
			e.fail(err)
		}
	}()
}

func (e *Encoder) boot(ctx context.Context) error {
	if err := e.ensureVenv(ctx); err != nil {
		return err
	}
	e.setState(StateLoading, "")
	scriptDir := filepath.Join(e.opt.Dir, "encoder")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return err
	}
	script, _ := assets.ReadFile("py/encode.py")
	scriptPath := filepath.Join(scriptDir, "encode.py")
	if err := os.WriteFile(scriptPath, script, 0o644); err != nil {
		return err
	}
	cmd := proc.Hide(exec.CommandContext(ctx, e.venvPython(), "-u", scriptPath, "--model", e.opt.Model, "--device", e.opt.Device))
	cmd.Dir = scriptDir
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUNBUFFERED=1", "PYTHONNOUSERSITE=1", "HF_HUB_DISABLE_TELEMETRY=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p := &process{cmd: cmd, stdin: stdin, lines: make(chan []byte, 4), done: make(chan struct{})}
	go e.pumpLines(stderr, true)
	go func() {
		r := bufio.NewReaderSize(stdout, 1<<16)
		for {
			b, err := r.ReadBytes('\n')
			if len(b) > 0 {
				p.lines <- b
			}
			if err != nil {
				break
			}
		}
		cmd.Wait()
		close(p.done)
	}()
	for {
		select {
		case b := <-p.lines:
			var m message
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.Event {
			case "ready":
				e.mu.Lock()
				e.proc = p
				e.st = Status{State: StateReady, Device: m.Device}
				e.mu.Unlock()
				go e.watch(p)
				return nil
			case "fatal":
				cmd.Process.Kill()
				return fmt.Errorf("model load failed: %s", m.Detail)
			}
		case <-p.done:
			return fmt.Errorf("encoder exited during startup: %s", e.lastLog())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (e *Encoder) watch(p *process) {
	<-p.done
	e.mu.Lock()
	if e.proc == p {
		e.proc = nil
		e.st = Status{State: StateError, Code: "exited", Detail: e.lastLogLocked()}
	}
	e.mu.Unlock()
}

func (e *Encoder) lastLog() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastLogLocked()
}

func (e *Encoder) lastLogLocked() string {
	if len(e.logBuf) == 0 {
		return "no output"
	}
	return e.logBuf[len(e.logBuf)-1]
}

func (e *Encoder) Restart() {
	e.Close()
	e.mu.Lock()
	e.st = Status{State: StateIdle}
	e.mu.Unlock()
	e.Start()
}

func (e *Encoder) Close() {
	e.mu.Lock()
	p, cancel := e.proc, e.cancel
	e.proc, e.cancel = nil, nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if p != nil {
		p.stdin.Close()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			p.cmd.Process.Kill()
		}
	}
}

func (e *Encoder) Encode(ctx context.Context, img []byte) ([]float32, error) {
	e.reqMu.Lock()
	defer e.reqMu.Unlock()
	e.mu.Lock()
	p := e.proc
	e.mu.Unlock()
	if p == nil {
		return nil, ErrNotReady
	}
	e.nextID++
	id := e.nextID
	req, _ := json.Marshal(map[string]any{"id": id, "image": base64.StdEncoding.EncodeToString(img)})
	if _, err := p.stdin.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("encoder: write: %w", err)
	}
	for {
		select {
		case b := <-p.lines:
			var m message
			if json.Unmarshal(b, &m) != nil || m.ID == nil || *m.ID != id {
				continue
			}
			if m.Error != "" {
				if m.Error == "input" {
					return nil, fmt.Errorf("%w: %s", ErrInput, m.Detail)
				}
				return nil, fmt.Errorf("encoder: %s", m.Detail)
			}
			return decodeVector(m.Vector)
		case <-p.done:
			return nil, errors.New("encoder: process exited")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func decodeVector(s string) ([]float32, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 4*Dim {
		return nil, fmt.Errorf("encoder: bad vector (%d bytes)", len(b))
	}
	v := make([]float32, Dim)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v, nil
}
