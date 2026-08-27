// Command aii is the AII OS runtime entry point — thin by rule: parse
// flags, load config, hand off to internal/app (the importable runtime
// both desktop and mobile hosts embed). No business logic here.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

func main() {
	// Subcommand dispatch stays minimal: "aii plugin ..." is the only
	// verb family; everything else is the runtime's flag path.
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		os.Exit(runPlugin(os.Args[2:], os.Stdout, os.Stderr))
	}

	// Asking a binary what it is must not require starting an identity.
	// Everything BuildIdentity knows was already computed and then only
	// whispered into the boot log, so answering "which build is this?"
	// meant finding a log from a boot that may be hours gone — which is
	// what the 2026-08-21 emergency-swap forensics could not do.
	showVersion := flag.Bool("version", false, "Print version and build identity, then exit")
	configPath := flag.String("config", "config.json", "Path to config file")
	// The install directory IS the identity's whole world — config,
	// ledger, database, key, providers.json and plugins/ all live beside
	// each other and every path defaults relative. That makes the working
	// directory load-bearing, so -dir names it explicitly for launchers
	// that do not set one (systemd without WorkingDirectory=, launchd, a
	// double-clicked bundle). Absent, the current directory is used
	// exactly as before. Same shape the mobile binding already uses when
	// it chdirs into the app container.
	startDir := flag.String("dir", "", "Identity install directory to run in (default: the current directory)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("AII OS v%s (build %s)\n", app.VersionString(), app.BuildIdentity())
		return
	}

	if *startDir != "" {
		if err := os.Chdir(*startDir); err != nil {
			log.Fatalf("cannot enter identity directory %s: %v", *startDir, err)
		}
	}

	cfg, err := app.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	// Version banner — the VERSION file injected via ldflags, printed
	// before the runtime owns stdout (the runtime's own banner follows).
	if app.Version != "" {
		log.Printf("AII OS v%s", app.Version)
	}

	app.New(cfg).Run()
}
