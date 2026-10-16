package app

import (
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

var tokenRemovalWindow func(path string)

var appendPauseBound = defaultAppendPauseBound

var activeVoicePlugin = defaultActiveVoicePlugin

var pushVoiceStatus = defaultPushVoiceStatus

var foundingBackoff = genesis.FetchBackoff

var recordStream = ledger.Stream

var startRetryAfter = time.AfterFunc

var endedStartsRestart = defaultEndedStartsRestart

var openForStartup = store.OpenForStartup

var witnessNoticeNow = time.Now

var recordPlace = ledger.RecordArtifact

var namedKey = genesis.NamedKey

var loadKeyPair = crypto.LoadKeyPair

var stopGrace = 5 * time.Second

var doorStep = func(step string) {}

var overlayStep = func(step string) {}

var floorStep = func(step string) {}

var limitsStep = func(step string) {}

var reloadStep = func(step string) {}

var snapshotStep = func(step string) {}

var startNameStep = func() {}

var modalityGuard = firewall.FetchGuard

var vulkanProbe = shippedVulkanProbe

var speakerDecisionBound = 16 * time.Second

var voiceSynthesize = (*App).synthesizeReply

var voicePassWait = 5 * time.Minute

var voiceWake = (*App).wake

var voiceEngineOpen = (*pluginhost.VoiceSession).OpenWithAudio

var voiceFallbackMint = (*App).speakAhead
