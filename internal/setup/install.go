package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/fetch"
)

const (
	verifiedName = ".booruscope-verified.json"
	maxAttempts  = 6
)

type FileState string

const (
	FileWaiting     FileState = "waiting"
	FileDownloading FileState = "downloading"
	FileVerifying   FileState = "verifying"
	FileDone        FileState = "done"
	FileSkipped     FileState = "skipped"
	FileFailed      FileState = "failed"
)

type JobState string

const (
	JobIdle      JobState = "idle"
	JobRunning   JobState = "running"
	JobDone      JobState = "done"
	JobFailed    JobState = "failed"
	JobCancelled JobState = "cancelled"
)

type FileProgress struct {
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	Done  int64     `json:"done"`
	State FileState `json:"state"`
}

type Progress struct {
	State JobState       `json:"state"`
	Step  string         `json:"step,omitempty"`
	Files []FileProgress `json:"files"`
	Speed int64          `json:"speed"`
	Code  string         `json:"code,omitempty"`
	Error string         `json:"error,omitempty"`
	Dir   string         `json:"dir,omitempty"`
	Exe   string         `json:"exe,omitempty"`
}

type Request struct {
	Dir      string `json:"dir"`
	Choice   Choice `json:"choice"`
	Shortcut bool   `json:"shortcut"`
}

type Installer struct {
	Source   string
	Token    string
	Client   *http.Client
	SelfExe  string
	Shortcut func(exe, dir string) error
	Logf     func(string, ...any)

	mu     sync.Mutex
	prog   Progress
	cancel context.CancelFunc
	done   chan struct{}
}

type InstallError struct {
	Code string
	Err  error
}

func (e *InstallError) Error() string { return e.Err.Error() }
func (e *InstallError) Unwrap() error { return e.Err }

func ExeName() string {
	if runtime.GOOS == "windows" {
		return "booruscope.exe"
	}
	return "booruscope"
}

func (in *Installer) Progress() Progress {
	in.mu.Lock()
	defer in.mu.Unlock()
	p := in.prog
	p.Files = append([]FileProgress(nil), p.Files...)
	if p.State == "" {
		p.State = JobIdle
	}
	return p
}

func (in *Installer) update(f func(p *Progress)) {
	in.mu.Lock()
	f(&in.prog)
	in.mu.Unlock()
}

func (in *Installer) Start(m *Manifest, req Request) error {
	files, err := m.Select(req.Choice)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(req.Dir)
	if err != nil {
		return err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.prog.State == JobRunning {
		return errors.New("an installation is already running")
	}
	fp := make([]FileProgress, len(files))
	for i, f := range files {
		fp[i] = FileProgress{Name: f.Name, Size: f.Size, State: FileWaiting}
	}
	ctx, cancel := context.WithCancel(context.Background())
	in.cancel = cancel
	in.done = make(chan struct{})
	in.prog = Progress{State: JobRunning, Files: fp, Dir: dir}
	go in.run(ctx, m.Packs[req.Choice.Pack], files, dir, req)
	return nil
}

func (in *Installer) Cancel() {
	in.mu.Lock()
	cancel, done := in.cancel, in.done
	in.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (in *Installer) run(ctx context.Context, pack Pack, files []File, dir string, req Request) {
	defer close(in.done)
	exe, err := in.install(ctx, pack, files, dir, req)
	in.update(func(p *Progress) {
		p.Speed = 0
		switch {
		case err == nil:
			p.State, p.Step, p.Exe = JobDone, "", exe
		case ctx.Err() != nil:
			p.State, p.Error = JobCancelled, ""
		default:
			p.State, p.Error = JobFailed, err.Error()
			var ie *InstallError
			if errors.As(err, &ie) {
				p.Code = ie.Code
			}
		}
	})
	if err != nil && in.Logf != nil {
		in.Logf("setup: %v", err)
	}
}

func (in *Installer) install(ctx context.Context, pack Pack, files []File, dir string, req Request) (string, error) {
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", &InstallError{Code: "not_writable", Err: err}
	}
	in.update(func(p *Progress) { p.Step = "copy" })
	exe, err := in.copySelf(dir)
	if err != nil {
		return "", err
	}
	verified := loadVerified(dataDir)
	in.update(func(p *Progress) { p.Step = "download" })
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		dest := filepath.Join(dataDir, f.Name)
		if verified.matches(dest, f) {
			in.update(func(p *Progress) { p.Files[i].State, p.Files[i].Done = FileSkipped, f.Size })
			continue
		}
		if err := in.fetchFile(ctx, i, f, dest); err != nil {
			in.update(func(p *Progress) { p.Files[i].State = FileFailed })
			return "", err
		}
		verified.record(dest, f)
		if err := verified.save(dataDir); err != nil {
			return "", err
		}
		in.update(func(p *Progress) { p.Files[i].State = FileDone })
	}
	in.update(func(p *Progress) { p.Step = "cleanup" })
	if err := cleanup(dataDir, pack, files, verified); err != nil {
		return "", err
	}
	if req.Choice.Pack == PackFull {
		setVariant(filepath.Join(dir, "settings.json"), req.Choice.Variant)
	} else {
		setVariant(filepath.Join(dir, "settings.json"), 0)
	}
	if req.Shortcut && in.Shortcut != nil {
		in.update(func(p *Progress) { p.Step = "shortcut" })
		if err := in.Shortcut(exe, dir); err != nil && in.Logf != nil {
			in.Logf("setup: shortcut: %v", err)
		}
	}
	return exe, nil
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func (in *Installer) copySelf(dir string) (string, error) {
	dst := filepath.Join(dir, ExeName())
	if in.SelfExe == "" || sameFile(in.SelfExe, dst) {
		return dst, nil
	}
	src, err := os.Open(in.SelfExe)
	if err != nil {
		return "", &InstallError{Code: "copy_failed", Err: err}
	}
	defer src.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", &InstallError{Code: "not_writable", Err: err}
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", &InstallError{Code: "copy_failed", Err: err}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", &InstallError{Code: "copy_failed", Err: err}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", &InstallError{Code: "app_running", Err: err}
	}
	return dst, nil
}

