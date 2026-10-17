package encoder

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

const StateVerify State = "verify"

func TestImage() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 96, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 96; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 2), uint8(255 - x - y), 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func (e *Encoder) Verify(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancel = cancel
	e.st = Status{State: StatePython}
	e.mu.Unlock()
	if err := e.boot(ctx); err != nil {
		if ctx.Err() == nil {
			e.fail(err)
		}
		return err
	}
	e.mu.Lock()
	device := e.st.Device
	e.st = Status{State: StateVerify, Device: device}
	e.mu.Unlock()
	v, err := e.Encode(ctx, TestImage())
	if err != nil {
		e.fail(err)
		return err
	}
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(n)-1) > 1e-3 {
		err := fmt.Errorf("test embedding has norm %.4f", math.Sqrt(n))
		e.fail(err)
		return err
	}
	e.mu.Lock()
	e.st = Status{State: StateReady, Device: device}
	e.mu.Unlock()
	return nil
}
