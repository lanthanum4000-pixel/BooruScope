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
	"github.com/lanthanum4000-pixel/BooruScope/internal/localweb"
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

	life := srv.Lifetime()
	go func() {
		<-srv.Quit()
		life.Quit()
	}()
	profile := ""
	var exited chan bool
	if !opt.NoWindow {
		if b, err := browser.Find(opt.BrowserPath); err == nil {
			removeLegacyProfiles(opt.Logf)
			profile = setupProfileDir()
			t0 := time.Now()
			cmd, err := browser.LaunchApp(b, url, profile, nil)
			if err != nil {
				opt.Logf("window: %v", err)
				life.NoBrowserProcess()
				profile = ""
			} else {
				opt.Logf("window: %s (%s)", b.Kind, b.Path)
				exited = make(chan bool, 1)
				go func() {
					cmd.Wait()
					ran := time.Since(t0)
					opt.Logf("window: browser process exited after %v", ran.Round(time.Millisecond))
					life.BrowserExited(ran)
					exited <- ran >= localweb.HandoffWindow
				}()
			}
		} else {
			opt.Logf("window: %v; opening the default browser instead", err)
			browser.OpenDefault(url)
			life.NoBrowserProcess()
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opt.Logf("setup: shutting down: %s", life.Wait(ctx))
	srv.Installer().Cancel()
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = httpSrv.Shutdown(sctx)
	if exited != nil {
		select {
		case own := <-exited:
			if own {
				if rerr := removeProfile(profile, 20, 250*time.Millisecond); rerr != nil {
					opt.Logf("setup: browser profile left in %s: %v", profile, rerr)
				}
			}
		case <-time.After(5 * time.Second):
			opt.Logf("setup: window still open; browser profile kept in %s", profile)
		}
	}
	return err
}
