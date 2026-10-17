package fetch

import (
	"container/list"
	"context"
	"errors"
	"net/url"
	"sync"
)

const (
	DefaultCacheBytes  = 256 << 20
	DefaultConcurrency = 6
)

type Thumb struct {
	Data        []byte
	ContentType string
}

type ThumbFetcher func(ctx context.Context, u *url.URL, referer string) ([]byte, string, error)

type Thumbs struct {
	fetch    ThumbFetcher
	sem      chan struct{}
	maxBytes int

	mu       sync.Mutex
	used     int
	order    *list.List
	items    map[string]*list.Element
	inflight map[string]*call
}

type entry struct {
	key string
	t   Thumb
}

type call struct {
	done chan struct{}
	t    Thumb
	err  error
}

func NewThumbs(maxBytes, concurrency int, f ThumbFetcher) *Thumbs {
	if f == nil {
		f = Image
	}
	return &Thumbs{
		fetch:    f,
		sem:      make(chan struct{}, concurrency),
		maxBytes: maxBytes,
		order:    list.New(),
		items:    map[string]*list.Element{},
		inflight: map[string]*call{},
	}
}

func ThumbnailURL(md5hex string) *url.URL {
	return &url.URL{Scheme: "https", Host: "assets.yande.re", Path: "/data/preview/" + md5hex[0:2] + "/" + md5hex[2:4] + "/" + md5hex + ".jpg"}
}

func (c *Thumbs) Get(ctx context.Context, u *url.URL) (Thumb, error) {
	key := u.String()
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		t := el.Value.(*entry).t
		c.mu.Unlock()
		return t, nil
	}
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-cl.done:
			return cl.t, cl.err
		case <-ctx.Done():
			return Thumb{}, ctx.Err()
		}
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	cl.t, cl.err = c.load(ctx, u)

	c.mu.Lock()
	delete(c.inflight, key)
	if cl.err == nil {
		c.put(key, cl.t)
	}
	c.mu.Unlock()
	close(cl.done)
	return cl.t, cl.err
}

func (c *Thumbs) load(ctx context.Context, u *url.URL) (Thumb, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return Thumb{}, ctx.Err()
	}
	defer func() { <-c.sem }()
	data, ct, err := c.fetch(context.WithoutCancel(ctx), u, "")
	if err != nil {
		return Thumb{}, err
	}
	return Thumb{Data: data, ContentType: ct}, nil
}

func (c *Thumbs) put(key string, t Thumb) {
	n := len(t.Data)
	if n > c.maxBytes {
		return
	}
	c.items[key] = c.order.PushFront(&entry{key: key, t: t})
	c.used += n
	for c.used > c.maxBytes {
		el := c.order.Back()
		e := el.Value.(*entry)
		c.order.Remove(el)
		delete(c.items, e.key)
		c.used -= len(e.t.Data)
	}
}

func (c *Thumbs) Used() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

func IsGone(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && (se.Code == 403 || se.Code == 404 || se.Code == 410)
}
