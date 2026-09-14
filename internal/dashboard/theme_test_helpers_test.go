package dashboard

import "strings"

// .
// .
func jsStringArray(xs []string) string {
	return "['" + strings.Join(xs, "','") + "']"
}
