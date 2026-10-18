package pluginhost

import (
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

func readDeclaration(raw []byte, out any, shape, trailing string) error {
	err := canonicaljson.DecodeStrict(raw, out)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, canonicaljson.ErrTrailingContent):
		return errors.New(trailing)
	}
	return fmt.Errorf("not %s: %v", shape, err)
}
