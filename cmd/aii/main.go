// .
// .
// .
package main

import (
	"flag"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"log"
	"os"
	_ "time/tzdata"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/workercmd"
)

func main() {
	// .
	// .
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		os.Exit(runPlugin(os.Args[2:], os.Stdout, os.Stderr))
	}

	// .
	// .
	// .
	// .
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			os.Exit(runInit(os.Args[2:]))
		case "slots":
			os.Exit(runSlots())
		case "register":
			os.Exit(runRegister(os.Args[2:]))
		case "stop":
			os.Exit(runStop(os.Args[2:]))
		case "unregister":
			os.Exit(runUnregister(os.Args[2:]))
		case "verify":
			os.Exit(runVerify(os.Args[2:], os.Stdout, os.Stderr))
		case "snapshot":
			// .
			// .
			os.Exit(runSnapshot(os.Args[2:], os.Stdout, os.Stderr))
		case "escrow":
			// .
			// .
			// .
			// .
			os.Exit(runEscrow(os.Args[2:], os.Stdout, os.Stderr, terminalPassphrase(os.Stderr)))
		case "memory-score":
			// .
			// .
			os.Exit(runMemoryScore(os.Args[2:], os.Stdout, os.Stderr))
		case "log":
			// .
			// .
			// .
			os.Exit(runLog(os.Args[2:], os.Stdout, os.Stderr))
		case "dashboard-token":
			// .
			// .
			os.Exit(runDashboardToken(os.Args[2:], os.Stdout, os.Stderr))
		case app.WorkerSubcommand:
			// .
			// .
			// .
			os.Exit(workercmd.Run(os.Args[2:]))
		}
	}

	// .
	// .
	// .
	// .
	// .
	showVersion := flag.Bool("version", false, "Print version and build identity, then exit")
	configPath := flag.String("config", "", "Path to config file (default: config/config.json, or config.json for an identity installed before that move)")
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	startDir := flag.String("dir", "", "Identity install directory to run in (default: the current directory)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("AII OS v%s (build %s)\n", app.VersionString(), app.BuildIdentity())
		return
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if args := flag.Args(); len(args) > 0 {
		fmt.Fprintf(os.Stderr, "aii: unrecognized argument %q — not a subcommand; runtime flags start with '-' (e.g. -version for the build identity)\n", args[0])
		os.Exit(2)
	}

	if *startDir != "" {
		if err := os.Chdir(*startDir); err != nil {
			log.Fatalf("cannot enter identity directory %s: %v", *startDir, err)
		}
	}
	// .
	// .
	if *configPath == "" {
		*configPath = app.DefaultConfigPath()
	}

	cfg, err := app.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	// .
	// .
	if app.Version != "" {
		logsink.Info("boot.start", "AII OS v%s", app.Current())
	}

	app.New(cfg).Run()
}
