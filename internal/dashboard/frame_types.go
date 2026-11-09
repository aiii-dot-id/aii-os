package dashboard

func frameType(ext string) (ctype string, embedOnly bool) {
	switch ext {
	case ".html":
		return "text/html; charset=utf-8", false
	case ".js":
		return "text/javascript; charset=utf-8", false
	case ".css":
		return "text/css; charset=utf-8", false
	case ".svg":
		return "image/svg+xml", true
	case ".png":
		return "image/png", true
	case ".md":
		return "text/markdown; charset=utf-8", true
	case ".woff2":
		return "font/woff2", true
	}
	return "", false
}

var compressed = map[string]bool{"image/png": true, "font/woff2": true}
