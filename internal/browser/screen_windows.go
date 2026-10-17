package browser

import (
	"syscall"
	"unsafe"
)

var procMonitorFromRect = syscall.NewLazyDLL("user32.dll").NewProc("MonitorFromRect")

type rect struct{ left, top, right, bottom int32 }

func onScreen(x, y, w, h int) bool {
	if procMonitorFromRect.Find() != nil {
		return true
	}
	inset := min(80, w/4)
	r := rect{int32(x + inset), int32(y), int32(x + w - inset), int32(y + min(32, h))}
	m, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), 0)
	return m != 0
}
