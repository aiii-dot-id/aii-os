package store

import (
	"bytes"
	"fmt"
	"math"
	"strings"
)

type RuntimeConversion struct {
	Before int64
	After  int64
	Reason string
}

type CopyValueError struct{ Table string }

type CopyRowsError struct{ Cause error }

func (e *CopyRowsError) Error() string { return "carry rows: " + e.Cause.Error() }
func (e *CopyRowsError) Unwrap() error { return e.Cause }

func (e *CopyValueError) Error() string { return "schema copy changes a recorded value in " + e.Table }

func verifyCopiedValues(q dbi, source, target string, from, to []string) error {
	order := make([]string, len(from))
	for i := range order {
		order[i] = fmt.Sprintf("%d COLLATE BINARY", i+1)
	}
	read := func(table string, cols []string) string {
		return "SELECT " + strings.Join(cols, ",") + " FROM " + quoteIdentifier(table) + " ORDER BY " + strings.Join(order, ",")
	}
	a, err := q.Query(read(source, from))
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := q.Query(read(target, to))
	if err != nil {
		return err
	}
	defer b.Close()
	x, y := make([]any, len(from)), make([]any, len(to))
	xp, yp := make([]any, len(from)), make([]any, len(to))
	for i := range x {
		xp[i] = &x[i]
		yp[i] = &y[i]
	}
	for {
		an, bn := a.Next(), b.Next()
		if !an || !bn {
			if err := a.Err(); err != nil {
				return err
			}
			if err := b.Err(); err != nil {
				return err
			}
			if an != bn {
				return &CopyValueError{source}
			}
			return nil
		}
		if err := a.Scan(xp...); err != nil {
			return err
		}
		if err := b.Scan(yp...); err != nil {
			return err
		}
		for i := range x {
			if !sameCopiedValue(x[i], y[i]) {
				return &CopyValueError{source}
			}
		}
	}
}

func sameCopiedValue(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	case int64:
		switch y := b.(type) {
		case int64:
			return x == y
		case float64:
			return exactInteger(x, y)
		}
	case float64:
		switch y := b.(type) {
		case float64:
			return x == y
		case int64:
			return exactInteger(y, x)
		}
	}
	return false
}

func exactInteger(i int64, f float64) bool {

	return f >= -0x1p63 && f < 0x1p63 && math.Trunc(f) == f && int64(f) == i
}
