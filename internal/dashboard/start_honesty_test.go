package dashboard

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartHealsOrRefusesAnUnusableKey(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"key missing": func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "dashboard.key")); err != nil {
				t.Fatal(err)
			}
		},
		"key is not this certificate's key": func(t *testing.T, dir string) {
			other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			der, err := x509.MarshalECPrivateKey(other)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "dashboard.key"),
				pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, corrupt := range cases {
		t.Run("heals: "+name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := ensureTLS(dir, "127.0.0.1"); err != nil {
				t.Fatal(err)
			}
			corrupt(t, dir)

			s := New("127.0.0.1", 0, nil)
			addr, err := s.Start(dir)
			if err != nil {
				t.Fatalf("a healable pair was refused: %v", err)
			}
			defer s.Shutdown(context.Background())
			resp, gerr := testClient.Get("https://" + addr + "/")
			if gerr != nil {
				t.Fatalf("the healed dashboard does not actually serve: %v", gerr)
			}
			resp.Body.Close()
		})
	}

	t.Run("refuses when healing cannot write", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := ensureTLS(dir, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		// Break the pair AND the heal: the leaf-cert path becomes a
		// directory, so the remint's atomic replace cannot land.
		// (A chmod would not do — the suite runs as root on the Linux test host, and
		// root writes through file modes.)
		leaf := filepath.Join(dir, "dashboard.crt")
		if err := os.Remove(leaf); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(leaf, 0o700); err != nil {
			t.Fatal(err)
		}
		s := New("127.0.0.1", 0, nil)
		if addr, err := s.Start(dir); err == nil {
			s.Shutdown(context.Background())
			t.Fatalf("Start reported success (%s) with an unusable, unhealable pair", addr)
		}
	})
}

// A wildcard bind has no single address, so the bound socket is not an
// origin. Printing it hands the operator "https://0.0.0.0:8180", which
// no browser can open.
func TestOriginIsNotTheWildcardBindAddress(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::"} {
		s := New(host, 0, nil)
		dir := t.TempDir()
		addr, err := s.Start(dir)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		got := s.Origin()
		s.Shutdown(context.Background())
		if strings.Contains(got, "0.0.0.0") || strings.Contains(got, "[::]") {
			t.Errorf("bound %s (%s): Origin() = %q — a wildcard bind is not a URL the operator can open", host, addr, got)
		}
	}
}
