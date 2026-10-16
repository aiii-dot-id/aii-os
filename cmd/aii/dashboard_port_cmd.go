package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

func runDashboardPort(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dashboard-port", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "Identity install directory")
	config := fs.String("config", "", "Path to config file (default: config/config.json)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aii dashboard-port [-dir directory] [-config file] <port>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	port, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "dashboard-port: %q is not a port number\n", fs.Arg(0))
		return 2
	}
	path := operatorConfigPath(*dir, *config)

	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "dashboard-port: read %s: %v\n", path, err)
		return 1
	}
	if err := app.SetDashboardPort(path, port); err != nil {
		fmt.Fprintf(stderr, "dashboard-port: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, port)
	fmt.Fprintf(stderr, "dashboard-port: the dashboard serves on port %d from the identity's next start — its address changes with it, and a browser signs in again at the new address if the dashboard asks for its access token\n", port)
	return 0
}
