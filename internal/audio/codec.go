package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

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
// .
// .
// .

const (
	headerBytes = 28
	// .
	// .
	MaxFramePayload = 64 * 1024
)

var magic = [4]byte{'A', 'U', 'D', '1'}

var (
	ErrBadMagic    = errors.New("audio: frame does not start with the magic")
	ErrBadKind     = errors.New("audio: frame kind is not pcm, discontinuity or end")
	ErrFrameTooBig = errors.New("audio: frame payload over the ceiling")
)

// .
func WriteFrame(w io.Writer, fr Frame) error {
	if fr.Kind != KindPCM && fr.Kind != KindDiscontinuity && fr.Kind != KindEnd {
		return ErrBadKind
	}
	if fr.Kind != KindPCM && len(fr.PCM) != 0 {
		return fmt.Errorf("audio: a %d frame carries no payload", fr.Kind)
	}
	if len(fr.PCM) > MaxFramePayload {
		return ErrFrameTooBig
	}
	var h [headerBytes]byte
	copy(h[0:4], magic[:])
	h[4] = byte(fr.Kind)
	binary.BigEndian.PutUint32(h[8:12], fr.Stream)
	binary.BigEndian.PutUint32(h[12:16], fr.Seq)
	binary.BigEndian.PutUint64(h[16:24], uint64(fr.Start))
	binary.BigEndian.PutUint32(h[24:28], uint32(len(fr.PCM)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	if len(fr.PCM) > 0 {
		if _, err := w.Write(fr.PCM); err != nil {
			return err
		}
	}
	return nil
}

// .
// .
func ReadFrame(r io.Reader) (Frame, error) {
	var h [headerBytes]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	if [4]byte(h[0:4]) != magic {
		return Frame{}, ErrBadMagic
	}
	fr := Frame{Kind: Kind(h[4]), Stream: binary.BigEndian.Uint32(h[8:12]), Seq: binary.BigEndian.Uint32(h[12:16]), Start: int64(binary.BigEndian.Uint64(h[16:24]))}
	n := binary.BigEndian.Uint32(h[24:28])
	if fr.Kind != KindPCM && fr.Kind != KindDiscontinuity && fr.Kind != KindEnd {
		return Frame{}, ErrBadKind
	}
	if n > MaxFramePayload {
		return Frame{}, ErrFrameTooBig
	}
	if fr.Kind != KindPCM && n != 0 {
		return Frame{}, fmt.Errorf("audio: a %d frame carries no payload", fr.Kind)
	}
	if n > 0 {
		fr.PCM = make([]byte, n)
		if _, err := io.ReadFull(r, fr.PCM); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return Frame{}, err
		}
	}
	return fr, nil
}

// .
// .
// .
type PipeChannel struct {
	In  io.WriteCloser
	Out io.ReadCloser
}

func (c *PipeChannel) WriteInput(fr Frame) error  { return WriteFrame(c.In, fr) }
func (c *PipeChannel) ReadOutput() (Frame, error) { return ReadFrame(c.Out) }
func (c *PipeChannel) Close() error {
	err := c.In.Close()
	if oerr := c.Out.Close(); err == nil {
		err = oerr
	}
	return err
}
