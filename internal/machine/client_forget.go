//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

const NeederForget = "aii unregister"

func Forget(ctx context.Context, dir, slot string, key *crypto.KeyPair, image string) error {
	path, err := LinkPath(dir)
	if err != nil {
		return err
	}
	conn, err := dialLink(ctx, path, image)
	if err != nil {
		return fmt.Errorf("the machine service does not answer at %s: %w", path, err)
	}
	defer conn.Close()
	return forgetOn(conn, key, slot)
}

func forgetOn(conn net.Conn, key *crypto.KeyPair, slot string) error {
	hello, err := handshakeOn(conn, key, slot, Self(), false)
	var ref *Refusal
	if errors.As(err, &ref) && ref.Reason == ReasonUnregistered {
		return nil
	}
	if err != nil {
		return err
	}
	if m := covers(hello.Speaks, NeederForget, []string{MsgForget}, hello.Build, Self()); m != nil {
		return m
	}
	_, err = call(conn, 2, MsgForget, nil)
	return err
}
