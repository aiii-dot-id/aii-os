//go:build android || ios

// .
// .
// .
// .
// .
// .

package hostcap

func can(c Capability) Status {
	switch c {
	case Subprocess:
		return Status{Reason: "this topology cannot spawn subprocesses (mobile app host: iOS forbids exec; the Android app sandbox restricts it; the runtime is a library inside a host app)"}
	case Shell:
		return Status{Reason: "no shell exists on the mobile app host — the pure-Go tool floor is the tool surface here"}
	case NativeChild:
		return Status{Reason: "native T3 children are never spawned on mobile — T3 is in-process wasm by construction"}
	case SelfReplace:
		return Status{Reason: "the platform store owns the binary lifecycle on mobile; there is no standalone binary to swap or re-exec"}
	}
	return Status{Reason: "unknown capability"}
}
