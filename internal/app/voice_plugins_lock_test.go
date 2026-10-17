package app

import (
	"testing"
	"time"
)

func TestListingTheVoiceEnginesDoesNotWaitOnAnotherReader(t *testing.T) {
	a := New(&Config{})
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.voicePlugins()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("voicePlugins waited on another reader of the plugin set: it takes the write half for a read")
	}
}
