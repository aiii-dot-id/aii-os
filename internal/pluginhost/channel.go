package pluginhost

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// channel.go — what a communications plugin must be, and how the host
// finds its operations.
//
// plugin_family "channel_adapter" has been a valid manifest value since
// the format was written, validated identically here and in the SDK, and
// CONSUMED BY NEITHER: a plugin declaring itself a channel adapter was
// treated exactly like a tool bridge. The taxonomy existed and meant
// nothing. This is what gives it meaning.
//
// The contract is deliberately three methods and no more. A channel
// adapter speaks a protocol; it does not decide who may be written to,
// when the identity is woken, or what a message becomes. Those are the
// host's, and keeping them there is what lets an adapter be written once
// and work on five platforms without containing a line of platform code.
//
// THE ADAPTER WAITS; THE HOST LOOPS. receive() blocks until there is
// news. The host calls it again when it returns — a blocking read loop,
// the ordinary shape of an event consumer, with no timer anywhere and
// nothing running between events.
//
// The platforms differ only in HOW OFTEN the host may call: a desktop
// process loops continuously, while on iOS and Android no process exists
// to hold a request, so the OS wakes it and the host calls receive once
// with a short budget before the process sleeps again. The adapter is
// identical either way and contains no platform code, which is the whole
// point of putting the loop in the host.

const (
	// ChannelInterfaceID is the interface a channel adapter declares.
	ChannelInterfaceID = "aii.channel"
	// ChannelInterfaceVersion is the contract version this host speaks.
	ChannelInterfaceVersion = 1

	// MethodSend delivers one message: the host hands over an address and
	// a body, the adapter speaks the protocol and returns a receipt.
	MethodSend = "send"
	// MethodReceive BLOCKS until something arrives, then returns it.
	//
	// This is a blocking read, not a timer. Telegram's getUpdates and
	// Matrix's /sync both take a timeout parameter and hold the response
	// open until an event occurs — the client sleeps in the kernel on a
	// socket, costing nothing, and returns the moment there is news. The
	// broker's http.get permits it directly: a long-poll is an ordinary
	// GET that takes fifty seconds to answer, and MaxHTTPTimeout is 60s.
	//
	// "a grant is not a socket" (broker.go) forbids a CACHED persistent
	// connection. It does not forbid one slow call. An earlier draft of
	// this contract read that line as "an adapter cannot wait" and
	// invented a menu of host-side wait mechanisms with a polling
	// fallback; that was wrong, and the polling was the worst of it.
	// Telegram's own ecosystem calls this "polling", which is where the
	// confusion came from — but timeout=0 is the wasteful kind their docs
	// warn against, and timeout=45 is a blocking read.
	MethodReceive = "receive"
	// MethodDescribe names the channel this adapter serves and how long
	// its receive may block. Metadata only — the host needs the name to
	// route by, and the budget to size its own context.
	MethodDescribe = "describe"
)

// channelMethods is the whole contract, in the order a reader should
// meet it.
var channelMethods = []string{MethodSend, MethodReceive, MethodDescribe}

// Channel is an activated communications plugin: which plugin serves the
// channel, and the registry names its three operations answer to.
//
// The host holds tool NAMES rather than callables because dispatch,
// SAFE-mode suspension and the capability broker all live behind
// Registry.Execute. A channel adapter is invoked exactly the way the
// identity's own tools are, and is suspended by exactly the same gate.
type Channel struct {
	PluginID string
	Send     string
	Receive  string
	Describe string
}

// ChannelContractError is the typed refusal for a plugin that claims to
// be a channel adapter and is not one.
type ChannelContractError struct {
	PluginID string
	Reason   string
}

func (e *ChannelContractError) Error() string {
	return fmt.Sprintf("plugin %s does not satisfy the channel contract: %s", e.PluginID, e.Reason)
}

// channelOf reads a manifest and returns its Channel, or a typed refusal.
//
// FAILS CLOSED IN BOTH DIRECTIONS. A plugin whose family says
// channel_adapter but which declares no aii.channel interface is refused
// rather than quietly activated as a tool bridge — the operator installed
// a way to be reached, and a half of one that registers three unrelated
// tools is worse than nothing. And a plugin declaring aii.channel while
// claiming another family is refused too: the host would never look for
// its operations, so its author would watch it activate and never carry
// a message.
func channelOf(m *packagefmt.Manifest) (*Channel, error) {
	declaresChannel := false
	var decl packagefmt.InterfaceDecl
	if m.Interfaces != nil {
		for _, d := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
			if d.ID == ChannelInterfaceID {
				declaresChannel, decl = true, d
				break
			}
		}
	}
	isChannelFamily := m.PluginFamily == "channel_adapter"

	switch {
	case !isChannelFamily && !declaresChannel:
		return nil, nil // an ordinary plugin; nothing to see
	case isChannelFamily && !declaresChannel:
		return nil, &ChannelContractError{PluginID: m.ID,
			Reason: "plugin_family is channel_adapter but no " + ChannelInterfaceID + " interface is declared"}
	case !isChannelFamily && declaresChannel:
		return nil, &ChannelContractError{PluginID: m.ID,
			Reason: "declares " + ChannelInterfaceID + " but plugin_family is " + m.PluginFamily +
				"; the host only looks for channel operations on a channel_adapter, so this would activate and never carry a message"}
	}

	if decl.Version != ChannelInterfaceVersion {
		return nil, &ChannelContractError{PluginID: m.ID,
			Reason: fmt.Sprintf("declares %s@%d; this host speaks @%d", ChannelInterfaceID, decl.Version, ChannelInterfaceVersion)}
	}

	have := make(map[string]bool, len(decl.Methods))
	for _, method := range decl.Methods {
		have[method] = true
	}
	var missing []string
	for _, want := range channelMethods {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return nil, &ChannelContractError{PluginID: m.ID,
			Reason: fmt.Sprintf("%s@%d requires %v; missing %v", ChannelInterfaceID, ChannelInterfaceVersion, channelMethods, missing)}
	}

	ch := &Channel{PluginID: m.ID}
	for _, method := range channelMethods {
		name, err := toolName(m.ID, method)
		if err != nil {
			return nil, err
		}
		switch method {
		case MethodSend:
			ch.Send = name
		case MethodReceive:
			ch.Receive = name
		case MethodDescribe:
			ch.Describe = name
		}
	}
	return ch, nil
}
