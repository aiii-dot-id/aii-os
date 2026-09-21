package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

func runDashboardToken(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dashboard-token", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "Identity install directory")
	config := fs.String("config", "", "Path to config file (default: config/config.json)")
	rotate := fs.Bool("rotate", false, "Mint a fresh access token into the config file and print it; the running identity applies it on its next configuration reload, and every signed-in browser must sign in again")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "dashboard-token: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	path := *config
	switch {
	case path == "":
		path = app.ConfigPathIn(*dir)
	case *dir != "" && !filepath.IsAbs(path):
		path = filepath.Join(*dir, path)
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "dashboard-token: read %s: %v\n", path, err)
		return 1
	}
	if *rotate {
		token, err := app.RotateDashboardToken(path)
		if err != nil {
			fmt.Fprintf(stderr, "dashboard-token: rotate: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, token)
		fmt.Fprintln(stderr, "dashboard-token: a new token is in the config file; the running identity applies it on its next configuration reload (SIGHUP, or any save from Settings), and every signed-in browser signs in again")
		return 0
	}
	cfg, err := app.LoadConfig(path)
	if err != nil {
		fmt.Fprintf(stderr, "dashboard-token: %v\n", err)
		return 1
	}
	if cfg.Dashboard.AccessToken == "" {
		fmt.Fprintln(stderr, "dashboard-token: no dashboard access token is configured; start the identity to mint or migrate one")
		return 1
	}
	fmt.Fprintln(stdout, cfg.Dashboard.AccessToken)
	return 0
}
