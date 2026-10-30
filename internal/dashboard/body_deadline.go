package dashboard

import (
	"io"
	"net/http"
	"sync"
	"time"
)

func (s *Server) boundStrangerBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength == 0 || r.Body == nil || r.Body == http.NoBody || tokenAuthorizedUnder(s.auth.Load(), r) {
			next.ServeHTTP(w, r)
			return
		}
		rc := http.NewResponseController(w)
		if err := rc.SetReadDeadline(time.Now().Add(s.strangerBodyTime())); err != nil {

			next.ServeHTTP(w, r)
			return
		}

		bounded := r.WithContext(r.Context())
		bounded.Body = &boundedBody{ReadCloser: r.Body, whole: func() { _ = rc.SetReadDeadline(time.Time{}) }}
		next.ServeHTTP(w, bounded)
	})
}

type boundedBody struct {
	io.ReadCloser
	once  sync.Once
	whole func()
}

func (b *boundedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(b.whole)
	}
	return n, err
}

func (b *boundedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.whole)
	return err
}
