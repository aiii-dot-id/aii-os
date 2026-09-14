// .
// .
// .
// .
// .
// .
// .
package actr

import (
	"math"
	"time"
)

const (
	// .
	Decay = 0.5
	// .
	// .
	MinAge = time.Hour
)

// .
// .
// .
func Activation(created time.Time, accesses []time.Time, now time.Time) float64 {
	var sum float64
	term := func(at time.Time) {
		if at.IsZero() {
			return
		}
		age := now.Sub(at)
		if age < MinAge {
			age = MinAge
		}
		sum += math.Pow(age.Hours()/24, -Decay)
	}
	term(created)
	for _, at := range accesses {
		term(at)
	}
	if sum <= 0 {
		return math.Inf(-1)
	}
	return math.Log(sum)
}

// .
// .
// .
// .
func Strength(created time.Time, accesses []time.Time, now time.Time) float64 {
	b := Activation(created, accesses, now)
	if math.IsInf(b, -1) {
		return 0.05
	}
	return 1 / (1 + math.Exp(-b))
}
