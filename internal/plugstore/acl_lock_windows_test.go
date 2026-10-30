//go:build windows

package plugstore

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestTwoEditsOfOneBlobsCapabilitiesLoseNeither(t *testing.T) {
	const k3, k4 = "3333333333333333", "4444444444444444"
	for _, tc := range []struct {
		name  string
		first func(blob, v2 string) error
		want  []string // view keys whose capability the blob must carry
	}{
		{"a grant beside a grant", func(blob, _ string) error { return GrantViewFile(blob, k4) }, []string{"1111111111111111", "2222222222222222", k3, k4}},
		{"a grant beside the collector's revoke", func(_, v2 string) error { return RevokeView(v2, "2222222222222222") }, []string{"1111111111111111", k3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, blob, _, v2 := twoViews(t)
			saved := editingDACL
			t.Cleanup(func() { editingDACL = saved })
			read, second := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			editingDACL = func(string) {
				if calls.Add(1) != 1 {
					return
				}
				close(read)
				select {
				case <-second:
				case <-time.After(2 * time.Second):
				}
			}
			firstDone := make(chan error, 1)
			go func() { firstDone <- tc.first(blob, v2) }()
			<-read
			err := GrantViewFile(blob, k3)
			close(second)
			if err != nil {
				t.Fatalf("the second edit: %v", err)
			}
			if err := <-firstDone; err != nil {
				t.Fatalf("the first edit: %v", err)
			}
			var want []string
			for _, k := range tc.want {
				want = append(want, capOf(t, Entry{Kind: DirViews, ID: k}))
			}
			slices.Sort(want)
			if got := capNames(t, blob); !slices.Equal(got, want) {
				t.Fatalf("the blob's capabilities after two edits at once: %v, want %v", got, want)
			}
		})
	}
}
