package pluginfacility

import "fmt"

// .
// .
// .
// .
// .

// .
type Availability struct {
	// .
	HostKnown     bool
	HostTotal     int64
	HostAvailable int64
	// .
	// .
	Devices map[string]DeviceAvailability
}

// .
type DeviceAvailability struct {
	Known            bool
	Total, Available int64
}

// .
type Capacity interface {
	Measure() Availability
}

// .
// .
type UnknownCapacity struct{}

// .
func (UnknownCapacity) Measure() Availability { return Availability{} }

// .
type AdmissionPolicy struct {
	// .
	// .
	ReserveBytes int64
	// .
	// .
	// .
	BudgetBytes int64
	// .
	// .
	MaxConcurrentStarts int
}

// .
// .
// .
// .
const DefaultNativeEnvelopeBytes int64 = 1 << 30

// .
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
