// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package carrd

import (
	"math"
	"strings"
)

// .
type Class string

const (
	// .
	Constitutional Class = "constitutional"
	// .
	Core Class = "core"
	// .
	// .
	Standard Class = "standard"
	// .
	Operational Class = "operational"
	// .
	// .
	Ephemeral Class = "ephemeral"
)

// .
// .
type Params struct {
	HalfLifeDays float64
	Floor        float64
}

// .
var table = map[Class]Params{
	Constitutional: {HalfLifeDays: 36500, Floor: 0.50},
	Core:           {HalfLifeDays: 365, Floor: 0.40},
	Standard:       {HalfLifeDays: 82, Floor: 0.15},
	Operational:    {HalfLifeDays: 21, Floor: 0.05},
	Ephemeral:      {HalfLifeDays: 5, Floor: 0.02},
}

// .
var order = []Class{Constitutional, Core, Standard, Operational, Ephemeral}

// .
func Classes() []Class {
	out := make([]Class, len(order))
	copy(out, order)
	return out
}

// .
// .
func Parse(name string) (Class, bool) {
	c := Class(strings.ToLower(strings.TrimSpace(name)))
	_, ok := table[c]
	return c, ok
}

// .
// .
func (c Class) Params() (Params, bool) {
	p, ok := table[c]
	return p, ok
}

// .
func (c Class) String() string { return string(c) }

// .
// .
// .
// .
func EffectiveAge(ageDays float64, accesses int64) float64 {
	if ageDays < 0 || math.IsNaN(ageDays) {
		ageDays = 0
	}
	if accesses < 0 {
		accesses = 0
	}
	return ageDays / (1 + 0.5*math.Log(1+float64(accesses)))
}

// .
// .
// .
func Strength(c Class, ageDays float64, accesses int64) (float64, bool) {
	p, ok := table[c]
	if !ok {
		return 0, false
	}
	decayed := math.Pow(0.5, EffectiveAge(ageDays, accesses)/p.HalfLifeDays)
	return math.Max(p.Floor, decayed), true
}

// .
// .
// .
func Final(rrf, importance, strength, locality float64) float64 {
	return rrf * importance * strength * locality
}
