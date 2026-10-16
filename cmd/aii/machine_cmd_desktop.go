//go:build (linux && !android) || (darwin && !ios) || windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/machine"
)

const machineUsage = `Usage:
  aii machine [-dir DIR]          run the account's machine service
  aii machine register            register this program as the account's machine service
  aii machine store <dir> <slot>  move the account's plugin store to <dir>

One machine service runs per OS account, as you, beside your identities. Its
registration names the account's installed program; an account with no
installed program runs the build its operator names with "aii machine register".
-dir runs a development service with a directory of its own: it is never
registered, and only a slot whose config names that directory (machine.dir)
links to it.
`

func runMachine(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "register" {
		return runMachineRegister(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "store" {
		return runMachineStore(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("machine", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, machineUsage) }
	dir := fs.String("dir", "", "a development service's own directory (default: the account's ~/.aii/machine)")
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	opts := machine.Options{Dir: *dir, StartupSlots: startupSlotDirs}
	if opts.Dir == "" {
		d, err := install.MachineDir()
		if err != nil {
			fmt.Fprintln(stderr, "machine:", err)
			return 1
		}
		opts.Dir = d

		if exe, err := runningProgram(); err == nil {
			opts.Program = exe
			opts.Successor = machine.AccountSuccessor(exe)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch err := machine.NewServer(opts).Serve(ctx); {
	case err == nil, errors.Is(err, machine.ErrHandover):
		return 0
	case errors.Is(err, machine.ErrHeld):

		fmt.Fprintln(stderr, "machine:", err)
		return 0
	default:
		fmt.Fprintln(stderr, "machine:", err)

		if rerr := machine.RecordFailure(opts.Dir, err); rerr != nil {
			fmt.Fprintln(stderr, "machine: the failure was not recorded:", rerr)
		}
		return 1
	}
}

func runMachineRegister(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("machine register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, machineUsage) }
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	exe, err := runningProgram()
	if err != nil {
		fmt.Fprintln(stderr, "machine register:", err)
		return 1
	}
	program, err := install.ServiceProgram(exe)
	if errors.Is(err, install.ErrNoInstalledProgram) {
		program, err = exe, nil
	}
	if err != nil {
		fmt.Fprintln(stderr, "machine register:", err)
		return 1
	}
	if err := registerMachine(program); err != nil {
		fmt.Fprintln(stderr, "machine register:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Registered the machine service: %s\n", program)
	return 0
}

func runningProgram() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the running program: %w", err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

func startupSlotDirs() ([]string, error) {
	slots, err := install.StartupSlots()
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(slots))
	for _, s := range slots {
		dirs = append(dirs, s.Dir)
	}
	return dirs, nil
}

func registerMachineService(stderr io.Writer) {
	exe, err := runningProgram()
	if err != nil {
		fmt.Fprintln(stderr, "note: the machine service was not registered:", err)
		return
	}
	program, err := install.ServiceProgram(exe)
	switch {
	case errors.Is(err, install.ErrNoInstalledProgram):
		fmt.Fprintln(stderr, "note: this account has no installed AII OS program; the machine service runs the build you name with: aii machine register")
	case err != nil:
		fmt.Fprintln(stderr, "note:", err)
	default:
		if err := registerMachine(program); err != nil {
			fmt.Fprintln(stderr, "note: the machine service was not registered:", err)
		}
	}
}

func registerMachine(program string) error {
	dir, err := install.MachineDir()
	if err != nil {
		return err
	}
	link, err := machine.LinkPath(dir)
	if err != nil {
		return err
	}
	return install.RegisterMachine(program, link)
}

func recordWithMachine(slot, dir string, stderr io.Writer) {
	kp, err := slotKey(dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "note: %s has no identity key yet; the machine service records the identity at its first link after birth\n", slot)
		return
	}
	if err != nil {
		fmt.Fprintf(stderr, "note: the machine service was not handed %s: %v\n", slot, err)
		return
	}
	mdir, err := install.MachineDir()
	if err != nil {
		fmt.Fprintf(stderr, "note: the machine service was not handed %s: %v\n", slot, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	image, _, _ := install.MachineRegistration()
	if _, err := machine.Register(ctx, mdir, dir, kp, image, 10*time.Second); err != nil {
		fmt.Fprintf(stderr, "note: the machine service did not record %s: %v\n", slot, err)
		return
	}
	fmt.Fprintf(stderr, "note: the machine service recorded %s (identity %s)\n", slot, kp.Fingerprint()[:12])
}

func slotKey(dir string) (*crypto.KeyPair, error) {
	keyPath := filepath.Join("data", "identity.sec")
	if cfg, err := app.ReadConfig(install.ConfigPathIn(dir)); err == nil && cfg.Identity.KeyPath != "" {
		keyPath = cfg.Identity.KeyPath
	}
	if !filepath.IsAbs(keyPath) {
		keyPath = filepath.Join(dir, keyPath)
	}
	if _, err := os.Stat(keyPath); err != nil {
		return nil, err
	}
	return crypto.LoadKeyPair(keyPath)
}

func forgetWithMachine(slot, dir string, stderr io.Writer) {
	kp, err := slotKey(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(stderr, "note: the machine service was not asked to let go of %s: %v\n", slot, err)
		return
	}
	mdir, err := install.MachineDir()
	if err != nil {
		fmt.Fprintf(stderr, "note: the machine service was not asked to let go of %s: %v\n", slot, err)
		return
	}
	image, _, _ := install.MachineRegistration()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := machine.Forget(ctx, mdir, dir, kp, image); err != nil {
		fmt.Fprintf(stderr, "note: the machine service did not let go of %s: %v\n", slot, err)
		return
	}
	fmt.Fprintf(stderr, "note: the machine service let go of %s and its plugins' references\n", slot)
}

func retireMachineIfLast(stdout, stderr io.Writer) {
	retired, err := install.RetireMachineIfLast()
	if err != nil {
		fmt.Fprintln(stderr, "note: the machine service was not unregistered:", err)
		return
	}
	if retired {
		fmt.Fprintln(stdout, "Unregistered the machine service with the account's last slot")
	}
}
