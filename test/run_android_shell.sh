#!/bin/sh
# Build the Android shell from this tree as a phone gets it, and run its
# unit tests: the Go core bound with gomobile for android/arm64, then the
# Kotlin shell assembled against that binding.
#
# The matrix compiles the Go packages for android/arm64, and nothing more:
# a change to what mobile/ exports, or to the shell, could leave the shell
# unable to compile against its binding and every gate green. The phones
# are a release target, so the gate builds the shell. The iPhone shell
# needs Xcode and
# is built on the Mac by the native run, not here.
#
# Nothing is written into the checkout: the binding is made into a
# temporary directory, and the shell is copied there and built there.
# Gradle runs --offline: a gate fetches nothing, so the wrapper's
# distribution and the shell's dependencies must already be in the Gradle
# cache of the user the gate runs as (a first build with the network fills
# it).
#
# ANDROID_HOME names the SDK (default $HOME/Android/Sdk) and
# ANDROID_NDK_HOME one NDK (default the newest under $ANDROID_HOME/ndk).
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/toolchain.sh"
cd "$script_dir/.."

ANDROID_HOME=${ANDROID_HOME:-$HOME/Android/Sdk}
[ -d "$ANDROID_HOME" ] || { echo "ANDROID FAIL: no Android SDK at $ANDROID_HOME; set ANDROID_HOME"; exit 1; }
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
	ANDROID_NDK_HOME=$(ls -d "$ANDROID_HOME"/ndk/*/ 2>/dev/null | sort -V | tail -1)
	ANDROID_NDK_HOME=${ANDROID_NDK_HOME%/}
fi
[ -n "$ANDROID_NDK_HOME" ] && [ -d "$ANDROID_NDK_HOME" ] || { echo "ANDROID FAIL: no NDK under $ANDROID_HOME/ndk; set ANDROID_NDK_HOME"; exit 1; }
export ANDROID_HOME ANDROID_NDK_HOME
echo "sdk $ANDROID_HOME, ndk $ANDROID_NDK_HOME, $("$GO" version)"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# gomobile runs gobind from PATH. Both are built here from the module's own
# pins (go.mod's tool block), as staticcheck and deadcode are, so the stage
# never depends on whatever gobind a user's PATH happens to hold.
mkdir "$work/bin"
"$GO" build -o "$work/bin/" golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
PATH="$work/bin:$PATH"; export PATH
gomobile bind -target=android/arm64 -androidapi 26 -o "$work/aiios.aar" ./mobile ||
	{ echo "ANDROID FAIL: mobile/ did not bind for android/arm64"; exit 1; }
cp -R shells/android "$work/android"
mkdir -p "$work/android/app/libs"
cp "$work/aiios.aar" "$work/android/app/libs/aiios.aar"
(cd "$work/android" && ./gradlew --offline --no-daemon assembleDebug testDebugUnitTest) ||
	{ echo "ANDROID FAIL: the shell did not build against this tree's binding, or its unit tests failed"; exit 1; }
echo "android shell: built and its unit tests pass"
