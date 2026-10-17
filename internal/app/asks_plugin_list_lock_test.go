package app

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestAConnectAnswerReadsThePluginListUnderItsLock(t *testing.T) {
	app, id, _, _ := actsApp(t)
	if err := app.store.StartWorkSession(t.Context(), "ws_lock", "read the issues"); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the sweep: commit, then retire, each under pluginMu
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			other := fmt.Sprintf("org.example.sweep%d", i)
			r := &pluginhost.Activation{ID: other, Plugin: &pluginhost.ActivePlugin{ID: other}}
			app.adoptPlugin(r, nil)
			app.releaseActivation(r)
			time.Sleep(200 * time.Microsecond)
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	for i := 0; i < 16; i++ {
		if err := app.proposeAsk("ws_lock", fmt.Sprintf("I need your GitHub to read issue %d.", i), nil, id); err != nil {
			t.Fatal(err)
		}
		v := app.askViews()[0]
		if _, _, err := app.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "read"}); err != nil {
			t.Fatal(err)
		}
		if g := app.configSnapshot().Plugins.Grants[id]; !g.ReadOnly {
			t.Fatalf("answer %d: read only follows the answer for the installed plugin: %+v", i, g)
		}
	}
}
