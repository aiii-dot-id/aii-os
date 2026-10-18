package audio

import (
	"encoding/binary"
	"math"
)

type Level struct {
	sum int64
	n   int64
}

func (l *Level) Add(pcm []byte) {
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		l.sum += v * v
		l.n++
	}
}

func (l Level) DBFS() float64 {
	if l.sum == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(math.Sqrt(float64(l.sum)/float64(l.n))/32768)
}

func RMSDBFS(pcm []byte) float64 {
	var l Level
	l.Add(pcm)
	return l.DBFS()
}
