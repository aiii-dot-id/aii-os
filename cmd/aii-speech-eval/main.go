package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/speech"
	"github.com/aiii-dot-id/aii-os/internal/speecheval"
)

const (
	exitPass  = 0
	exitFail  = 1
	exitError = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv)) }

func run(args []string, out, errOut io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("aii-speech-eval", flag.ContinueOnError)
	fs.SetOutput(errOut)
	corpus := fs.String("corpus", "", "directory holding manifest.json and its clips (required)")
	endpoint := fs.String("endpoint", "", "the endpoint's API base, e.g. http://127.0.0.1:8081/v1 (required)")
	model := fs.String("model", "", "the transcription model to ask for")
	language := fs.String("language", "", "an optional BCP-47 hint, e.g. en")
	keyEnv := fs.String("key-env", "", "name of the environment variable holding the API key (none for a local engine)")
	timeout := fs.Duration("timeout", 5*time.Minute, "per-clip request timeout")
	reportPath := fs.String("report", "", "also write the report to this file")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *corpus == "" || *endpoint == "" {
		fmt.Fprintln(errOut, "usage: aii-speech-eval -corpus DIR -endpoint URL [-model NAME] [-language en] [-key-env VAR] [-timeout 5m] [-report FILE]")
		return exitError
	}
	cfg := speech.Config{Endpoint: *endpoint, Model: *model, Language: *language, Timeout: *timeout}
	if *keyEnv != "" {
		if cfg.APIKey = getenv(*keyEnv); cfg.APIKey == "" {
			fmt.Fprintf(errOut, "aii-speech-eval: the environment variable %s named by -key-env is empty\n", *keyEnv)
			return exitError
		}
	}
	m, err := speecheval.LoadManifest(filepath.Join(*corpus, "manifest.json"))
	if err != nil {
		fmt.Fprintln(errOut, "aii-speech-eval:", err)
		return exitError
	}
	client := speech.New(cfg)
	fmt.Fprintf(out, "endpoint %s, model %q\n", client.Endpoint(), client.Model())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	results, err := speecheval.Run(ctx, m, *corpus, func(ctx context.Context, pcm []byte, rate, channels int) (string, error) {
		res, err := client.Transcribe(ctx, pcm, rate, channels)
		return res.Text, err
	}, func(done, total int, file string) { fmt.Fprintf(errOut, "\r%d/%d %-40s", done, total, file) })
	fmt.Fprintln(errOut)
	if err != nil {
		fmt.Fprintln(errOut, "aii-speech-eval:", err)
		return exitError
	}
	aggregate := speecheval.Aggregate(m, results)
	aggregate.Target = fmt.Sprintf("%s, model %q, language %q", client.Endpoint(), client.Model(), *language)
	report := aggregate.Format(m.Contract)
	fmt.Fprint(out, report)
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, []byte(report), 0o600); err != nil {
			fmt.Fprintln(errOut, "aii-speech-eval:", err)
			return exitError
		}
	}
	if len(aggregate.Violations(m.Contract)) > 0 {
		return exitFail
	}
	return exitPass
}
