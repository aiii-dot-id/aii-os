package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// .
// .
// .
// .
// .
// .
// .
// .
func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	noStart := fs.Bool("no-start", false, "Create the slot but do not register or start the service")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: aii init [--no-start]\n\nCreates the next identity slot under ~/%s and starts it.\n", install.Dir)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		return 1
	}
	n, err := install.NextSlot(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		return 1
	}
	dir, err := install.Create(root, n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		return 1
	}

	slot := install.SlotName(n)
	port := install.Port(n)

	fmt.Printf("Created %s (dashboard port %d)\n", dir, port)

	if *noStart {
		fmt.Printf("\nStart it with:\n  %s\n", install.StartCommand(slot, dir))
		return 0
	}

	started, notes, err := install.Register(slot)
	for _, note := range notes {
		fmt.Fprintln(os.Stderr, "note:", note)
	}
	if err != nil {
		// .
		// .
		fmt.Fprintln(os.Stderr, "init:", err)
		fmt.Fprintf(os.Stderr, "\nThe slot is ready. Start it with:\n  %s\n", install.StartCommand(slot, dir))
		return 1
	}
	if !started {
		fmt.Printf("\nStart it with:\n  %s\n", install.StartCommand(slot, dir))
		return 0
	}

	fmt.Printf("Started %s%s\n", install.Unit, slot)
	fmt.Printf("\nOpen http://127.0.0.1:%d to meet them.\n", port)
	return 0
}

// .
// .
func runSlots() int {
	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "slots:", err)
		return 1
	}
	ns, err := install.Slots(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "slots:", err)
		return 1
	}
	if len(ns) == 0 {
		fmt.Printf("No identity slots under %s — create one with: aii init\n", root)
		return 0
	}
	// .
	// .
	// .
	names := install.RefreshNames(root)
	for _, n := range ns {
		slot := install.SlotName(n)
		label := names[slot]
		if label == "" {
			label = "(unnamed — not yet born)"
		}
		port := install.ConfiguredPort(filepath.Join(root, slot), n)
		fmt.Printf("  %-12s port %d  %s\n", slot, port, label)
	}
	return 0
}

// .
// .
// .
// .
// .
func runRegister(args []string) int {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: aii register <slot>\n\nRegisters and starts the service for an existing slot.\n")
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	slot := fs.Arg(0)

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "register:", err)
		return 1
	}
	dir := filepath.Join(root, slot)
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(os.Stderr, "register: %s does not exist — create it with: aii init\n", dir)
		return 1
	}

	started, notes, err := install.Register(slot)
	for _, note := range notes {
		fmt.Fprintln(os.Stderr, "note:", note)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "register:", err)
		return 1
	}
	if !started {
		fmt.Fprintf(os.Stderr, "\nNot started. Try:\n  %s\n", install.StartCommand(slot, dir))
		return 1
	}
	fmt.Printf("Started %s%s\n", install.Unit, slot)
	return 0
}

// .
// .
// .
// .
// .
// .
// .
func runStop(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: aii stop <slot>\n\nAsks a running identity to shut down. It stays registered.\n")
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	slot := fs.Arg(0)

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stop:", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(root, slot)); err != nil {
		fmt.Fprintf(os.Stderr, "stop: %s does not exist\n", filepath.Join(root, slot))
		return 1
	}
	switch err := install.Stop(slot); {
	case err == nil:
		fmt.Printf("Stopped %s%s\n", install.Unit, slot)
		return 0
	case isNotRunning(err):
		// .
		fmt.Printf("%s%s is not running\n", install.Unit, slot)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "stop:", err)
		return 1
	}
}

// .
// .
// .
func runUnregister(args []string) int {
	fs := flag.NewFlagSet("unregister", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: aii unregister <slot>\n\nStops the identity and removes it from startup. The slot's data is left alone.\n")
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	slot := fs.Arg(0)

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "unregister:", err)
		return 1
	}
	dir := filepath.Join(root, slot)
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(os.Stderr, "unregister: %s does not exist\n", dir)
		return 1
	}
	if err := install.Unregister(slot); err != nil {
		fmt.Fprintln(os.Stderr, "unregister:", err)
		return 1
	}
	fmt.Printf("Unregistered %s%s — its data is still at %s\n", install.Unit, slot, dir)
	return 0
}
