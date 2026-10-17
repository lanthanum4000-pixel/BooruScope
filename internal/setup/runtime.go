package setup

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lanthanum4000-pixel/BooruScope/internal/encoder"
)

type RuntimeFunc func(ctx context.Context, dir string, report func(encoder.Status)) error

func PrepareRuntime(logf func(string, ...any)) RuntimeFunc {
	return func(ctx context.Context, dir string, report func(encoder.Status)) error {
		rt := filepath.Join(dir, "runtime")
		enc := encoder.New(encoder.Options{Dir: rt, Env: []string{"HF_HOME=" + encoder.HFHome(rt)}, Logf: logf})
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(400 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					report(enc.Status())
				}
			}
		}()
		err := enc.Verify(ctx)
		close(stop)
		report(enc.Status())
		enc.Close()
		return err
	}
}

func DetectPython(ctx context.Context) (string, error) {
	_, v, err := encoder.FindPython(ctx, "")
	return v, err
}
