package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	UserAgent    = "BooruScope/0.1 (+https://github.com/lanthanum4000-pixel/BooruScope)"
	MaxImageSize = 64 << 20
)

var (
	ErrNotImage = errors.New("fetch: response is not an image")
	ErrTooLarge = errors.New("fetch: image too large")
	ErrBadURL   = errors.New("fetch: unsupported URL")
)

type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("fetch: HTTP %d", e.Code) }

var client = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("fetch: too many redirects")
		}
		if strings.HasSuffix(req.URL.Path, "/hotlink.php") {
			return ErrNotImage
		}
		return nil
	},
}

func OriginOf(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

func ParseHTTP(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, ErrBadURL
	}
	return u, nil
}

func Image(ctx context.Context, u *url.URL, referer string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.5")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrNotImage) {
			return nil, "", ErrNotImage
		}
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &StatusError{Code: resp.StatusCode}
	}
	if resp.ContentLength > MaxImageSize {
		return nil, "", ErrTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > MaxImageSize {
		return nil, "", ErrTooLarge
	}
	ct, ok := Sniff(body)
	if !ok {
		return nil, "", ErrNotImage
	}
	return body, ct, nil
}

func Sniff(b []byte) (string, bool) {
	if len(b) >= 12 && string(b[4:8]) == "ftyp" {
		switch string(b[8:12]) {
		case "avif", "avis":
			return "image/avif", true
		}
	}
	ct := http.DetectContentType(b)
	if strings.HasPrefix(ct, "image/") {
		return ct, true
	}
	return ct, false
}
