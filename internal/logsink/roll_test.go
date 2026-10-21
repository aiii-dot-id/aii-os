package logsink

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

// fillLive writes n marked lines through the logger, as the identity
// does: at debug, which the file takes and the console does not.
func fillLive(n int) {
	pad := strings.Repeat("x", 80)
	for i := 0; i < n; i++ {
		Debug("logs.pass", "line-%03d %s", i, pad)
	}
}

// THE LIVE LOG ROLLS AT ITS SIZE WITHIN A BOOT. It was opened once a boot
// and rotated only at the next one, so whatever wrote to it without end
// grew one file until the disk was full. Rolled, it is the rotation a
// start makes: the same names beside the live file, every line in one of
// them and none twice, and no file much past the size.
func TestTheLiveLogRollsAtItsSizeWithinABoot(t *testing.T) {
	const limit, lines = 2000, 100
	s := newTestSink(t, Config{MaxBytes: limit, MaxBackups: -1})
	fillLive(lines)
	// A roll is said in the log, by the file that continues the one
	// rolled: a reader of a live log that begins in the middle of a run
	// is told why and where the lines before it are.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(everyLogByte(t, s.Dir()), "(logs.max_bytes) and was rolled to "+rotatedPrefix) {
		if time.Now().After(deadline) {
			t.Fatalf("no roll was said in the log:\n%s", everyLogByte(t, s.Dir()))
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Close() // every roll's retention pass is done when this returns

	names := rotatedNames(t, s.Dir())
	if len(names) < 5 {
		t.Fatalf("THE LIVE LOG DID NOT ROLL: %d lines of over 100 bytes at a size of %d left %d rotated file(s): %v", lines, limit, len(names), names)
	}
	seen := map[string]int{}
	for _, name := range append(names, LiveName) {
		data, err := os.ReadFile(filepath.Join(s.Dir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if name != LiveName && (len(data) < limit || len(data) > limit+512) {
			t.Errorf("%s holds %d bytes: a rolled file is the size and at most a line more", name, len(data))
		}
		if name == LiveName && len(data) >= limit {
			t.Errorf("the live log holds %d bytes after its rolls, at or past its size of %d", len(data), limit)
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			t.Errorf("%s does not end on a whole line", name)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if i := strings.Index(line, "line-"); i >= 0 {
				seen[line[i:i+8]]++
			}
		}
	}
	for i := 0; i < lines; i++ {
		if n := seen[fmt.Sprintf("line-%03d", i)]; n != 1 {
			t.Errorf("line %d is in the files %d time(s), want once", i, n)
		}
	}
	if _, err := s.Tail(names[0], 0); err != nil {
		t.Errorf("a rolled file is not one the viewer reads: %v", err)
	}
}

// everyLogByte is the rotated logs and the live one, read whole.
func everyLogByte(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	for _, name := range append(rotatedNames(t, dir), LiveName) {
		// Read as the viewer opens, so the read holds no roll off; a file
		// rolled between the listing and the read is in the next reading.
		data, err := atomicfile.ReadFile(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String()
}

// ONE RETENTION. The pass a start runs is the pass a roll runs, so the
// count that bounds rotated logs bounds rolled ones (here with the day
// floor off: inside the floor every rotated log is kept, rolled or not).
func TestARollRunsTheRetentionAStartRuns(t *testing.T) {
	s := newTestSink(t, Config{MaxBytes: 2000, MaxBackups: 2, MaxDays: -1})
	fillLive(100)
	s.Close()
	if names := rotatedNames(t, s.Dir()); len(names) != 2 {
		t.Fatalf("at least five rolls with max_backups 2 and no day floor left %d rotated file(s): %v", len(names), names)
	}
}

// A ROLL THAT CANNOT BE MADE LOSES NOTHING AND IS NOT TRIED AT EVERY
// LINE. The lines go on into the file that is open, the failure is told
// to the sink, and the next try comes when as many bytes again are
// written.
func TestARollThatCannotBeMadeKeepsWritingAndTriesAgainASizeLater(t *testing.T) {
	dir := t.TempDir()
	open := filepath.Join(dir, "open.log")
	f, err := os.OpenFile(open, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	l := &liveLog{
		// A name under a file: nothing can be rotated or opened there.
		path: filepath.Join(open, LiveName), f: f, limit: 1000,
		rolled: func(size int64, name string, err error) {
			if err == nil || name != "" {
				t.Errorf("a roll under a file was reported as made: name %q, err %v", name, err)
			}
			if size < 1000 {
				t.Errorf("a roll was tried at %d bytes, under the size", size)
			}
			failures++
		},
	}
	line := []byte(strings.Repeat("y", 99) + "\n")
	for i := 0; i < 30; i++ {
		if n, err := l.Write(line); err != nil || n != len(line) {
			t.Fatalf("write %d after a roll that could not be made: %d, %v", i, n, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if failures != 3 {
		t.Fatalf("3000 bytes at a size of 1000 tried the roll %d time(s), want 3: once a size, not once a line", failures)
	}
	data, err := os.ReadFile(open)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 30*len(line) {
		t.Fatalf("the open file holds %d bytes of the %d written", len(data), 30*len(line))
	}
	if _, err := l.Write(line); err == nil {
		t.Fatal("a closed live log took a write")
	}
}

// THE LOGS OF ONE SECOND ARE KEPT AND EVICTED IN THE ORDER THEY WERE MADE.
// A live log that rolls within a boot makes several rotated logs a second
// under a flood, which a start never did: the second's first carries the
// bare timestamp and the later ones -1, -2 and on. Sorted as strings the
// later ones came first ("-" sorts before ".", and "-10" before "-2"), so
// the count evicted the newest of a second and kept its oldest; and their
// names read as carrying no time, so the day floor did not keep them at
// all.
func TestTheLogsOfOneSecondAreEvictedOldestFirstAndKeptByTheDayFloor(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Now().UTC().Format(rotatedStampLayout)
	made := []string{rotatedPrefix + stamp + rotatedExt}
	for n := 1; n <= 11; n++ {
		made = append(made, fmt.Sprintf("%s%s-%d%s", rotatedPrefix, stamp, n, rotatedExt))
	}
	for _, name := range made {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if got := rotatedNames(t, dir); !slices.Equal(got, made) {
		t.Fatalf("THE LOGS OF ONE SECOND ARE NOT LISTED IN THE ORDER THEY WERE MADE:\n got %v\nwant %v", got, made)
	}
	for _, name := range made {
		if at, ok := rotatedAt(name); !ok || at.Format(rotatedStampLayout) != stamp {
			t.Fatalf("%s reads as carrying the time %v (%v), want its second's", name, at, ok)
		}
	}

	// Inside the day floor the count evicts none of them.
	if removed, err := (&Sink{cfg: Config{MaxBackups: 3}, dir: dir}).Prune(); err != nil || removed != 0 {
		t.Fatalf("THE DAY FLOOR DID NOT KEEP A SECOND'S LATER LOGS: %d removed (%v), left %v", removed, err, rotatedNames(t, dir))
	}
	// With the floor off the count keeps the three made last.
	if _, err := (&Sink{cfg: Config{MaxBackups: 3, MaxDays: -1}, dir: dir}).Prune(); err != nil {
		t.Fatal(err)
	}
	if got, want := rotatedNames(t, dir), made[len(made)-3:]; !slices.Equal(got, want) {
		t.Fatalf("THE COUNT EVICTED A SECOND'S LOGS OUT OF ORDER:\n kept %v\nwant %v", got, want)
	}

	// And the next log of that second takes the place after the highest
	// left, not the first name free: the bare timestamp is free again, and
	// a log given it would sort as the second's oldest.
	live := filepath.Join(dir, LiveName)
	if err := os.WriteFile(live, []byte("the newest\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	at, _ := rotatedAt(made[0])
	if err := os.Chtimes(live, at, at); err != nil {
		t.Fatal(err)
	}
	name, err := rotateIfPresent(live, nil)
	if want := fmt.Sprintf("%s%s-12%s", rotatedPrefix, stamp, rotatedExt); err != nil || name != want {
		t.Fatalf("the next log of the second was named %q (%v), want %q", name, err, want)
	}
	if got := rotatedNames(t, dir); got[len(got)-1] != name {
		t.Fatalf("the log made last is not listed last: %v", got)
	}
}

// A roll renames the file it holds and never removes it, whatever the
// path says of its size: the handle is asked, and it is the roll's own.
func TestARollNeverRemovesTheLiveLogItHolds(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, LiveName)
	f, err := atomicfile.OpenAppendRemovable(live, 0o640) // as the sink holds it
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Held and empty: a start removes such a file; a roll, which holds it,
	// gives it its name.
	name, err := rotateIfPresent(live, f)
	if err != nil || name == "" {
		t.Fatalf("a roll of a held, empty live log gave it no name: %q, %v", name, err)
	}
	if got := rotatedNames(t, dir); len(got) != 1 || got[0] != name {
		t.Fatalf("the held log is not in the directory under its name %q: %v", name, got)
	}
	// Not held (a start): the empty file is removed, as it always was.
	if err := os.WriteFile(live, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if name, err := rotateIfPresent(live, nil); err != nil || name != "" {
		t.Fatalf("a start kept an empty live log as %q (%v)", name, err)
	}
	// A handle that is not the file at the path decides nothing for it.
	if err := os.WriteFile(live, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if name, err := rotateIfPresent(live, f); err != nil || name != "" {
		t.Fatalf("a roll took another file at the name for its own: %q, %v", name, err)
	}
}

// The default is one constant, held to the two bounds beside it: a
// configuration may name it, and a rolled file of that size is still one
// the viewer will read once it is compressed.
func TestTheDefaultRollSizeStandsBetweenItsBounds(t *testing.T) {
	if got := (Config{}).maxBytes(); got != DefaultMaxBytes {
		t.Errorf("no size named resolves to %d, want the default %d", got, int64(DefaultMaxBytes))
	}
	if got := (Config{MaxBytes: 3 << 20}).maxBytes(); got != 3<<20 {
		t.Errorf("a named size resolves to %d", got)
	}
	if DefaultMaxBytes < MinMaxBytes {
		t.Errorf("the default %d is under the least a configuration may name, %d", int64(DefaultMaxBytes), int64(MinMaxBytes))
	}
	if DefaultMaxBytes > MaxMaxBytes {
		t.Errorf("the default %d is over the most a configuration may name, %d", int64(DefaultMaxBytes), int64(MaxMaxBytes))
	}
	// The most a configuration may name is the viewer's own bound halved,
	// so a rolled file and the line that reached its size are read
	// through once compressed.
	if MaxMaxBytes != maxGunzipBytes/2 || MaxMaxBytes <= MinMaxBytes {
		t.Errorf("the most a configuration may name is %d: it is to be half the viewer's bound %d and over the least, %d", int64(MaxMaxBytes), int64(maxGunzipBytes), int64(MinMaxBytes))
	}
}

// A LIVE LOG REMOVED FROM UNDER THE SINK IS OPENED AGAIN AT THE NEXT ROLL.
// Where someone deletes aii.log while the identity runs, the roll finds no
// file at the name to rotate: it opens a fresh one there and tells the
// sink so, and the lines after it go into the file at the name, not into
// one nobody can see any more.
func TestALiveLogRemovedFromUnderTheSinkIsOpenedAgainAtTheNextRoll(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a file this process holds open is not removed from its name on Windows as it is here; the case is the unix one")
	}
	dir := t.TempDir()
	live := filepath.Join(dir, LiveName)
	f, err := atomicfile.OpenAppendRemovable(live, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	var rolls []string
	l := &liveLog{path: live, f: f, limit: 1000, rolled: func(size int64, name string, err error) {
		if err != nil {
			t.Errorf("the roll after a removal failed: %v", err)
		}
		rolls = append(rolls, name)
	}}
	t.Cleanup(func() { _ = l.Close() })
	line := []byte(strings.Repeat("z", 99) + "\n")
	write := func(n int) {
		for i := 0; i < n; i++ {
			if _, err := l.Write(line); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(5)
	if err := os.Remove(live); err != nil {
		t.Fatal(err)
	}
	write(5) // the size is reached: the roll finds nothing at the name
	if len(rolls) != 1 || rolls[0] != "" {
		t.Fatalf("the roll after a removal was told as %q, want one roll with nothing to rotate", rolls)
	}
	write(3)
	data, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("A LIVE LOG REMOVED FROM UNDER THE SINK WAS NOT OPENED AGAIN AT THE ROLL: %v", err)
	}
	if len(data) != 3*len(line) {
		t.Fatalf("the file at the name holds %d bytes, want the %d written after the roll", len(data), 3*len(line))
	}
}

// THE FOLDER IS HELD UNDER ITS BOUND INSIDE THE DAY FLOOR. A
// stream that never ends rolls the live log at its size, and the day floor
// keeps every rotated log younger than MaxDays whatever MaxBackups says,
// so the rolled files filled the disk instead of one file. With a bound on
// the rotated logs together the oldest go while they sum above it, inside
// the floor too, the live log is never one of them, and the retention says
// what went (TestTheEvictionIsSaidInTheLogItBounds). The control half:
// with the same flood and no bound smaller than the floor's, the floor
// alone keeps every rolled file.
func TestAFloodIsHeldUnderTheFoldersBoundInsideTheDayFloor(t *testing.T) {
	const limit, bound = 2000, 7000
	s := newTestSink(t, Config{MaxBytes: limit, MaxBackups: 2, MaxTotalBytes: bound})
	fillLive(300) // about 30,000 bytes: fifteen rolls, all inside the day floor
	s.Close()
	names := rotatedNames(t, s.Dir())
	var sum int64
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(s.Dir(), n))
		if err != nil {
			t.Fatal(err)
		}
		sum += fi.Size()
	}
	if sum > bound {
		t.Fatalf("THE FOLDER'S ROTATED LOGS SUM %d BYTES, OVER THE BOUND %d, inside the day floor: %v", sum, int64(bound), names)
	}
	if len(names) < 2 {
		t.Fatalf("the bound evicted more than it needed: %d rotated left (%v), each about %d bytes under a bound of %d", len(names), names, limit, bound)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), LiveName)); err != nil {
		t.Fatalf("the live log was evicted: %v", err)
	}

	// The control: the day floor alone (no bound under what was written)
	// keeps every rolled file, more than the count names.
	open := newTestSink(t, Config{MaxBytes: limit, MaxBackups: 2, MaxTotalBytes: 1 << 30})
	fillLive(300)
	open.Close()
	if kept := rotatedNames(t, open.Dir()); len(kept) <= 2 {
		t.Fatalf("control: the day floor alone should keep every rolled file of the flood, kept %d", len(kept))
	}
}

// The default bound is as many full files as the count keeps and one more,
// and nothing is bounded while everything is kept.
func TestTheFoldersDefaultBoundFollowsTheRollSizeAndTheCount(t *testing.T) {
	if got := (Config{}).maxTotalBytes(); got != DefaultMaxBytes*10 {
		t.Fatalf("at the defaults the bound is %d, want 5 MiB times ten, %d", got, int64(DefaultMaxBytes*10))
	}
	if got := DefaultMaxTotalBytes(2<<20, 4); got != 10<<20 {
		t.Fatalf("for 2 MiB and four backups the bound is %d, want 10 MiB", got)
	}
	if got := DefaultMaxTotalBytes(0, -1); got != 0 {
		t.Fatalf("with every log kept the page is told a bound of %d, want none", got)
	}
	if got := (Config{MaxTotalBytes: 123456}).maxTotalBytes(); got != 123456 {
		t.Fatalf("a bound named is %d, want it", got)
	}
}

// "KEEP EVERYTHING" HAS NO DEFAULT BOUND, AND A BOUND NAMED STILL HOLDS.
// With max_backups negative the count and the day floor evict nothing and
// the default bound is none; a bound the operator names was ignored there
// and now holds.
func TestABoundNamedHoldsWhileEveryLogIsKept(t *testing.T) {
	kept := newTestSink(t, Config{MaxBytes: 2000, MaxBackups: -1})
	fillLive(300)
	kept.Close()
	if n := len(rotatedNames(t, kept.Dir())); n < 10 {
		t.Fatalf("with every log kept and no bound named, %d rotated logs are left of about fifteen", n)
	}
	bounded := newTestSink(t, Config{MaxBytes: 2000, MaxBackups: -1, MaxTotalBytes: 7000})
	fillLive(300)
	bounded.Close()
	var sum int64
	for _, n := range rotatedNames(t, bounded.Dir()) {
		fi, err := os.Stat(filepath.Join(bounded.Dir(), n))
		if err != nil {
			t.Fatal(err)
		}
		sum += fi.Size()
	}
	if sum > 7000 {
		t.Fatalf("a bound named while every log is kept was ignored: the rotated logs sum %d", sum)
	}
}

// THE EVICTION IS SAID IN THE LOG IT BOUNDS. A start runs the retention
// after the live log is open, so what it removed is the first thing the
// fresh file says. (Under a flood the line can land in a file a later
// eviction removes, so it is read here, where nothing rolls after it.)
func TestTheEvictionIsSaidInTheLogItBounds(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Now().UTC().Format(rotatedStampLayout)
	for n := 0; n < 5; n++ {
		name := rotatedPrefix + stamp + rotatedExt
		if n > 0 {
			name = fmt.Sprintf("%s%s-%d%s", rotatedPrefix, stamp, n, rotatedExt)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x", 1000)), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestSink(t, Config{Dir: dir, MaxBytes: 1 << 20, MaxBackups: 9, MaxTotalBytes: 2500})
	s.Close()
	live, err := os.ReadFile(filepath.Join(dir, LiveName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(live), "went to hold the rotated logs under 2500 bytes (logs.max_total_bytes)") {
		t.Fatalf("the eviction was not said in the fresh live log:\n%s", live)
	}
	var sum int64
	for _, n := range rotatedNames(t, dir) {
		fi, _ := os.Stat(filepath.Join(dir, n))
		sum += fi.Size()
	}
	if sum > 2500 {
		t.Fatalf("the start's retention left %d bytes of rotated logs over a bound of 2500", sum)
	}
}
