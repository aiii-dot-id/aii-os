//go:build (linux && !android) || (darwin && !ios)

package plugstore

func GrantRootAt(string, string) error { return nil }

func GrantView(string, string) error { return nil }

func GrantViewFile(string, string) error { return nil }

func RevokeView(string, string) error { return nil }

func CopyCapabilities(string, string) error { return nil }
