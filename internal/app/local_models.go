package app

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func firstAnsweringLocal(marked []providerEntry, probes map[string]providerProbe) (providerEntry, bool) {
	for _, e := range marked {
		if e.Local && probes[e.Name].state == probeOK {
			return e, true
		}
	}
	return providerEntry{}, false
}

func (a *App) localModelState(reg *providerRegistry) *dashboard.LocalModelState {
	return a.localModelStateFrom(reg, a.probeProviders(&providerRegistry{Providers: localEntries(reg)}))
}

func (a *App) localModelStateFrom(reg *providerRegistry, probes map[string]providerProbe) *dashboard.LocalModelState {
	marked := localEntries(reg)
	st := &dashboard.LocalModelState{Marked: len(marked)}
	if len(marked) == 0 {
		return st
	}
	e, ok := firstAnsweringLocal(marked, probes)
	if !ok {
		return st
	}
	st.Provider = e.Name

	cc, _, err := a.resolveRouteLLM(a.configSnapshot(), reg, e.Name, "")
	if err != nil {
		st.Problem = err.Error()
		return st
	}
	st.Model = cc.Model
	return st
}

type localPort struct {
	port  int
	usual string
}

var (
	localServerPorts = []localPort{
		{11434, "Ollama"},
		{1234, "LM Studio"},
		{8080, "llama.cpp"},
		{8000, "oMLX or vLLM"},
	}
	localServerHost = "127.0.0.1"
)

func localServerAddress(port int) string {
	return "http://" + net.JoinHostPort(localServerHost, strconv.Itoa(port)) + "/v1"
}

func usualLocalPort(port int) (localPort, bool) {
	for _, p := range localServerPorts {
		if p.port == port {
			return p, true
		}
	}
	return localPort{}, false
}

func entryAtLocalPort(reg *providerRegistry, port int) string {
	if reg == nil {
		return ""
	}
	for _, e := range reg.Providers {
		u, err := url.Parse(e.URL)
		if err != nil {
			continue
		}
		host := u.Hostname()
		if host != "localhost" && host != localServerHost {
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				continue
			}
		}
		if u.Port() == strconv.Itoa(port) {
			return e.Name
		}
	}
	return ""
}

func lookCandidate(port int) providerEntry {
	return providerEntry{Name: "look:" + strconv.Itoa(port), APIType: "openai", URL: localServerAddress(port), Local: true}
}

func (a *App) lookForLocalServers() []dashboard.LocalServerFound {
	reg, _ := a.loadProviders()
	found := make([]*dashboard.LocalServerFound, len(localServerPorts))
	var wg sync.WaitGroup
	for i, p := range localServerPorts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pr := a.probeOne(lookCandidate(p.port))
			f := dashboard.LocalServerFound{Port: p.port, Address: localServerAddress(p.port), Usual: p.usual, Entry: entryAtLocalPort(reg, p.port)}
			switch pr.state {
			case probeOK:
				f.Models = pr.models
			case probeAuthRequired:
				f.NeedsKey = true
			default:
				return
			}
			found[i] = &f
		}()
	}
	wg.Wait()
	out := []dashboard.LocalServerFound{}
	for _, f := range found {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}

func (a *App) addLocalServer(port int, model string) error {
	if _, ok := usualLocalPort(port); !ok {
		return fmt.Errorf("port %d is not one of the ports this looks on", port)
	}
	reg, err := a.loadProviders()
	if err != nil {
		return err
	}
	if name := entryAtLocalPort(reg, port); name != "" {
		return fmt.Errorf("%q already points at this server", name)
	}
	pr := a.probeOne(lookCandidate(port))
	switch pr.state {
	case probeOK:
	case probeAuthRequired:
		return fmt.Errorf("the server on port %d asks for a key; add it under Providers with its key", port)
	default:
		return fmt.Errorf("nothing answers on port %d now", port)
	}
	if model == "" || !slices.Contains(pr.models, model) {
		return fmt.Errorf("%q is not a model the server on port %d lists", model, port)
	}
	chat := true
	e := providerEntry{Name: "Local server, port " + strconv.Itoa(port), APIType: "openai", URL: localServerAddress(port), Local: true, Chat: &chat, DefaultModel: model}
	if err := normalizeProviderAPIType(&e); err != nil {
		return err
	}
	if err := validateModelWindow(e, e.DefaultModel); err != nil {
		return err
	}
	if err := a.changeProviders("", func(reg *providerRegistry) error {
		if name := entryAtLocalPort(reg, port); name != "" {
			return fmt.Errorf("%q already points at this server", name)
		}
		base := e.Name
		for n := 2; entryNamed(reg, e.Name) != nil; n++ {
			e.Name = base + " (" + strconv.Itoa(n) + ")"
		}
		reg.Providers = append(reg.Providers, e)
		return nil
	}); err != nil {
		return err
	}
	a.clearProviderStatus(e.Name)
	return nil
}
