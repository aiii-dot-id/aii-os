//go:build !darwin || ios

package supervisor

import "os/exec"

type procTree struct{}

func treeKeeperArgv() []string { return nil }

func startTree(cmd, keeper *exec.Cmd) (*procTree, error) { return nil, nil }

func (t *procTree) note(root int) {}

func (t *procTree) retire() error { return nil }
