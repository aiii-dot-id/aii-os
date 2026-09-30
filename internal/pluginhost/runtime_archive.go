package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func extractRuntimeArchive(ctx context.Context, r io.Reader, d *RuntimeDecl, limits packagefmt.TreeLimits, dest string) (*packagefmt.TreeReport, bool, error) {
	input := &runtimeInput{Reader: acquisitionReader{ctx, r}}
	limited := &io.LimitedReader{R: input, N: d.Size}
	digest := sha256.New()
	stream := io.TeeReader(limited, digest)
	rep, err := packagefmt.ExtractTree(stream, "sha256:"+d.InventorySHA256, limits, dest)
	ceiling := limits.MaxCompressedBytes
	if ceiling <= 0 {
		ceiling = packagefmt.DefaultTreeLimits.MaxCompressedBytes
	}
	verified := false
	if d.Size > 0 && d.Size <= ceiling {
		_, readErr := io.Copy(io.Discard, stream)
		var extra [1]byte
		n, endErr := input.Read(extra[:])
		verified = limited.N == 0 && readErr == nil && n == 0 && endErr == io.EOF && input.fault == nil && hex.EncodeToString(digest.Sum(nil)) == d.SHA256
		err = errors.Join(err, readErr, input.fault)
	}
	if err == nil && !verified {
		err = errors.New("runtime archive bytes changed during extraction or exceed the compressed-size ceiling")
	}
	return rep, verified, err
}

type runtimeInput struct {
	io.Reader
	fault error
}

func (r *runtimeInput) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && err != io.EOF {
		r.fault = err
	}
	return n, err
}

func runtimeContentError(err error) bool {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return false
	}
	var formatErr *packagefmt.Error
	return errors.As(err, &formatErr) && formatErr.Reason != packagefmt.ReasonCeilingExceeded
}
