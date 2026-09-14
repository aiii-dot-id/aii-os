package store

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/memory/carrd"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	sqlite "modernc.org/sqlite"
)

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
// .
// .
// .
func init() {
	sqlite.MustRegisterFunction("carrd_strength", &sqlite.FunctionImpl{
		NArgs: 3, Deterministic: true, Scalar: carrdStrengthSQL,
	})
	sqlite.MustRegisterFunction("trigram_similarity", &sqlite.FunctionImpl{
		NArgs: 2, Deterministic: true, Scalar: trigramSimilaritySQL,
	})
	// .
	// .
	sqlite.MustRegisterFunction("vec_cosine_q8", &sqlite.FunctionImpl{
		NArgs: 4, Deterministic: true, Scalar: vecCosineSQL,
	})
	sqlite.MustRegisterFunction("vec_l2_q8", &sqlite.FunctionImpl{
		NArgs: 4, Deterministic: true, Scalar: vecL2SQL,
	})
	sqlite.MustRegisterFunction("vec_hamming_q8", &sqlite.FunctionImpl{
		NArgs: 2, Deterministic: true, Scalar: vecHammingSQL,
	})
}

func vecCosineSQL(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	a, sa, b, sb, ok := vecPairArgs(args)
	if !ok {
		return nil, nil
	}
	return vec.Cosine(a, sa, b, sb)
}

func vecL2SQL(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	a, sa, b, sb, ok := vecPairArgs(args)
	if !ok {
		return nil, nil
	}
	return vec.L2(a, sa, b, sb)
}

func vecHammingSQL(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	a, ok := sqlBlob(args[0])
	if !ok {
		return nil, nil
	}
	b, ok := sqlBlob(args[1])
	if !ok {
		return nil, nil
	}
	n, err := vec.Hamming(a, b)
	if err != nil {
		return nil, err
	}
	return int64(n), nil
}

// .
func vecPairArgs(args []driver.Value) (a []byte, sa float64, b []byte, sb float64, ok bool) {
	if a, ok = sqlBlob(args[0]); !ok {
		return
	}
	if sa, ok = sqlFloat(args[1]); !ok {
		return
	}
	if b, ok = sqlBlob(args[2]); !ok {
		return
	}
	sb, ok = sqlFloat(args[3])
	return
}

func sqlBlob(v driver.Value) ([]byte, bool) {
	switch b := v.(type) {
	case []byte:
		return b, true
	case string:
		return []byte(b), true
	}
	return nil, false
}

func carrdStrengthSQL(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	name, ok := sqlText(args[0])
	if !ok {
		return nil, nil
	}
	age, ok := sqlFloat(args[1])
	if !ok {
		return nil, nil
	}
	accesses, ok := sqlInt(args[2])
	if !ok {
		return nil, nil
	}
	class, known := carrd.Parse(name)
	if !known {
		return nil, fmt.Errorf("carrd_strength: unknown durability class %q", name)
	}
	v, _ := carrd.Strength(class, age, accesses)
	return v, nil
}

func trigramSimilaritySQL(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	a, ok := sqlText(args[0])
	if !ok {
		return nil, nil
	}
	b, ok := sqlText(args[1])
	if !ok {
		return nil, nil
	}
	return trigram.Similarity(a, b), nil
}

// .
// .
// .
func sqlText(v driver.Value) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	case int64:
		return fmt.Sprint(x), true
	case float64:
		return strings.TrimSpace(fmt.Sprint(x)), true
	}
	return "", false
}

func sqlFloat(v driver.Value) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func sqlInt(v driver.Value) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	}
	return 0, false
}

// .
// .
// .
// .
// .
func probeMemoryFunctions(db *sql.DB) error {
	var sim, strength, cos float64
	if err := db.QueryRow("SELECT trigram_similarity('cat', 'cat'), carrd_strength('core', 0, 0), vec_cosine_q8(X'7F', 1.0/127, X'7F', 1.0/127)").Scan(&sim, &strength, &cos); err != nil {
		return fmt.Errorf("the memory functions are not registered on this build: %w", err)
	}
	if sim != 1 || strength != 1 || math.Abs(cos-1) > 1e-9 {
		return fmt.Errorf("the memory functions answer wrongly: similarity %v, strength %v, cosine %v (want 1, 1 and 1)", sim, strength, cos)
	}
	return nil
}
