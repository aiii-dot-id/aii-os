package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestResamplerPreservesIndependentChannels(t *testing.T) {
	for _, rate := range []int{16000, 44100, 48000} {
		for _, split := range []int{1, 127, 480, 2048} {
			r := NewResampler(Format{Rate: rate, Channels: 2}, Format{Rate: 16000, Channels: 2})
			left := NewResampler(Format{Rate: rate, Channels: 1}, Format{Rate: 16000, Channels: 1})
			right := NewResampler(Format{Rate: rate, Channels: 1}, Format{Rate: 16000, Channels: 1})
			var got, want []byte
			check := func(a, b, c []byte) {
				got = append(got, a...)
				if len(b) != len(c) {
					t.Fatal("channel clocks differ")
				}
				for i := 0; i < len(b); i += 2 {
					want = append(want, b[i:i+2]...)
					want = append(want, c[i:i+2]...)
				}
			}
			for start := 0; start < 4903; {
				n := min(split, 4903-start)
				l := make([]byte, n*2)
				q := make([]byte, n*2)
				stereo := make([]byte, n*4)
				for i := 0; i < n; i++ {
					binary.LittleEndian.PutUint16(l[i*2:], uint16(int16((start+i)*17%23000-11000)))
					binary.LittleEndian.PutUint16(q[i*2:], uint16(int16((start+i)*43%19000-9000)))
					copy(stereo[i*4:], l[i*2:i*2+2])
					copy(stereo[i*4+2:], q[i*2:i*2+2])
				}
				r.Feed(stereo)
				left.Feed(l)
				right.Feed(q)
				check(r.Take(), left.Take(), right.Take())
				start += n
			}
			check(r.Finish(), left.Finish(), right.Finish())
			if !bytes.Equal(got, want) || r.Position() != r.TargetPos(4903) {
				t.Fatalf("independent samples/tail lost rate=%d split=%d", rate, split)
			}
			r.Reset(0)
			if r.Position() != 0 {
				t.Fatal("reset retained timeline")
			}
		}
	}
}
