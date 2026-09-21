package compressvfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
)

// .
// .
// .
// .
func EncodeImage(ctx context.Context, source io.ReaderAt, length int64, destination *os.File) error {
	info, err := destination.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 || length < 0 {
		return fmt.Errorf("encode: destination must be an empty regular file and length nonnegative")
	}
	physical := imageFile{destination}
	c, err := newContainer(physical)
	if err != nil {
		return err
	}
	defer c.close()
	buf := make([]byte, 64*1024)
	for offset := int64(0); offset < length; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(int64(len(buf)), length-offset)
		if _, err := source.ReadAt(buf[:n], offset); err != nil {
			return fmt.Errorf("read image at %d: %w", offset, err)
		}
		if err := c.write(buf[:n], offset); err != nil {
			return fmt.Errorf("encode image at %d: %w", offset, err)
		}
		offset += n
	}
	if length == 0 {
		if err := c.initialize(); err != nil {
			return err
		}
	}
	if err := c.sync(0); err != nil {
		return err
	}
	// .
	// .
	verified, err := newContainer(physical)
	if err != nil {
		return err
	}
	defer verified.close()
	if verified.length != length {
		return fmt.Errorf("encoded image size %d differs from source %d", verified.length, length)
	}
	decoded := make([]byte, len(buf))
	for offset := int64(0); offset < length; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(int64(len(buf)), length-offset)
		if _, err := source.ReadAt(buf[:n], offset); err != nil {
			return err
		}
		if err := verified.read(decoded[:n], offset); err != nil {
			return err
		}
		if !bytes.Equal(buf[:n], decoded[:n]) {
			return fmt.Errorf("encoded image differs from source at offset %d", offset)
		}
		offset += n
	}
	return nil
}

// .
// .
type imageFile struct{ file *os.File }

func (f imageFile) read(p []byte, off int64) error {
	return f.transfer(p, off, func(b []byte, o int64) error { _, err := f.file.ReadAt(b, o); return err })
}
func (f imageFile) write(p []byte, off int64) error {
	return f.transfer(p, off, func(b []byte, o int64) error { _, err := f.file.WriteAt(b, o); return err })
}
func (f imageFile) transfer(p []byte, off int64, fn func([]byte, int64) error) error {
	for len(p) > 0 {
		n := len(p)
		if off < pendingByte && int64(n) > pendingByte-off {
			n = int(pendingByte - off)
		}
		if err := fn(p[:n], physicalOffset(off)); err != nil {
			return err
		}
		off += int64(n)
		p = p[n:]
	}
	return nil
}
func (f imageFile) size() (int64, error) {
	info, err := f.file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size >= pendingByte+lockGap {
		size -= lockGap
	} else if size > pendingByte {
		size = pendingByte
	}
	return size, nil
}
func (f imageFile) truncate(size int64) error { return f.file.Truncate(physicalOffset(size)) }
func (f imageFile) sync(_ int32) error        { return f.file.Sync() }
