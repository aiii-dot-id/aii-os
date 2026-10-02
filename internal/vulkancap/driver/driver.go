package driver

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

type Capacity = vulkancap.Capacity

func Run(out, diagnostic io.Writer) int {
	c, err := measure()
	if err != nil {
		fmt.Fprintln(diagnostic, err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(c); err != nil {
		return 1
	}
	return 0
}
