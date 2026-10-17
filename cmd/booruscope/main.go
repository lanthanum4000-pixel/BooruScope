package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/app"
	"github.com/lanthanum4000-pixel/BooruScope/internal/browser"
	"github.com/lanthanum4000-pixel/BooruScope/internal/encoder"
	"github.com/lanthanum4000-pixel/BooruScope/internal/fetch"
	"github.com/lanthanum4000-pixel/BooruScope/web"
)

func defaultHome() string {
	if h := os.Getenv("BOORUSCOPE_HOME"); h != "" {
		return h
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if strings.HasPrefix(dir, os.TempDir()) {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
	}
	return dir
}

func main() {
	home := flag.String("home", defaultHome(), "folder for settings and the Python runtime")
	dataDir := flag.String("data", "", "folder with index files (default: <home>/data)")
	port := flag.Int("port", 0, "local port (0 picks a free one)")
	noWindow := flag.Bool("no-window", false, "do not open an app window")
	browserPath := flag.String("browser", "", "path to a Chrome or Edge executable")
	python := flag.String("python", os.Getenv("BOORUSCOPE_PYTHON"), "Python 3.9+ used to create the environment")
	torchIndex := flag.String("torch-index", os.Getenv("BOORUSCOPE_TORCH_INDEX"), "pip index for torch, or \"pypi\"")
	model := flag.String("model", encoder.DefaultModel, "CLIP model id or local folder")
	device := flag.String("device", "auto", "encoder device: auto, cpu, cuda, cuda:N")
	flag.Parse()

	logger := log.New(os.Stderr, "", log.LstdFlags)
	logf := logger.Printf

	absHome, err := filepath.Abs(*home)
	if err != nil {
		logger.Fatal(err)
	}
	if *dataDir == "" {
		*dataDir = filepath.Join(absHome, "data")
	}
	runtimeDir := filepath.Join(absHome, "runtime")
	for _, d := range []string{absHome, *dataDir, runtimeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			logger.Fatal(err)
		}
	}
	if os.Getenv("HF_HOME") == "" {
		os.Setenv("HF_HOME", filepath.Join(runtimeDir, "huggingface"))
	}

	enc := encoder.New(encoder.Options{
		Dir:        runtimeDir,
		Python:     *python,
		TorchIndex: *torchIndex,
		Model:      *model,
		Device:     *device,
		Logf:       logf,
	})
	lib := app.NewLibrary(*dataDir, logf)
	srv := app.New(app.Config{
		Library:      lib,
		Encoder:      enc,
		Thumbs:       fetch.NewThumbs(fetch.DefaultCacheBytes, fetch.DefaultConcurrency, nil),
		Web:          web.FS,
		SettingsPath: filepath.Join(absHome, "settings.json"),
		OpenURL:      browser.OpenDefault,
		Logf:         logf,
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		logger.Fatal(err)
	}
	srv.SetAddr(ln.Addr().String())
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal(err)
		}
	}()
	appURL := "http://" + ln.Addr().String() + "/"
	logf("BooruScope at %s (data: %s)", appURL, *dataDir)

	lib.Load(srv.Settings().Variant)
	enc.Start()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	windowClosed := make(chan struct{})
	if !*noWindow {
		if b, err := browser.Find(*browserPath); err == nil {
			cmd, err := browser.LaunchApp(b, appURL, filepath.Join(runtimeDir, "browser-profile"))
			if err != nil {
				logf("window: %v", err)
			} else {
				logf("window: %s (%s)", b.Kind, b.Path)
				go func() {
					cmd.Wait()
					close(windowClosed)
				}()
			}
		} else {
			logf("window: %v; opening the default browser instead", err)
			browser.OpenDefault(appURL)
		}
	}

	select {
	case <-stop:
	case <-windowClosed:
	}
	logf("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
	enc.Close()
	lib.Close()
}
