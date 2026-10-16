package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

func runDashboardLink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dashboard-link", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "Identity install directory")
	config := fs.String("config", "", "Path to config file (default: config/config.json)")
	port := fs.Int("port", 0, "The dashboard's port (default: the configured one)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aii dashboard-link [-dir directory] [-config file] [-port port]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	path := operatorConfigPath(*dir, *config)
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "dashboard-link: read %s: %v\n", path, err)
		return 1
	}
	cfg, err := app.ReadConfig(path)
	if err != nil {
		fmt.Fprintf(stderr, "dashboard-link: %v\n", err)
		return 1
	}
	p := *port
	if p == 0 {
		p = cfg.Dashboard.Port
	}
	if p <= 0 || p > 65535 {
		fmt.Fprintf(stderr, "dashboard-link: no dashboard port is configured in %s; name it with -port\n", path)
		return 1
	}
	base := "http://127.0.0.1:" + strconv.Itoa(p)
	if cfg.Dashboard.AccessToken == "" {

		fmt.Fprintln(stdout, base)
		return 0
	}
	link, err := askLauncherLink(base, cfg.Dashboard.AccessToken)
	if err != nil {
		fmt.Fprintf(stderr, "dashboard-link: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, link)
	return 0
}

func askLauncherLink(base, token string) (string, error) {
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Post(base+"/auth/launcher", "text/plain; charset=utf-8", strings.NewReader(token))
	if err != nil {
		return "", fmt.Errorf("the dashboard at %s did not answer: %w", base, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	switch res.StatusCode {
	case http.StatusNoContent:
		return base, nil
	case http.StatusOK:
		var got struct {
			Link string `json:"link"`
		}
		if json.Unmarshal(body, &got) != nil || !strings.HasPrefix(got.Link, base+"/auth/link#") || len(got.Link) == len(base+"/auth/link#") {
			return "", errors.New("the dashboard's answer is not a sign-in link at " + base)
		}
		return got.Link, nil
	}
	return "", fmt.Errorf("the dashboard at %s made no sign-in link (%d): %s", base, res.StatusCode, strings.TrimSpace(string(body)))
}
