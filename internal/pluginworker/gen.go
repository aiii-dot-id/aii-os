package pluginworker

// The testdata fixtures are generated, never hand-edited: wasmgen is
// their single source (see wasmgen's package doc for why the SDK
// toolchain and wat2wasm were not options). TestFixturesInSync fails
// the suite when a checked-in .wasm drifts from this source.
//go:generate go run ./testdata/gen -out testdata
