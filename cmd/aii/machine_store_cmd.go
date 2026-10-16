//go:build (linux && !android) || (darwin && !ios) || windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/machine"
)

const machineStoreUsage = `Usage:
  aii machine store <dir> <slot>   move the account's plugin store to <dir>
  aii machine store -list          print the account's plugin stores, one per line

Every identity's native plugins on this account read their models and
runtimes from one plugin store, which the machine service writes (by
default ~/.aii/plugins). This names another place for it — a volume with
more room, or a shorter path where a Windows path is too long. The service
copies what the old store holds into the new one as it is needed, by
digest, and empties the old one as nothing runs from it. The place must be
on a volume that keeps hard links (NTFS, APFS, ext4, xfs, btrfs).

<slot> is the identity the request is proved with, as aii register names
it; its key stays in this command.

-list reads the machine service's state and changes nothing: where the
store is, then every store it is moving away from. The package's removal
reads it to know what a purge removes.
`

func runMachineStore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("machine store", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, machineStoreUsage) }
	list := fs.Bool("list", false, "print the account's plugin stores, one per line")
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}
	if *list {
		if fs.NArg() != 0 {
			fs.Usage()
			return 2
		}
		return runMachineStoreList(stdout, stderr)
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	store, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "machine store:", err)
		return 1
	}
	root, err := install.Root()
	if err != nil {
		fmt.Fprintln(stderr, "machine store:", err)
		return 1
	}
	slot := filepath.Join(root, fs.Arg(1))
	if _, err := os.Stat(slot); err != nil {
		fmt.Fprintf(stderr, "machine store: %s does not exist — name a slot of this account, as aii register does\n", slot)
		return 1
	}
	cfg, err := app.ReadConfig(install.ConfigPathIn(slot))
	if err != nil {
		fmt.Fprintln(stderr, "machine store:", err)
		return 1
	}
	keyPath := cfg.Identity.KeyPath
	if keyPath == "" {
		keyPath = filepath.Join("data", "identity.sec")
	}
	if !filepath.IsAbs(keyPath) {
		keyPath = filepath.Join(slot, keyPath)
	}
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "machine store: %s's key: %v\n", slot, err)
		return 1
	}
	mdir, image := cfg.Machine.Dir, ""
	switch {
	case mdir == "":
		if mdir, err = install.MachineDir(); err != nil {
			fmt.Fprintln(stderr, "machine store:", err)
			return 1
		}
		image, _, _ = install.MachineRegistration()
	case !filepath.IsAbs(mdir):
		mdir = filepath.Join(slot, mdir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := machine.Place(ctx, mdir, slot, kp, image, store)
	var ref *machine.Refusal
	switch {
	case errors.As(err, &ref):
		fmt.Fprintln(stderr, "machine store:", ref.Error())
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "machine store:", err)
		return 1
	}
	fmt.Fprintf(stdout, "The account's plugin store is %s\n", res.Store)
	for _, d := range res.Draining {
		fmt.Fprintf(stdout, "%s is copied from by digest as material is needed, and emptied as nothing runs from it\n", d)
	}
	return 0
}

func runMachineStoreList(stdout, stderr io.Writer) int {
	dir, err := install.MachineDir()
	if err != nil {
		fmt.Fprintln(stderr, "machine store:", err)
		return 1
	}
	places, err := machine.StorePlaces(dir)
	if err != nil {
		fmt.Fprintln(stderr, "machine store:", err)
		return 1
	}
	for _, p := range places {
		if strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			fmt.Fprintf(stderr, "machine store: a store's path holds a control character: %q\n", p)
			return 1
		}
	}
	for _, p := range places {
		fmt.Fprintln(stdout, p)
	}
	return 0
}
