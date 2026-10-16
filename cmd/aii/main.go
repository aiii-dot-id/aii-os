package main

import (
	"flag"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"log"
	"os"
	_ "time/tzdata"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
	"github.com/aiii-dot-id/aii-os/internal/workercmd"
)

func main() {

	if code, ok := dispatch(os.Args[1:], os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}

	showVersion := flag.Bool("version", false, "Print version and build identity, then exit")
	configPath := flag.String("config", "", "Path to config file (default: config/config.json, or config.json for an identity installed before that move)")

	startDir := flag.String("dir", "", "Identity install directory to run in (default: the current directory)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("AII OS v%s (build %s)\n", app.VersionString(), app.BuildIdentity())
		return
	}

	if args := flag.Args(); len(args) > 0 {
		fmt.Fprintf(os.Stderr, "aii: unrecognized argument %q — not a subcommand; runtime flags start with '-' (e.g. -version for the build identity)\n", args[0])
		os.Exit(2)
	}

	if *startDir != "" {
		if err := os.Chdir(*startDir); err != nil {
			log.Fatalf("cannot enter identity directory %s: %v", *startDir, err)
		}
	}

	if *configPath == "" {
		*configPath = install.ConfigPathIn("")
	}

	cfg, err := app.LoadConfig(*configPath)
	if err != nil {

		log.Printf("Config error: %v", err)
		app.RollBackOverRefusedConfig(*configPath)
		os.Exit(1)
	}
	app.ForgetRefusedStart(*configPath)

	logsink.Info("boot.start", "AII OS v%s", app.Current())

	app.New(cfg).Run()
}

func dispatch(args []string, stdout, stderr io.Writer) (code int, ok bool) {
	if len(args) == 0 {
		return 0, false
	}
	rest := args[1:]
	switch args[0] {
	case "plugin":
		return runPlugin(rest, stdout, stderr), true
	case vulkancap.Subcommand:
		return runVulkanProbe(rest, stdout, stderr), true
	case "init":
		return runInit(rest, stdout, stderr), true
	case "slots":
		return runSlots(rest, stdout, stderr), true
	case "register":
		return runRegister(rest, stdout, stderr), true
	case "stop":
		return runStop(rest, stdout, stderr), true
	case "unregister":
		return runUnregister(rest, stdout, stderr), true
	case "uninstall":

		return runUninstall(rest, stdout, stderr), true
	case "machine":

		return runMachine(rest, stdout, stderr), true
	case "verify":
		return runVerify(rest, stdout, stderr), true
	case "ledger":

		return runLedger(rest, stdout, stderr), true
	case "snapshot":

		return runSnapshot(rest, stdout, stderr), true
	case "escrow":

		return runEscrow(rest, stdout, stderr, terminalPassphrase(stderr)), true
	case "memory-score":

		return runMemoryScore(rest, stdout, stderr), true
	case "log":

		return runLog(rest, stdout, stderr), true
	case "dashboard-token":

		return runDashboardToken(rest, stdout, stderr), true
	case "dashboard-port":

		return runDashboardPort(rest, stdout, stderr), true
	case "dashboard-link":

		return runDashboardLink(rest, stdout, stderr), true
	case app.WorkerSubcommand:

		return workercmd.Run(rest), true
	}
	return 0, false
}
