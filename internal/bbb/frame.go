// Package bbb implements the BBB v2 wire layer for the Go plugin host.
//
// BBB v2 is the ABI of the plugin plane — adopted from the C stack,
// evolved never forked (docs/PLUGIN_FRAMEWORK.md §4). This file is
// build-order step 1's seed: the frame codec ONLY. No envelope, no
// dispatch, no client — those land with steps 3-5 against the audited
// contract in spec/bbb/BBB_V2_AUDIT.md and the vectors in
// spec/bbb/vectors/, which internal/bbb/frame_test.go executes.
package bbb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// FrameHeaderBytes is the fixed frame header size: a 4-byte
	// big-endian unsigned payload length, header excluded.
	// (AUDIT §2: sdk bbb_transport.h:27, posix.c:182-187,213-214;
	// daemon sev_rpc.h:129, rpc.c:3526-3527; sdk/go bbb.go:453-486.)
	FrameHeaderBytes = 4

	// MaxControlFrameBytes is the plugin-side frame limit, both
	// directions: 1 MiB. Adopted verbatim from
	// SEV_TRANSPORT_DEFAULT_MAX_FRAME_BYTES (sdk bbb_transport.h:30)
	// and the SDK Go client's MaxControlFrameBytes (bbb.go:41).
	MaxControlFrameBytes = 1 << 20

	// MaxServerFrameBytes is the daemon-inbound frame limit: 16 MiB
	// (SEV_RPC_MAX_FRAME_SIZE, daemon sev_rpc.h:130). The limits are
	// deliberately asymmetric in the C stack (AUDIT §2.1, finding
	// F-11); a host may be stricter with children it spawns — the C
	// daemon itself caps its outbound plugin.invoke frames at the
	// 1 MiB client bound — so callers choose the bound by role
	// rather than this package guessing one.
	MaxServerFrameBytes = 16 << 20
)

var (
	// ErrFrameTooLarge reports a payload above the caller's frame
	// limit — declared (decode) or actual (encode). On decode the
	// payload is left unread, exactly like the C transport
	// (posix.c:215-217): a stream that declared an oversize frame
	// cannot be resynchronized and must be closed (the daemon
	// disconnects such peers outright, rpc.c:3529-3534).
	ErrFrameTooLarge = errors.New("bbb: frame exceeds limit")

	// ErrEmptyPayload reports an attempt to WRITE a zero-length
	// frame. The C transport refuses empty sends (posix.c:177) and
	// no protocol layer produces one; reading an empty frame is NOT
	// a framing error — the JSON layer above rejects it (AUDIT
	// §2.3).
	ErrEmptyPayload = errors.New("bbb: refusing to write empty frame")
)

// WriteFrame writes one frame: 4-byte big-endian length, then payload.
// The limit is checked before any byte is written so an oversize
// payload never poisons the stream (posix.c:179-180; the SDK pins
// "fails before write", bbb_test.go:428-434).
func WriteFrame(w io.Writer, payload []byte, maxFrameBytes int) error {
	if err := checkLimit(maxFrameBytes); err != nil {
		return err
	}
	if len(payload) == 0 {
		return ErrEmptyPayload
	}
	if len(payload) > maxFrameBytes {
		return ErrFrameTooLarge
	}

	var header [FrameHeaderBytes]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads one frame and returns its payload.
//
// Errors mirror the audited C behavior (AUDIT §2.2-2.3):
//   - clean EOF before any header byte returns io.EOF (no frame);
//   - EOF mid-header or mid-payload returns io.ErrUnexpectedEOF —
//     framing has no magic and no resync, a desynced stream is dead;
//   - a declared length above maxFrameBytes returns ErrFrameTooLarge
//     with the payload unread;
//   - a declared length of zero returns an empty payload and nil
//     error: legal at this layer only, the JSON layer rejects it.
func ReadFrame(r io.Reader, maxFrameBytes int) ([]byte, error) {
	if err := checkLimit(maxFrameBytes); err != nil {
		return nil, err
	}

	var header [FrameHeaderBytes]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		// io.ReadFull already maps a partial header to
		// io.ErrUnexpectedEOF and a clean boundary to io.EOF.
		return nil, err
	}
	// Compare in uint64 so a hostile 0xffffffff declaration cannot
	// overflow int on 32-bit builds before the limit check.
	declared := uint64(binary.BigEndian.Uint32(header[:]))
	if declared > uint64(maxFrameBytes) {
		return nil, ErrFrameTooLarge
	}

	payload := make([]byte, int(declared))
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			// A truncated payload is never a clean EOF: the
			// header promised bytes that did not arrive.
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return payload, nil
}

// checkLimit rejects nonsensical limits instead of defaulting one:
// the caller must name the boundary role (MaxControlFrameBytes for a
// plugin-side endpoint, MaxServerFrameBytes for daemon-inbound) — a
// silent default here would hide which side of the asymmetric
// contract (F-11) the caller believes it is on.
func checkLimit(maxFrameBytes int) error {
	if maxFrameBytes <= 0 {
		return fmt.Errorf("bbb: max frame bytes must be positive, got %d", maxFrameBytes)
	}
	return nil
}
