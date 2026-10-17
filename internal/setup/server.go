package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/localweb"
)

type Server struct {
	guard  *localweb.Guard
	web    fs.FS
	inst   *Installer
	source string
	token  string
	logf   func(string, ...any)
	quit   chan struct{}
	pick   func(initial, prompt string) (string, error)
	launch func(exe, dir string) error

	removeShortcuts func(exe string) error
	findPython      func(ctx context.Context) (string, error)
	python          string
	pythonChecked   bool

	mu       sync.Mutex
	manifest *Manifest
	manErr   error
	loading  bool
	quitOnce sync.Once
}

type ServerConfig struct {
	Web     fs.FS
	Source  string
	SelfExe string
	Logf    func(string, ...any)
	Pick    func(initial, prompt string) (string, error)
	Launch  func(exe, dir string) error
	Client  *http.Client
	Token   string

	RemoveShortcuts func(exe string) error
	Runtime         RuntimeFunc
	FindPython      func(ctx context.Context) (string, error)
}

func NewServer(cfg ServerConfig) *Server {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Pick == nil {
		cfg.Pick = PickFolder
	}
	if cfg.Launch == nil {
		cfg.Launch = LaunchApp
	}
	if cfg.RemoveShortcuts == nil {
		cfg.RemoveShortcuts = RemoveShortcuts
	}
	if cfg.Runtime == nil {
		cfg.Runtime = PrepareRuntime(cfg.Logf)
	}
	if cfg.FindPython == nil {
		cfg.FindPython = DetectPython
	}
	s := &Server{
		guard:  localweb.NewGuard(),
		web:    cfg.Web,
		source: cfg.Source,
		logf:   cfg.Logf,
		quit:   make(chan struct{}),
		pick:   cfg.Pick,
		launch: cfg.Launch,

		removeShortcuts: cfg.RemoveShortcuts,
		findPython:      cfg.FindPython,
		token:           cfg.Token,
		inst:            &Installer{Source: cfg.Source, Token: cfg.Token, SelfExe: cfg.SelfExe, Shortcut: CreateShortcuts, Runtime: cfg.Runtime, Logf: cfg.Logf, Client: cfg.Client},
	}
	s.loadManifest()
	s.checkPython()
	return s
}

func (s *Server) checkPython() {
	s.mu.Lock()
	s.pythonChecked = false
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		v, err := s.findPython(ctx)
		if err != nil {
			s.logf("setup: python: %v", err)
		} else {
			s.logf("setup: python %s", v)
		}
		s.mu.Lock()
		s.python, s.pythonChecked = v, true
		s.mu.Unlock()
	}()
}

func (s *Server) pythonState() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.python, s.pythonChecked
}

func LaunchApp(exe, dir string) error {
	cmd := exec.Command(exe)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (s *Server) SetAddr(addr string)   { s.guard.SetAddr(addr) }
func (s *Server) Quit() <-chan struct{} { return s.quit }
func (s *Server) Installer() *Installer { return s.inst }
func (s *Server) Manifest() *Manifest   { s.mu.Lock(); defer s.mu.Unlock(); return s.manifest }
func (s *Server) requestQuit()          { s.quitOnce.Do(func() { close(s.quit) }) }
func (s *Server) Token() string         { return s.guard.Token() }
func (s *Server) manifestState() (*Manifest, error, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifest, s.manErr, s.loading
}

func (s *Server) loadManifest() {
	s.mu.Lock()
	if s.loading {
		s.mu.Unlock()
		return
	}
	s.loading, s.manErr = true, nil
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m, err := FetchManifest(ctx, s.source, s.token)
		if err != nil {
			s.logf("setup: manifest: %v", err)
		} else {
			for name, p := range m.Packs {
				s.logf("setup: manifest: pack %s, %d rows, %d files", name, p.Rows, len(p.Files))
			}
		}
		s.mu.Lock()
		s.manifest, s.manErr, s.loading = m, err, false
		s.mu.Unlock()
	}()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { s.guard.ServePage(w, s.web, "setup.html") })
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(s.web)))
	mux.HandleFunc("GET /api/setup/state", s.handleState)
	mux.HandleFunc("POST /api/setup/reload", s.handleReload)
	mux.HandleFunc("POST /api/setup/browse", s.handleBrowse)
	mux.HandleFunc("POST /api/setup/check", s.handleCheck)
	mux.HandleFunc("POST /api/setup/install", s.handleInstall)
	mux.HandleFunc("GET /api/setup/progress", s.handleProgress)
	mux.HandleFunc("POST /api/setup/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/setup/launch", s.handleLaunch)
	mux.HandleFunc("POST /api/setup/uninstall", s.handleUninstall)
	mux.HandleFunc("POST /api/setup/quit", s.handleQuit)
	return s.guard.Wrap(mux)
}

type packInfo struct {
	Rows  int              `json:"rows"`
	Files map[string]int64 `json:"files"`
}

