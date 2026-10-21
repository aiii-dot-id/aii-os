package tools

import (
	"context"
	"io"
)

func (r *Registry) ReadOwn(ctx context.Context, path string, max int) (data []byte, more bool, err error) {
	look := r.policy.Look()
	ctx = context.WithValue(ctx, callLook{}, look)
	resolved := r.rooted(path)
	if _, _, err := r.admitOwn(look, "read", resolved); err != nil {
		return nil, false, err
	}
	if r.afterAdmit != nil {
		r.afterAdmit()
	}
	f, st, err := openRegular(resolved)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if r.afterOpen != nil {
		r.afterOpen()
	}
	if err := r.heldRead(ctx, resolved, st, f); err != nil {
		return nil, false, err
	}
	in := io.LimitReader(f, int64(max)+1)
	buf := make([]byte, 0, min(max+1, 64<<10))
	chunk := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		n, err := in.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, err
		}
	}
	if len(buf) > max {
		return buf[:max], true, nil
	}
	return buf, false, nil
}
