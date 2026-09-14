package pluginhost

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .
// .
// .
// .
// .
// .

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
	// .
	// .
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

// .
// .
func TestAnOrdinaryPluginIsNotAChannel(t *testing.T) {
	ch, err := channelOf(chanManifest("tool_bridge", "", 0, nil))
	if err != nil {
		t.Fatalf("an ordinary plugin was refused: %v", err)
	}
	if ch != nil {
		t.Fatalf("a tool bridge was recognised as a channel: %+v", ch)
	}
}

// .
func TestTheFamilyWithoutTheInterfaceIsRefused(t *testing.T) {
	_, err := channelOf(chanManifest("channel_adapter", "", 0, nil))
	if err == nil {
		t.Fatal("a plugin claiming channel_adapter without the interface activated as an ordinary tool bridge")
	}
	if !strings.Contains(err.Error(), ChannelInterfaceID) {
		t.Fatalf("the refusal does not name what is missing: %v", err)
	}
}

// .
// .
// .
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
		[]string{MethodSend, MethodDescribe}))
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

// .
// .
// .
func TestTheContractIsExactlyThreeMethods(t *testing.T) {
	if len(channelMethods) != 3 {
		t.Fatalf("the channel contract grew to %d methods: %v — each one is a decision "+
			"taken away from the host and copied into every adapter", len(channelMethods), channelMethods)
	}
}
