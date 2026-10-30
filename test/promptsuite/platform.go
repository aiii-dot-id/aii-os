package main

import (
	"fmt"
	"net"
	"net/http"

	"github.com/aiii-dot-id/aii-os/internal/genesis/genesislive"
)

type platform struct {
	url   string
	close func() error
}

func servePlatform() (*platform, error) {
	live, err := genesislive.Fetch()
	if err != nil {
		return nil, fmt.Errorf("the platform's founding artifacts come from the live fetch and nowhere else, and it could not be had: %w", err)
	}
	mux := http.NewServeMux()
	serve := func(path string, body []byte, token string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			if token != "" {
				w.Header().Set("X-Genesis-Token", token)
			}
			_, _ = w.Write(body)
		})
	}
	// The paths the genesis client asks (internal/genesis/client.go,
	// fetchBundle). The bootstrap packet is the one the live fetch was
	// served for its own token; the token a host presents for it here
	// is the same one, handed back with Ring 0.
	serve("/genesis/bundle", live.Ring0, live.Token)
	serve("/v1/ring5/pubkey.bundle", live.Ring5PubkeyBundle, "")
	serve("/v1/ring5/bundle", live.Ring5Bundle, "")
	serve("/v1/ring5/manifest", live.Ring5Manifest, "")
	serve("/bootstrap/pubkey.bundle", live.BootstrapKey, "")
	serve("/bootstrap", live.BootstrapPacket, "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return &platform{url: "http://" + ln.Addr().String(), close: srv.Close}, nil
}
