package test

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// NO PHONE BACKS UP AN IDENTITY BEHIND ITS OPERATOR. On a phone the app's
// files are the identity: its signing key, its record, its credentials.
// Both systems copy an app's files to a cloud backup and to a replacement
// phone by default, and neither shell said otherwise, so a restored phone
// would have been a second holder of one key. The shells are not built by
// this test, so what is held is what they declare: the Android manifest and
// its rules file (every domain, the device-protected ones too), and, in the
// iOS shell's source, the mark applied to the home's own URL before the
// runtime starts, with a start that cannot apply it refused.
func TestThePhoneShellsKeepTheIdentityOutOfSystemBackups(t *testing.T) {
	root := repoRoot(t)

	var manifest struct {
		Application struct {
			AllowBackup         string `xml:"http://schemas.android.com/apk/res/android allowBackup,attr"`
			FullBackupContent   string `xml:"http://schemas.android.com/apk/res/android fullBackupContent,attr"`
			DataExtractionRules string `xml:"http://schemas.android.com/apk/res/android dataExtractionRules,attr"`
		} `xml:"application"`
	}
	read := func(rel string, into any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if err := xml.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	read("shells/android/app/src/main/AndroidManifest.xml", &manifest)
	app := manifest.Application
	if app.AllowBackup != "false" || app.FullBackupContent != "false" || app.DataExtractionRules != "@xml/data_extraction_rules" {
		t.Fatalf("the Android shell lets the system back the identity up: allowBackup=%q fullBackupContent=%q dataExtractionRules=%q",
			app.AllowBackup, app.FullBackupContent, app.DataExtractionRules)
	}

	type exclude struct {
		Domain string `xml:"domain,attr"`
		Path   string `xml:"path,attr"`
	}
	var rules struct {
		Cloud    []exclude `xml:"cloud-backup>exclude"`
		Transfer []exclude `xml:"device-transfer>exclude"`
	}
	read("shells/android/app/src/main/res/xml/data_extraction_rules.xml", &rules)
	for name, section := range map[string][]exclude{"cloud-backup": rules.Cloud, "device-transfer": rules.Transfer} {
		excluded := map[string]bool{}
		for _, e := range section {
			if e.Path == "." {
				excluded[e.Domain] = true
			}
		}
		for _, domain := range []string{"root", "file", "database", "sharedpref", "external", "device_root", "device_file", "device_database", "device_sharedpref"} {
			if !excluded[domain] {
				t.Errorf("the Android rules leave %q in %s", domain, name)
			}
		}
	}

	swift, err := os.ReadFile(filepath.Join(root, "shells", "ios", "AIIOS", "AIIOSApp.swift"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(swift)
	if why := iosBackupMarkMissing(src); why != "" {
		t.Fatalf("the iOS shell does not keep the identity's directory out of backups before it starts the runtime: %s", why)
	}
	// The check is held to the three ways the source could be bent and
	// still read as marked: each bent copy must be refused.
	for _, bend := range []struct{ name, from, to string }{
		{"the mark is never applied", "try homeURL.setResourceValues(noBackup)\n", ""},
		{"a start that cannot set the mark starts anyway", "error.localizedDescription)\"))\n            return nil\n", "error.localizedDescription)\"))\n"},
		{"the mark is set on another directory", "URL(fileURLWithPath: home, isDirectory: true)", "URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)"},
		{"the mark is applied to another URL", "try homeURL.setResourceValues(noBackup)", "try otherURL.setResourceValues(noBackup)"},
	} {
		if strings.Count(src, bend.from) != 1 {
			t.Fatalf("bend %q: its text is not in the Swift source exactly once; bring the bend up to the source", bend.name)
		}
		if why := iosBackupMarkMissing(strings.Replace(src, bend.from, bend.to, 1)); why == "" {
			t.Errorf("bend %q: the check still calls the source held", bend.name)
		}
	}
}

// iosBackupMarkMissing says what the iOS shell's source lacks, or "" when,
// between the Documents home's definition and MobileStart(home + ...,
// home, ...), a URL made from that home has a resource value with
// isExcludedFromBackup = true applied to it by setResourceValues inside a
// do, whose catch says why and returns nil before any start.
func iosBackupMarkMissing(src string) string {
	src = regexp.MustCompile(`(?m)(^|\s)//.*$`).ReplaceAllString(src, "$1")
	start := strings.Index(src, "MobileStart(")
	if start < 0 {
		return "no MobileStart( call"
	}
	defs := regexp.MustCompile(`let\s+(\w+)\s*=\s*NSSearchPathForDirectoriesInDomains\(\s*\.documentDirectory\b`).FindAllStringSubmatchIndex(src[:start], -1)
	if len(defs) == 0 {
		return "no Documents home defined before MobileStart("
	}
	def := defs[len(defs)-1]
	home := src[def[2]:def[3]]
	if !regexp.MustCompile(`^MobileStart\(\s*` + home + `\s*\+\s*"/config\.json"\s*,\s*` + home + `\s*,`).MatchString(src[start:]) {
		return "MobileStart( is not given the Documents home " + strconv.Quote(home)
	}
	between := src[def[1]:start]
	url := regexp.MustCompile(`var\s+(\w+)\s*=\s*URL\(\s*fileURLWithPath:\s*` + home + `\s*[,)]`).FindStringSubmatchIndex(between)
	if url == nil {
		return "no URL made from the Documents home " + strconv.Quote(home)
	}
	urlVar := between[url[2]:url[3]]
	mark := regexp.MustCompile(`(\w+)\.isExcludedFromBackup\s*=\s*true`).FindStringSubmatchIndex(between)
	if mark == nil {
		return "no resource value with isExcludedFromBackup = true"
	}
	values := between[mark[2]:mark[3]]
	set := regexp.MustCompile(`do\s*\{\s*try\s+` + urlVar + `\.setResourceValues\(\s*` + values + `\s*\)\s*\}\s*catch\s*\{`).FindStringIndex(between)
	if set == nil {
		return "the home's URL " + strconv.Quote(urlVar) + " is not given " + strconv.Quote(values) + " by setResourceValues( inside a do with a catch"
	}
	if url[1] > set[0] || mark[1] > set[0] {
		return "setResourceValues( comes before the URL or the mark it applies"
	}
	depth, end := 1, -1
	for i := set[1]; i < len(between) && end < 0; i++ {
		switch between[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		return "the catch is not closed before MobileStart("
	}
	catch := between[set[1]:end]
	if !strings.Contains(catch, "could not be kept out of this phone's backups") {
		return "the catch does not say why the identity was not started"
	}
	if !regexp.MustCompile(`(?m)^\s*return\s+nil\s*$`).MatchString(catch) {
		return "the catch does not return nil, so a start that cannot set the mark starts anyway"
	}
	return ""
}