type stateResp struct {
	DefaultDir string              `json:"defaultDir"`
	OS         string              `json:"os"`
	Shortcuts  bool                `json:"shortcuts"`
	Source     string              `json:"source"`
	Manifest   map[string]packInfo `json:"manifest,omitempty"`
	ManErr     string              `json:"manifestError,omitempty"`
	ManAuth    bool                `json:"manifestAuth,omitempty"`
	HasToken   bool                `json:"hasToken"`
	Python     string              `json:"python"`
	PyChecked  bool                `json:"pythonChecked"`
	Loading    bool                `json:"manifestLoading"`
	Progress   Progress            `json:"progress"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	m, err, loading := s.manifestState()
	py, pyChecked := s.pythonState()
	out := stateResp{DefaultDir: DefaultDir(), OS: runtime.GOOS, Shortcuts: ShortcutsSupported(), Source: s.source, Loading: loading, Progress: s.inst.Progress(), HasToken: s.token != "", Python: py, PyChecked: pyChecked}
	if err != nil {
		out.ManErr = err.Error()
		out.ManAuth = IsAuthError(err)
	}
	if m != nil {
		out.Manifest = map[string]packInfo{}
		for name, p := range m.Packs {
			pi := packInfo{Rows: p.Rows, Files: map[string]int64{}}
			for _, f := range p.Files {
				pi.Files[f.Name] = f.Size
			}
			out.Manifest[name] = pi
		}
	}
	localweb.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	s.loadManifest()
	s.checkPython()
	localweb.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir    string `json:"dir"`
		Prompt string `json:"prompt"`
	}
	if err := decode(r, &in); err != nil {
		localweb.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	dir, err := s.pick(nearestExisting(in.Dir), in.Prompt)
	if err != nil && !errors.Is(err, ErrPickCancelled) {
		s.logf("setup: folder dialog: %v", err)
	}
	switch {
	case errors.Is(err, ErrPickCancelled):
		localweb.WriteErr(w, http.StatusOK, "cancelled", "")
	case errors.Is(err, ErrNoPicker):
		localweb.WriteErr(w, http.StatusNotImplemented, "no_picker", "")
	case err != nil:
		localweb.WriteErr(w, http.StatusInternalServerError, "picker_failed", err.Error())
	default:
		localweb.WriteJSON(w, http.StatusOK, map[string]string{"dir": dir})
	}
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir string `json:"dir"`
	}
	if err := decode(r, &in); err != nil {
		localweb.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	c, err := CheckDir(in.Dir)
	if err != nil {
		localweb.WriteErr(w, http.StatusBadRequest, "bad_dir", err.Error())
		return
	}
	localweb.WriteJSON(w, http.StatusOK, c)
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var in Request
	if err := decode(r, &in); err != nil {
		localweb.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.logf("setup: install requested: dir=%q pack=%s variant=%d rerank=%v", in.Dir, in.Choice.Pack, in.Choice.Variant, in.Choice.Rerank)
	m := s.Manifest()
	if m == nil {
		s.logf("setup: install refused: no manifest")
		localweb.WriteErr(w, http.StatusServiceUnavailable, "no_manifest", "")
		return
	}
	c, err := CheckDir(in.Dir)
	if err != nil {
		s.logf("setup: install refused: %v", err)
		localweb.WriteErr(w, http.StatusBadRequest, "bad_dir", err.Error())
		return
	}
	if !c.Writable {
		s.logf("setup: install refused: %s is not writable", c.Dir)
		localweb.WriteErr(w, http.StatusBadRequest, "not_writable", c.Dir)
		return
	}
	if py, checked := s.pythonState(); checked && py == "" {
		s.logf("setup: install refused: no Python")
		localweb.WriteErr(w, http.StatusBadRequest, "no_python", "")
		return
	}
	in.Dir = c.Dir
	if err := s.inst.Start(m, in); err != nil {
		s.logf("setup: install refused: %v", err)
		localweb.WriteErr(w, http.StatusBadRequest, "bad_choice", err.Error())
		return
	}
	localweb.WriteJSON(w, http.StatusOK, s.inst.Progress())
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	localweb.WriteJSON(w, http.StatusOK, s.inst.Progress())
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.inst.Cancel()
	localweb.WriteJSON(w, http.StatusOK, s.inst.Progress())
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	p := s.inst.Progress()
	if p.State != JobDone || p.Exe == "" {
		localweb.WriteErr(w, http.StatusConflict, "not_installed", "")
		return
	}
	if err := s.launch(p.Exe, p.Dir); err != nil {
		localweb.WriteErr(w, http.StatusInternalServerError, "launch_failed", err.Error())
		return
	}
	localweb.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.requestQuit()
	}()
}

func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir string `json:"dir"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Dir) == "" {
		localweb.WriteErr(w, http.StatusBadRequest, "bad_dir", "")
		return
	}
	if s.inst.Progress().State == JobRunning {
		localweb.WriteErr(w, http.StatusConflict, "busy", "")
		return
	}
	s.logf("setup: uninstall requested: %q", in.Dir)
	res, err := Uninstall(in.Dir, s.inst.SelfExe, s.removeShortcuts)
	if err != nil {
		s.logf("setup: uninstall: %v", err)
		code := "remove_failed"
		var ie *InstallError
		switch {
		case errors.Is(err, ErrNotInstall):
			code = "not_installed_here"
		case errors.Is(err, ErrUninstallMe):
			code = "uninstall_self"
		case errors.As(err, &ie):
			code = ie.Code
		}
		localweb.WriteJSON(w, http.StatusOK, map[string]any{"error": code, "detail": err.Error(), "result": res})
		return
	}
	s.logf("setup: uninstalled %s: %d items removed, folder removed=%v, kept=%v", res.Dir, res.Removed, res.DirRemoved, res.Kept)
	localweb.WriteJSON(w, http.StatusOK, res)
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	localweb.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.requestQuit()
	}()
}
