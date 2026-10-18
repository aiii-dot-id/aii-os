package test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// guard:tree
func TestAndroidWrapperPinsAChecksummedStableDistribution(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "shells", "android", "gradle", "wrapper", "gradle-wrapper.properties"))
	if err != nil {
		t.Fatal(err)
	}
	properties := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			properties[key] = strings.ReplaceAll(value, `\:`, ":")
		}
	}
	if !regexp.MustCompile(`^https://services\.gradle\.org/distributions/gradle-[0-9]+\.[0-9]+\.[0-9]+-bin\.zip$`).MatchString(properties["distributionUrl"]) {
		t.Fatal("wrapper must name one stable upstream binary distribution")
	}
	digest, err := hex.DecodeString(properties["distributionSha256Sum"])
	if err != nil || len(digest) != 32 {
		t.Fatal("wrapper must verify the distribution SHA-256")
	}
	if properties["validateDistributionUrl"] != "true" {
		t.Fatal("wrapper URL validation was disabled")
	}
}
