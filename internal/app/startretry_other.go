//go:build !windows

package app

import "syscall"

func passesWithTimeHere(syscall.Errno) bool { return false }

const defaultEndedStartsRestart = true
