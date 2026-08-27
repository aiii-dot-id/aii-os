# Platform shells

Linux, macOS and Windows run the `aii` binary directly and serve the
dashboard over loopback. Android and iOS cannot: the OS owns the process
lifecycle, so each needs a native shell that embeds the runtime, shows
its dashboard in a WebView, and relays foreground/background and OS
wakes. That is all a shell is — a window and a wake source. The runtime
is the identity; the shell is not.

Both shells consume the **gomobile binding of `../mobile`**, whose
contract is documented at the top of `mobile/mobile.go`:

    rt, err := mobile.Start(configPath)
    rt.DashboardURL()          // load in the WebView
    rt.SetForeground(bool)     // app lifecycle -> presence
    rt.SetWakeScheduler(s)     // OS scheduler learns next-due
    rt.TimeWake()              // OS wake -> TIME catch-up
    rt.Stop()

The binding is a **build product, not source**, so it is gitignored in
both shells. `release.yml` builds it in CI and publishes it as a release
asset; build it locally with the commands below.

## Android

    go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init
    gomobile bind -target=android/arm64 -o shells/android/app/libs/aiios.aar ./mobile
    cd shells/android && ./gradlew assembleDebug

The Gradle **wrapper is committed** (`gradlew` + wrapper jar), so no
local Gradle install is needed — the review found the documented
`./gradlew` invoking a file that did not exist.

`local.properties` (your SDK path) is gitignored — Android Studio writes
it on first open, or set `ANDROID_HOME`.

Exact next-due wakes need **Settings > Apps > Special app access >
Alarms & reminders**. Ungranted, `AppRuntime` falls back to
`setAndAllowWhileIdle`: Doze-batched, still wakes, and the TIME catch-up
tolerates the lateness.

## iOS

    gomobile bind -target=ios/arm64 -o shells/ios/Mobile.xcframework ./mobile
    brew install xcodegen && cd shells/ios && xcodegen generate
    open AIIOS.xcodeproj

`AIIOS.xcodeproj` is generated from `project.yml` and gitignored — a
generated project is a build product, and a hand-merged `pbxproj` is a
conflict machine.

Signing belongs to whoever builds. Set `AIIOS_DEVELOPMENT_TEAM` to your
Apple team id before `xcodegen generate`, or leave it unset and let
Xcode sign locally with your own account.

iOS wakes are **opportunistic by design**: the `BGTaskScheduler`
`earliestBeginDate` means "not before", never "at". The durable alarm
row plus boot catch-up is the correctness floor; the OS wake is
acceleration only.

## Known gap

`release.yml` builds the Android `.aar` in CI. The iOS `.xcframework`
step is still a documented stub — gomobile needs Xcode, so it waits on a
macOS runner and is produced offline until then. A tag cut before that
lands yields a release whose iOS asset is missing, not one that is
silently wrong.

## What is deliberately absent

Neither shell carries a copy of the runtime, a second config format, or
any identity logic. A shell that decided anything would be a second
place the identity lives.
