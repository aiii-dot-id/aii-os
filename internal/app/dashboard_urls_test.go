package app

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestDashboardBootOutputContainsTheURLButNotTheCredential(t *testing.T) {
	const token = "configured-dashboard-token"
	cfg := defaultConfig()
	cfg.Dashboard.RequireToken = true
	cfg.Dashboard.AccessToken = token
	a := New(cfg)
	a.dashboard = dashboard.New("127.0.0.1", 8181, &dashboard.WSHandler{})

	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = previous }()

	a.printDashboardURLs()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = previous
	body, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	output := string(body)
	if !strings.Contains(output, "http://127.0.0.1:8181") {
		t.Fatalf("boot output omitted the dashboard URL: %q", output)
	}
	if strings.Contains(output, token) || strings.Contains(output, "?token=") {
		t.Fatalf("boot output disclosed a bearer credential: %q", output)
	}
}
