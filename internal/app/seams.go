package app

import (
	"net"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

var activeVoicePlugin = defaultActiveVoicePlugin

var pushVoiceStatus = defaultPushVoiceStatus

var stopGrace = 5 * time.Second

var snapshotStep = func(step string) {}

var syncDir = atomicfile.SyncDir

var modalityGuard = firewall.FetchGuard

var retryIssueStarted = func() {}

var restoreStep = func(step string) {}

var afterKeyPublished = func(string) {}

var interfaceAddrs = net.InterfaceAddrs

var vulkanProbe = shippedVulkanProbe

var speakerDecisionBound = 16 * time.Second

var voiceSynthesize = (*App).synthesizeReply

var timerWakeBudget = 15 * time.Minute

var voicePassWait = 5 * time.Minute

var voiceWake = (*App).wake

var voiceEngineOpen = (*pluginhost.VoiceSession).OpenWithAudio

var voiceFallbackMint = (*App).speakAhead
