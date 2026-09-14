package logsink

import "io"

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type tee struct {
	file   io.Writer
	stderr io.Writer
}

func (t tee) Write(p []byte) (int, error) {
	n, err := t.file.Write(p)
	if t.stderr != nil {
		_, _ = t.stderr.Write(p)
	}
	return n, err
}