func (in *Installer) client() *http.Client {
	if in.Client != nil {
		return in.Client
	}
	return http.DefaultClient
}

func (in *Installer) fetchFile(ctx context.Context, i int, f File, dest string) error {
	part := dest + ".part"
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		lastErr = in.downloadOnce(ctx, i, f, part)
		if lastErr == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var se *fetch.StatusError
		if errors.As(lastErr, &se) && se.Code >= 400 && se.Code < 500 && se.Code != 408 && se.Code != 429 {
			return &InstallError{Code: "download_failed", Err: fmt.Errorf("%s: %w", f.Name, lastErr)}
		}
		if in.Logf != nil {
			in.Logf("setup: %s: %v (retrying)", f.Name, lastErr)
		}
	}
	if lastErr != nil {
		return &InstallError{Code: "download_failed", Err: fmt.Errorf("%s: %w", f.Name, lastErr)}
	}
	in.update(func(p *Progress) { p.Files[i].State = FileVerifying })
	sum, err := HashFile(ctx, part)
	if err != nil {
		return err
	}
	if sum != f.SHA256 {
		os.Remove(part)
		return &InstallError{Code: "checksum", Err: fmt.Errorf("%s: checksum mismatch", f.Name)}
	}
	os.Remove(dest)
	if err := os.Rename(part, dest); err != nil {
		return &InstallError{Code: "not_writable", Err: err}
	}
	return nil
}

func (in *Installer) downloadOnce(ctx context.Context, i int, f File, part string) error {
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	if have > f.Size {
		os.Remove(part)
		have = 0
	}
	if have == f.Size {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.Source+f.Path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", fetch.UserAgent)
	authorize(req, in.Token)
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := in.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		flags |= os.O_TRUNC
		have = 0
	default:
		return &fetch.StatusError{Code: resp.StatusCode}
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return &InstallError{Code: "not_writable", Err: err}
	}
	defer out.Close()
	in.update(func(p *Progress) { p.Files[i].State, p.Files[i].Done = FileDownloading, have })
	buf := make([]byte, 1<<20)
	tick, tickBytes := time.Now(), int64(0)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return &InstallError{Code: "disk_full", Err: werr}
			}
			have += int64(n)
			tickBytes += int64(n)
			if el := time.Since(tick); el >= 500*time.Millisecond {
				speed := int64(float64(tickBytes) / el.Seconds())
				tick, tickBytes = time.Now(), 0
				in.update(func(p *Progress) { p.Files[i].Done, p.Speed = have, speed })
			} else {
				in.update(func(p *Progress) { p.Files[i].Done = have })
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if have != f.Size {
		return fmt.Errorf("got %d of %d bytes", have, f.Size)
	}
	return nil
}

func HashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 4<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type verifiedEntry struct {
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	ModTime int64  `json:"modTime"`
}

type verifiedSet map[string]verifiedEntry

func loadVerified(dataDir string) verifiedSet {
	v := verifiedSet{}
	if b, err := os.ReadFile(filepath.Join(dataDir, verifiedName)); err == nil {
		json.Unmarshal(b, &v)
	}
	return v
}

func (v verifiedSet) matches(path string, f File) bool {
	e, ok := v[filepath.Base(path)]
	st, err := os.Stat(path)
	return ok && err == nil && e.SHA256 == f.SHA256 && e.Size == f.Size && st.Size() == f.Size && st.ModTime().UnixNano() == e.ModTime
}

func (v verifiedSet) record(path string, f File) {
	if st, err := os.Stat(path); err == nil {
		v[filepath.Base(path)] = verifiedEntry{Size: f.Size, SHA256: f.SHA256, ModTime: st.ModTime().UnixNano()}
	}
}

func (v verifiedSet) save(dataDir string) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(filepath.Join(dataDir, verifiedName), b, 0o644)
}

func cleanup(dataDir string, pack Pack, chosen []File, v verifiedSet) error {
	keep := map[string]bool{}
	for _, f := range chosen {
		keep[f.Name] = true
	}
	changed := false
	for _, name := range KnownDataFiles() {
		if keep[name] {
			continue
		}
		path := filepath.Join(dataDir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if f, ok := pack.File(name); ok && v.matches(path, f) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return &InstallError{Code: "not_writable", Err: err}
		}
		delete(v, name)
		changed = true
	}
	if changed {
		return v.save(dataDir)
	}
	return nil
}

func setVariant(path string, m int) {
	s := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s)
	}
	s["variant"] = m
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(path, b, 0o644)
}
