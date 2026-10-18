package packagefmt

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// A MEMBER THAT COULD NOT BE READ IS NOT A VERDICT ON THE PACKAGE. The
// read's own error comes back as it is — never a refusal of the file and
// never a check failed for good — so the attempt that met it is asked
// again; a member the package does not carry fails that check alone. The
// grammar itself is pinned by the shared vectors
// (internal/pluginhost/testdata/validation_checks.json).
func TestAMemberThatCouldNotBeReadIsNotAVerdict(t *testing.T) {
	file := []byte(`{"checks":[{"name":"echoes","sets":["*"],"lane":"operation","call":{"operation":"echo","arguments":{"$ref":"inputs.json#/echo"}},"expect":{"result_contains":"x"},"within_ms":1500}]}`)
	disk := errors.New("the disk did not answer")
	_, err := ParseValidation(file, func(string) ([]byte, bool, error) { return nil, false, disk })
	var refused *ValidationError
	if !errors.Is(err, disk) || errors.As(err, &refused) {
		t.Fatalf("a read that failed must come back as itself, not as a refusal: %v", err)
	}
	v, err := ParseValidation(file, func(string) ([]byte, bool, error) { return []byte(`{"echo":{"text":"harbor"}}`), true, nil })
	if err != nil || len(v.Checks) != 1 {
		t.Fatalf("read: %v", err)
	}
	c := v.Checks[0]
	if c.Unresolved != "" || c.Arguments["text"] != "harbor" || c.Within != 1500*time.Millisecond || !c.For("any-set") {
		t.Fatalf("the check as read: %+v", c)
	}
}

// A CHECK NAMES THE SETS IT IS FOR: the one named, and no other.
func TestACheckIsForTheSetsItNames(t *testing.T) {
	c := Check{Sets: []string{"full", "small"}}
	if !c.For("small") || c.For("other") {
		t.Fatal("a check must be for the sets it names and no other")
	}
}

// wavOf is a 16-bit PCM WAV of n silent sample groups at f.
func wavOf(rate, channels, n int) []byte {
	data := make([]byte, n*2*channels)
	return append(audio.WAVHeader(audio.Format{Rate: rate, Channels: channels}, uint32(len(data))), data...)
}

// A PACKAGE'S RECORDINGS ARE BOUNDED, EACH MEMBER COUNTED ONCE. Every start
// of a set sends them in real time, so the checks of one package carry at
// most MaxCheckAudioBytes of them: two members that come to more refuse the
// package; one member sent by two checks is counted once.
func TestTheRecordingsAPackageCarriesAreBoundedEachMemberOnce(t *testing.T) {
	big := wavOf(192000, 2, 1<<20) // 4 MiB of samples, 5.46 s
	check := func(name, member string) string {
		return `{"name":"` + name + `","sets":["*"],"lane":"session","input":{"audio":"` + member + `"},"expect":{"events":{"order":["input_finished"]}},"within_ms":10000}`
	}
	members := func(rel string) ([]byte, bool, error) {
		if rel == "a.wav" || rel == "b.wav" {
			return big, true, nil
		}
		return nil, false, nil
	}
	v, err := ParseValidation([]byte(`{"checks":[`+check("one", "a.wav")+`,`+check("two", "a.wav")+`]}`), members)
	if err != nil || len(v.Checks) != 2 || v.Checks[0].Unresolved != "" || v.Checks[0].Recording != time.Second*(1<<20)/192000 {
		t.Fatalf("one member sent twice is counted once: %v %+v", err, v)
	}
	_, err = ParseValidation([]byte(`{"checks":[`+check("one", "a.wav")+`,`+check("two", "b.wav")+`]}`), members)
	var refused *ValidationError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "recordings come to") {
		t.Fatalf("two members of more than %d bytes refuse the package: %v", MaxCheckAudioBytes, err)
	}
}

// A RECORDING THAT COULD NOT BE READ IS NOT A VERDICT, as any member: the
// read's own error comes back as it is.
func TestARecordingThatCouldNotBeReadIsNotAVerdict(t *testing.T) {
	disk := errors.New("the disk did not answer")
	_, err := ParseValidation([]byte(`{"checks":[{"name":"hears","sets":["*"],"lane":"session","input":{"audio":"a.wav"},"expect":{"events":{"order":["input_finished"]}},"within_ms":1000}]}`),
		func(string) ([]byte, bool, error) { return nil, false, disk })
	var refused *ValidationError
	if !errors.Is(err, disk) || errors.As(err, &refused) {
		t.Fatalf("a read that failed comes back as itself: %v", err)
	}
}
