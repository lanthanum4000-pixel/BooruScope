package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestThumbnailURL(t *testing.T) {
	got := ThumbnailURL("0123456789abcdef0123456789abcdef").String()
	want := "https://assets.yande.re/data/preview/01/23/0123456789abcdef0123456789abcdef.jpg"
	if got != want {
		t.Fatalf("%s", got)
	}
}

func TestThumbsCacheDedupAndEvict(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	f := func(ctx context.Context, u *url.URL, ref string) ([]byte, string, error) {
		calls.Add(1)
		if ref != "" {
			t.Errorf("referer %q", ref)
		}
		<-release
		return make([]byte, 400), "image/jpeg", nil
	}
	c := NewThumbs(1000, 2, f)
	u := func(n string) *url.URL { return &url.URL{Scheme: "https", Host: "img.test", Path: "/" + n} }
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), u("a")); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d fetches for one key", n)
	}
	c.Get(context.Background(), u("b"))
	c.Get(context.Background(), u("a"))
	c.Get(context.Background(), u("c"))
	if c.Used() > 1000 {
		t.Fatalf("used %d", c.Used())
	}
	before := calls.Load()
	c.Get(context.Background(), u("a"))
	if calls.Load() != before {
		t.Fatal("recently used entry was evicted")
	}
	c.Get(context.Background(), u("b"))
	if calls.Load() != before+1 {
		t.Fatal("least recently used entry was kept")
	}
}

func TestImageRejectsHTMLAndHotlinkRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>hotlink</html>"))
		case "/redirect":
			http.Redirect(w, r, "/hotlink.php", http.StatusFound)
		case "/img":
			if r.Header.Get("Referer") != "https://ref.test/" || !strings.HasPrefix(r.Header.Get("User-Agent"), "BooruScope/") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Write([]byte("\x89PNG\r\n\x1a\nrest"))
		}
	}))
	defer srv.Close()
	get := func(p, ref string) error {
		u, _ := url.Parse(srv.URL + p)
		_, _, err := Image(context.Background(), u, ref)
		return err
	}
	if err := get("/html", ""); err != ErrNotImage {
		t.Fatalf("html: %v", err)
	}
	if err := get("/redirect", ""); err != ErrNotImage {
		t.Fatalf("redirect: %v", err)
	}
	if err := get("/img", ""); !IsGone(err) {
		t.Fatalf("no referer: %v", err)
	}
	if err := get("/img", "https://ref.test/"); err != nil {
		t.Fatalf("with referer: %v", err)
	}
}

func TestSniffAVIF(t *testing.T) {
	if ct, ok := Sniff([]byte("\x00\x00\x00\x1cftypavif\x00\x00")); !ok || ct != "image/avif" {
		t.Fatalf("%s %v", ct, ok)
	}
	if _, ok := Sniff([]byte("plain text")); ok {
		t.Fatal("text accepted")
	}
}

func TestParseHTTP(t *testing.T) {
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://", "ftp://x/y"} {
		if _, err := ParseHTTP(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := ParseHTTP(" https://a.test/x.jpg "); err != nil {
		t.Fatal(err)
	}
}
