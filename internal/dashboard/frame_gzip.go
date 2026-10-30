package dashboard

import (
	"bytes"
	"compress/gzip"
	"strings"
	"sync"
)

var embeddedGzip sync.Map

type gzipped struct {
	once sync.Once
	b    []byte
}

var (
	gzipMu     sync.Mutex
	gzipWriter *gzip.Writer
)

func gzipOf(p string, raw []byte) []byte {
	v, ok := embeddedGzip.Load(p)
	if !ok {
		v, _ = embeddedGzip.LoadOrStore(p, new(gzipped))
	}
	g := v.(*gzipped)
	g.once.Do(func() {
		gzipMu.Lock()
		defer gzipMu.Unlock()

		var buf bytes.Buffer
		if gzipWriter == nil {
			gzipWriter, _ = gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
		} else {
			gzipWriter.Reset(&buf)
		}
		gzipWriter.Write(raw)
		gzipWriter.Close()
		if buf.Len() < len(raw) {
			g.b = bytes.Clone(buf.Bytes())
		}
	})
	return g.b
}

func negotiateGzip(fields []string, offered bool) (useGzip, acceptable bool) {
	var gz, id, star weight
	for _, el := range strings.Split(strings.Join(fields, ","), ",") {
		coding, params, hasParams := strings.Cut(el, ";")
		var w *weight
		switch strings.ToLower(strings.TrimSpace(coding)) {
		case "gzip", "x-gzip":
			w = &gz
		case "identity":
			w = &id
		case "*":
			w = &star
		default:
			continue
		}
		q := 1000
		if hasParams {
			q = qvalue(params)
		}
		if !w.named || q < w.q {
			w.q, w.named = q, true
		}
	}
	of := func(w weight, unlisted int) int {
		if w.named {
			return w.q
		}
		if star.named {
			return star.q
		}
		return unlisted
	}
	qi, qg := of(id, 1000), 0
	if offered {
		qg = of(gz, 0)
	}
	if qg > 0 && qg >= qi {
		return true, true
	}
	return false, qi > 0
}

type weight struct {
	q     int
	named bool
}

func qvalue(params string) int {
	v, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(params)), "q=")
	whole, frac, _ := strings.Cut(v, ".")
	if !ok || (whole != "0" && whole != "1") || len(frac) > 3 {
		return -1
	}
	q := 0
	if whole == "1" {
		q = 1000
	}
	for i, scale := 0, 100; i < len(frac); i, scale = i+1, scale/10 {
		d := frac[i]
		if d < '0' || d > '9' || (whole == "1" && d != '0') {
			return -1
		}
		q += int(d-'0') * scale
	}
	return q
}
