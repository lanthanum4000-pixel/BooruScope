//go:build !windows

package browser

func onScreen(x, y, w, h int) bool {
	return x+w > 0 && y >= 0
}
