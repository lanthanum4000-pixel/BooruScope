package setup

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/browser"
)

type Options struct {
	Web         fs.FS
	Source      string
	Port        int
	NoWindow    bool
	BrowserPath string
	Logf        func(string, ...any)
}

func Run(opt Options) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	token := HFToken()
	if token != "" {
		opt.Logf("setup: using a Hugging Face access token")
	}
	srv := NewServer(ServerConfig{Web: opt.Web, Source: opt.Source, SelfExe: self, Logf: opt.Logf, Token: token})
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opt.Port)))
	if err != nil {
		return err
	}
	srv.SetAddr(ln.Addr().String())
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			opt.Logf("setup: %v", err)
		}
	}()
	url := "http://" + ln.Addr().String() + "/"
	opt.Logf("BooruScope Setup at %s", url)

	closed := make(chan struct{})
	if !opt.NoWindow {
		if b, err := browser.Find(opt.BrowserPath); err == nil {
			profile, _ := os.MkdirTemp("", "booruscope-setup-")
			cmd, err := browser.LaunchApp(b, url, profile)
			if err != nil {
				opt.Logf("window: %v", err)
			} else {
				go func() {
					cmd.Wait()
					os.RemoveAll(profile)
					close(closed)
				}()
			}
		} else {
			opt.Logf("window: %v; opening the default browser instead", err)
			browser.OpenDefault(url)
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case <-closed:
	case <-srv.Quit():
	}
	srv.Installer().Cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return httpSrv.Shutdown(ctx)
}
