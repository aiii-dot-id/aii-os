package genesis

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestARing5NoServerAnsweredIsTypedAsNotAnswered(t *testing.T) {
	fastBackoff(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dark := "http://" + l.Addr().String()
	l.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer refusing.Close()
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not a bundle"))
	}))
	defer garbled.Close()

	for _, tc := range []struct {
		name        string
		url         string
		notAnswered bool
	}{
		{"a server that is not there", dark, true},
		{"a server that answers only 503", failing.URL, true},
		{"a server that answers 404", refusing.URL, false},
		{"a server whose answer does not verify", garbled.URL, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient("", tc.url, "").WithPatience(2, 0).FetchRing5()
			if err == nil {
				t.Fatal("Ring 5 was admitted")
			}
			var na *NotAnsweredError
			if got := errors.As(err, &na); got != tc.notAnswered {
				t.Fatalf("not answered = %v, want %v: %v", got, tc.notAnswered, err)
			}
		})
	}
}
