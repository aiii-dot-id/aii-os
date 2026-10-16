//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

const NeederPlace = "aii machine store"

func (c *Client) PlaceStore(ctx context.Context, dir string) (PlaceResult, error) {
	if err := c.Covers(NeederPlace, []string{MsgStorePlace}); err != nil {
		return PlaceResult{}, err
	}
	raw, err := c.request(ctx, MsgStorePlace, PlaceParams{Dir: dir})
	if err != nil {
		return PlaceResult{}, err
	}
	var res PlaceResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return PlaceResult{}, fmt.Errorf("the machine service's answer to store.place does not read: %w", err)
	}
	return res, nil
}

func Place(ctx context.Context, dir, slot string, key *crypto.KeyPair, image, store string) (PlaceResult, error) {
	path, err := LinkPath(dir)
	if err != nil {
		return PlaceResult{}, err
	}
	conn, err := dialLink(ctx, path, image)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("the machine service does not answer at %s: %w", path, err)
	}
	defer conn.Close()
	return placeOn(conn, key, slot, store)
}

func placeOn(conn net.Conn, key *crypto.KeyPair, slot, store string) (PlaceResult, error) {
	hello, err := handshakeOn(conn, key, slot, Self(), false)
	if err != nil {
		return PlaceResult{}, err
	}
	if m := covers(hello.Speaks, NeederPlace, []string{MsgStorePlace}, hello.Build, Self()); m != nil {
		return PlaceResult{}, m
	}
	raw, err := call(conn, 2, MsgStorePlace, PlaceParams{Dir: store})
	if err != nil {
		return PlaceResult{}, err
	}
	var res PlaceResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return PlaceResult{}, fmt.Errorf("the machine service's answer to store.place does not read: %w", err)
	}
	return res, nil
}
