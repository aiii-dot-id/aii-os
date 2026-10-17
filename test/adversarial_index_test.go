package test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// guard:tree
func TestAdversarialIndexNamesOnlyRealTests(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "docs", "ADVERSARIAL.md"))
	if err != nil {
		t.Fatal(err)
	}
	names := regexp.MustCompile("`(Test[A-Za-z0-9_]+)`").FindAllStringSubmatch(string(doc), -1)
	if len(names) < 20 {
		t.Fatalf("the index names only %d tests; it has lost rows", len(names))
	}
	defined := map[string]bool{}
	err = filepath.WalkDir(filepath.Join("..", "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`).FindAllStringSubmatch(string(src), -1) {
			defined[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range names {
		if !defined[m[1]] {
			t.Errorf("docs/ADVERSARIAL.md names %s, which no _test.go under internal/ defines", m[1])
		}
	}
}

// THE GUARDS THAT READ THE WHOLE TREE ARE IN THE RUNNER THAT RUNS THEM
// BEFORE A COMMIT (test/guards.sh). The runner lists no names — a guard
// joins it by carrying `// guard:tree` on the line above its func — so the
// one way a known guard could fall out of it is by losing that line. These
// are the ones that have already cost a red gate, or would have.
//
// guard:tree
func TestTheTreeGuardsCarryTheirMarker(t *testing.T) {
	root := repoRoot(t)
	for file, names := range map[string][]string{
		"internal/logsink/vocabulary_test.go":       {"TestEveryCategoryTheTreeNamesIsDeclared"},
		"internal/store/schema_ownership_test.go":   {"TestNoFutureDates", "TestSchemaSoleOwner"},
		"test/drills_grep_test.go":                  {"TestDrillsGrepCategoriesTheTreeStillEmits"},
		"test/adversarial_index_test.go":            {"TestAdversarialIndexNamesOnlyRealTests", "TestTheTreeGuardsCarryTheirMarker"},
		"test/atomicfile_contract_test.go":          {"TestNoCallerDiscardsPublished"},
		"test/layering_test.go":                     {"TestTheLayersPointDown", "TestTheSemanticCoreExcludesTheEngines", "TestTheLayeringGuardRefusesWhatItIsFor"},
		"test/static_runtime_test.go":               {"TestTheLinuxRuntimeLinksStatically", "TestTheStaticGuardRefusesAnyDynamicImport"},
		"internal/hostcap/execguard_test.go":        {"TestEveryExecSiteIsTopologyGated"},
		"internal/ledger/stream_test.go":            {"TestTheRuntimeNeverLoadsTheWholeLedger"},
		"internal/ring/ring0_immutable_test.go":     {"TestNoGeneralRing0WriteExistsAnywhereInTheTree"},
		"internal/untrusted/sentinel_owner_test.go": {"TestSentinelLiteralsHaveOneOwner"},
		"test/firstwords_integrity_test.go":         {"TestNoHardcodedAliveString"},
		"test/store_seams_test.go":                  {"TestNoProductionCodeReachesTheStoresTestSeams"},
		"test/error_prose_test.go":                  {"TestNoCodeDecidesByAnErrorsWording"},
		"test/app_seams_test.go":                    {"TestAppTestsRunOneAtATimeWhileTheyShareSeams"},
		"test/app_sync_fields_test.go":              {"TestAppTakesOnNoNewLockOrAtomic"},
		"test/app_context_roots_test.go":            {"TestAppRootsWorkOnBackgroundOnlyWhereListed", "TestTheContextRootGuardRefusesWhatItIsFor"},
		"test/platform_selection_test.go": {"TestFilenameSuffixNeverLeaksAcrossTheAliasRule",
			"TestEveryPlatformFileIsSelectedOnAShippedRow", "TestThePlatformSelectionGuardsRefuseWhatTheyAreFor"},
		"test/libc_lockstep_test.go":         {"TestLibcIsTheOneSQLiteWasBuiltAgainst", "TestTheLibcLockstepGuardRefusesWhatItIsFor"},
		"test/sql_owners_test.go":            {"TestOnlyTheStoresOwnersImportDatabaseSQL"},
		"internal/store/tree_writes_test.go": {"TestNoCodeOutsideTheStoreWritesItsTables"},
		"test/private_terms_test.go":         {"TestNoPrivateTermInTheTree", "TestThePrivateTermsGuardRefusesWhatItIsFor"},
		"test/decode_end_check_test.go":      {"TestADecodersMoreIsAskedOnlyInALoop", "TestTheDecodeEndGuardRefusesWhatItIsFor"},
	} {
		src, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if !strings.Contains(string(src), "// guard:tree\nfunc "+name+"(") {
				t.Errorf("%s: %s does not carry `// guard:tree` on the line above it — test/guards.sh no longer runs it before a commit", file, name)
			}
		}
	}
	// Executable where the bit exists. Windows keeps none (the script runs
	// there as `sh test/guards.sh`), so there it must be present.
	if info, err := os.Stat(filepath.Join(root, "test", "guards.sh")); err != nil || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
		t.Errorf("test/guards.sh is missing or not executable: %v", err)
	}
}
