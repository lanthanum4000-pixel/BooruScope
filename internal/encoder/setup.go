package encoder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/lanthanum4000-pixel/BooruScope/internal/proc"
)

const (
	DefaultModel   = "openai/clip-vit-large-patch14"
	torchIndexCUDA = "https://download.pytorch.org/whl/cu128"
	torchIndexCPU  = "https://download.pytorch.org/whl/cpu"
	stampName      = "booruscope-stamp"
)

func (e *Encoder) venvDir() string { return filepath.Join(e.opt.Dir, "venv") }

func (e *Encoder) venvPython() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(e.venvDir(), "Scripts", "python.exe")
	}
	return filepath.Join(e.venvDir(), "bin", "python")
}

func (e *Encoder) torchIndex() string {
	switch e.opt.TorchIndex {
	case "pypi":
		return ""
	case "":
	default:
		return e.opt.TorchIndex
	}
	if runtime.GOOS == "darwin" {
		return ""
	}
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return torchIndexCUDA
	}
	return torchIndexCPU
}

func (e *Encoder) stamp() string {
	req, _ := assets.ReadFile("py/requirements.txt")
	h := sha256.New()
	h.Write(req)
	h.Write([]byte{0})
	h.Write([]byte(e.torchIndex()))
	return hex.EncodeToString(h.Sum(nil))
}

func pythonCandidates() [][]string {
	if runtime.GOOS == "windows" {
		return [][]string{{"py", "-3"}, {"python"}, {"python3"}}
	}
	return [][]string{{"python3"}, {"python"}}
}

func FindPython(ctx context.Context, override string) ([]string, string, error) {
	cands := pythonCandidates()
	if override != "" {
		cands = [][]string{{override}}
	}
	probe := "import sys, venv; print('%d.%d.%d' % sys.version_info[:3]); sys.exit(0 if sys.version_info >= (3, 9) else 3)"
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		args := append(append([]string{}, c[1:]...), "-c", probe)
		out, err := proc.Hide(exec.CommandContext(ctx, c[0], args...)).Output()
		if err == nil {
			return c, strings.TrimSpace(string(out)), nil
		}
	}
	return nil, "", ErrNoPython
}

func HFHome(runtimeDir string) string {
	if h := os.Getenv("HF_HOME"); h != "" {
		return h
	}
	return filepath.Join(runtimeDir, "huggingface")
}

func (e *Encoder) findPython(ctx context.Context) ([]string, error) {
	c, _, err := FindPython(ctx, e.opt.Python)
	return c, err
}

func (e *Encoder) ensureVenv(ctx context.Context) error {
	want := e.stamp()
	if got, err := os.ReadFile(filepath.Join(e.venvDir(), stampName)); err == nil && strings.TrimSpace(string(got)) == want {
		if _, err := os.Stat(e.venvPython()); err == nil {
			return nil
		}
	}
	if _, err := os.Stat(e.venvPython()); err != nil {
		e.setState(StatePython, "")
		base, err := e.findPython(ctx)
		if err != nil {
			return err
		}
		e.setState(StateVenv, "")
		if err := os.MkdirAll(e.opt.Dir, 0o755); err != nil {
			return err
		}
		args := append(append([]string{}, base[1:]...), "-m", "venv", e.venvDir())
		if err := e.runLogged(ctx, base[0], args...); err != nil {
			return fmt.Errorf("create venv: %w", err)
		}
	}
	e.setState(StateInstall, "pip")
	py := e.venvPython()
	pip := []string{"-m", "pip", "install", "--disable-pip-version-check", "--progress-bar", "raw"}
	if err := e.runLogged(ctx, py, "-m", "pip", "install", "--disable-pip-version-check", "--progress-bar", "off", "--upgrade", "pip"); err != nil {
		return fmt.Errorf("upgrade pip: %w", err)
	}
	e.setState(StateInstall, "torch")
	idx := e.torchIndex()
	torchArgs := append(append([]string{}, pip...), "torch")
	if idx != "" {
		if err := e.runLogged(ctx, py, append(torchArgs, "--index-url", idx)...); err != nil {
			e.logLine("torch index failed, retrying from PyPI: " + err.Error())
			if err := e.runLogged(ctx, py, torchArgs...); err != nil {
				return fmt.Errorf("install torch: %w", err)
			}
		}
	} else if err := e.runLogged(ctx, py, torchArgs...); err != nil {
		return fmt.Errorf("install torch: %w", err)
	}
	e.setState(StateInstall, "transformers")
	reqPath := filepath.Join(e.opt.Dir, "requirements.txt")
	req, _ := assets.ReadFile("py/requirements.txt")
	if err := os.WriteFile(reqPath, req, 0o644); err != nil {
		return err
	}
	if err := e.runLogged(ctx, py, append(pip, "-r", reqPath)...); err != nil {
		return fmt.Errorf("install requirements: %w", err)
	}
	return os.WriteFile(filepath.Join(e.venvDir(), stampName), []byte(want+"\n"), 0o644)
}

func (e *Encoder) runLogged(ctx context.Context, name string, args ...string) error {
	cmd := proc.Hide(exec.CommandContext(ctx, name, args...))
	cmd.Env = append(append(os.Environ(), e.opt.Env...), "PYTHONIOENCODING=utf-8", "PIP_NO_INPUT=1")
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.pumpLines(pr, true)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

var pipProgress = regexp.MustCompile(`^Progress (\d+) of (\d+)$`)

func scanCRLF(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (e *Encoder) pumpLines(r io.Reader, detail bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	sc.Split(scanCRLF)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if m := pipProgress.FindStringSubmatch(line); m != nil {
			done, _ := strconv.ParseInt(m[1], 10, 64)
			total, _ := strconv.ParseInt(m[2], 10, 64)
			e.setProgress(done, total)
			continue
		}
		e.logLine(line)
		if strings.HasPrefix(line, "Installing collected packages") {
			e.setPhase(PhaseUnpack)
		}
		if detail {
			e.setDetail(line)
		}
	}
}
