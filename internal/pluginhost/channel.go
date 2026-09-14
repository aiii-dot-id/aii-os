package pluginhost

import (
	"fmt"

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
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const (
	// .
	ChannelInterfaceID = "aii.channel"
	// .
	ChannelInterfaceVersion = 1

	// .
	// .
	MethodSend = "send"
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	MethodReceive = "receive"
	// .
	// .
	// .
	MethodDescribe = "describe"
)

// .
// .
var channelMethods = []string{MethodSend, MethodReceive, MethodDescribe}

// .
// .
// .
// .
// .
// .
// .
type Channel struct {
	PluginID string
	Send     string
	Receive  string
	Describe string
}

// .
// .
type ChannelContractError struct {
	PluginID string
	Reason   string
}

func (e *ChannelContractError) Error() string {
	return fmt.Sprintf("plugin %s does not satisfy the channel contract: %s", e.PluginID, e.Reason)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
		return nil, nil
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
