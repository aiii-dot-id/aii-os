package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	noStart := fs.Bool("no-start", false, "Create the slot but do not register or start the service")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: aii init [--no-start]\n\nCreates the next identity slot under ~/%s and starts it.\n", install.Dir)
		fs.PrintDefaults()
	}
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(stderr, "init:", err)
		return 1
	}
	n, err := install.NextSlot(root)
	if err != nil {
		fmt.Fprintln(stderr, "init:", err)
		return 1
	}
	dir, err := install.Create(root, n)
	if err != nil {
		fmt.Fprintln(stderr, "init:", err)
		return 1
	}

	slot := install.SlotName(n)
	port := install.Port(n)

	fmt.Fprintf(stdout, "Created %s (dashboard port %d)\n", dir, port)

	if *noStart {
		fmt.Fprintf(stdout, "\nStart it with:\n  %s\n", install.StartCommand(slot, dir))
		fmt.Fprint(stdout, tokenWords(dir, "Once it runs, the"))
		return 0
	}

	started, notes, err := install.Register(slot)
	for _, note := range notes {
		fmt.Fprintln(stderr, "note:", note)
	}
	if err != nil {

		fmt.Fprintln(stderr, "init:", err)
		fmt.Fprintf(stderr, "\nThe slot is ready. Start it with:\n  %s\n", install.StartCommand(slot, dir))
		return 1
	}
	if !started {
		fmt.Fprintf(stdout, "\nStart it with:\n  %s\n", install.StartCommand(slot, dir))
		fmt.Fprint(stdout, tokenWords(dir, "Once it runs, the"))
		return 0
	}

	registerMachineService(stderr)

	fmt.Fprintf(stdout, "Started %s%s\n", install.Unit, slot)
	fmt.Fprintf(stdout, "\nOpen http://127.0.0.1:%d to meet them.\n", port)
	fmt.Fprint(stdout, tokenWords(dir, "The"))
	return 0
}

func tokenWords(dir, lead string) string {
	words := "\n" + lead + " dashboard asks for its access token, once in each browser; that browser then stays signed in.\n"
	said := false
	for _, sh := range tokenShells() {
		line, ok := sh.Command("aii", "dashboard-token", "-dir", dir)
		if !ok {
			continue
		}
		if !said {
			words += "Print the token with this, in " + sh.Name() + ":\n  " + line + "\n"
		} else {
			words += "or in " + sh.Name() + ":\n  " + line + "\n"
		}
		said = true
	}
	return words
}

func tokenShells() []install.Shell {
	if runtime.GOOS == "windows" {
		return []install.Shell{install.PowerShell, install.Cmd}
	}
	return []install.Shell{install.POSIX}
}

func runSlots(_ []string, stdout, stderr io.Writer) int {
	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(stderr, "slots:", err)
		return 1
	}
	ns, err := install.Slots(root)
	if err != nil {
		fmt.Fprintln(stderr, "slots:", err)
		return 1
	}
	if len(ns) == 0 {
		fmt.Fprintf(stdout, "No identity slots under %s — create one with: aii init\n", root)
		return 0
	}

	names := install.RefreshNames(root)
	for _, n := range ns {
		slot := install.SlotName(n)
		label := names[slot]
		if label == "" {
			label = "(unnamed — not yet born)"
		}
		port := install.ConfiguredPort(filepath.Join(root, slot), n)
		fmt.Fprintf(stdout, "  %-12s port %d  %s\n", slot, port, label)
	}
	return 0
}

func runRegister(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: aii register <slot>\n\nRegisters and starts the service for an existing slot.\n")
	}
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	slot := fs.Arg(0)

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(stderr, "register:", err)
		return 1
	}
	dir := filepath.Join(root, slot)
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(stderr, "register: %s does not exist — create it with: aii init\n", dir)
		return 1
	}

	started, notes, err := install.Register(slot)
	for _, note := range notes {
		fmt.Fprintln(stderr, "note:", note)
	}
	if err != nil {
		fmt.Fprintln(stderr, "register:", err)
		return 1
	}
	if !started {
		fmt.Fprintf(stderr, "\nNot started. Try:\n  %s\n", install.StartCommand(slot, dir))
		return 1
	}

	registerMachineService(stderr)
	recordWithMachine(slot, dir, stderr)
	fmt.Fprintf(stdout, "Started %s%s\n", install.Unit, slot)
	return 0
}

func runStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: aii stop <slot>\n\nAsks a running identity to shut down. It stays registered.\n")
	}
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	slot := fs.Arg(0)

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(stderr, "stop:", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(root, slot)); err != nil {
		fmt.Fprintf(stderr, "stop: inspect %s: %v\n", filepath.Join(root, slot), err)
		return 1
	}
	switch err := install.Stop(slot); {
	case err == nil:
		fmt.Fprintf(stdout, "Stopped %s%s\n", install.Unit, slot)
		return 0
	case isNotRunning(err):

		fmt.Fprintf(stdout, "%s%s is not running\n", install.Unit, slot)
		return 0
	default:
		fmt.Fprintln(stderr, "stop:", err)
		return 1
	}
}

func runUnregister(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unregister", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: aii unregister <slot>\n\nStops the identity and removes it from startup. The slot's data is left alone.\n")
	}
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	slot := fs.Arg(0)

	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(stderr, "unregister:", err)
		return 1
	}
	dir := filepath.Join(root, slot)
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(stderr, "unregister: %s does not exist\n", dir)
		return 1
	}
	if err := install.Unregister(slot); err != nil {
		fmt.Fprintln(stderr, "unregister:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Unregistered %s%s — its data is still at %s\n", install.Unit, slot, dir)

	forgetWithMachine(slot, dir, stderr)
	retireMachineIfLast(stdout, stderr)
	return 0
}

func parseVerbFlags(fs *flag.FlagSet, args []string) (code int, done bool) {
	err := fs.Parse(args)
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, flag.ErrHelp):
		return 0, true
	default:
		return 2, true
	}
}
