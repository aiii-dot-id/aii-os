package pluginhost

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// The channel contract, and the two ways a plugin can claim to be a
// communications adapter without being one.
//
// plugin_family "channel_adapter" was a valid manifest value in both the
// host and the SDK, validated by each and consumed by neither: a plugin
// declaring itself a channel adapter activated as an ordinary tool
// bridge. The operator would have installed a way to be reached and
// received three unrelated tools.

func chanManifest(family string, ifaceID string, version int, methods []string) *packagefmt.Manifest {
	m := &packagefmt.Manifest{ID: "org.example.signal", PluginFamily: family}
	if ifaceID != "" {
		m.Interfaces = &packagefmt.InterfaceSet{
			Core: []packagefmt.InterfaceDecl{{ID: ifaceID, Version: version, Methods: methods}},
		}
	} else {
		m.Interfaces = &packagefmt.InterfaceSet{
			Core: []packagefmt.InterfaceDecl{{ID: "org.example.other", Version: 1, Methods: []string{"echo"}}},
		}
	}
	return m
}

func TestAChannelAdapterIsRecognisedAndItsOperationsLocated(t *testing.T) {
	m := chanManifest("channel_adapter", ChannelInterfaceID, ChannelInterfaceVersion,
		[]string{MethodSend, MethodReceive, MethodDescribe})
	ch, err := channelOf(m)
	if err != nil {
		t.Fatalf("a conforming channel adapter was refused: %v", err)
	}
	if ch == nil {
		t.Fatal("a conforming channel adapter was not recognised as one")
	}
	// The host dispatches by registry name, so those names must be the
	// ones the operations actually registered under.
	for label, got := range map[string]string{"send": ch.Send, "receive": ch.Receive, "describe": ch.Describe} {
		want, err := toolName(m.ID, label)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s resolves to %q, but the operation registered as %q", label, got, want)
		}
	}
}

// An ordinary plugin is untouched: this must not make every plugin a
// channel, or the recognition means nothing.
func TestAnOrdinaryPluginIsNotAChannel(t *testing.T) {
	ch, err := channelOf(chanManifest("tool_bridge", "", 0, nil))
	if err != nil {
		t.Fatalf("an ordinary plugin was refused: %v", err)
	}
	if ch != nil {
		t.Fatalf("a tool bridge was recognised as a channel: %+v", ch)
	}
}

// Claim without contract: the operator installed a way to be reached.
func TestTheFamilyWithoutTheInterfaceIsRefused(t *testing.T) {
	_, err := channelOf(chanManifest("channel_adapter", "", 0, nil))
	if err == nil {
		t.Fatal("a plugin claiming channel_adapter without the interface activated as an ordinary tool bridge")
	}
	if !strings.Contains(err.Error(), ChannelInterfaceID) {
		t.Fatalf("the refusal does not name what is missing: %v", err)
	}
}

// Contract without claim: this one is quieter and worse. It would
// activate cleanly, register three tools, and never carry a message —
// its author would have no idea why.
func TestTheInterfaceUnderAnotherFamilyIsRefused(t *testing.T) {
	_, err := channelOf(chanManifest("tool_bridge", ChannelInterfaceID, ChannelInterfaceVersion,
		[]string{MethodSend, MethodReceive, MethodDescribe}))
	if err == nil {
		t.Fatal("a plugin declaring the channel interface under another family was accepted — " +
			"the host would never look for its operations")
	}
	if !strings.Contains(err.Error(), "never carry a message") {
		t.Fatalf("the refusal does not say what would happen: %v", err)
	}
}

func TestAMissingMethodIsNamed(t *testing.T) {
	_, err := channelOf(chanManifest("channel_adapter", ChannelInterfaceID, ChannelInterfaceVersion,
		[]string{MethodSend, MethodDescribe})) // no receive
	if err == nil {
		t.Fatal("a channel adapter that cannot receive was accepted")
	}
	if !strings.Contains(err.Error(), MethodReceive) {
		t.Fatalf("the refusal does not name the missing method: %v", err)
	}
}

func TestAVersionThisHostDoesNotSpeakIsRefused(t *testing.T) {
	_, err := channelOf(chanManifest("channel_adapter", ChannelInterfaceID, ChannelInterfaceVersion+1,
		[]string{MethodSend, MethodReceive, MethodDescribe}))
	if err == nil {
		t.Fatal("an interface version this host does not speak was accepted")
	}
	if !strings.Contains(err.Error(), "this host speaks") {
		t.Fatalf("the refusal does not say which version is spoken: %v", err)
	}
}

// The contract is three methods and no more, on purpose: an adapter
// speaks a protocol and does not decide who may be written to, when the
// identity wakes, or what a message becomes.
func TestTheContractIsExactlyThreeMethods(t *testing.T) {
	if len(channelMethods) != 3 {
		t.Fatalf("the channel contract grew to %d methods: %v — each one is a decision "+
			"taken away from the host and copied into every adapter", len(channelMethods), channelMethods)
	}
}
