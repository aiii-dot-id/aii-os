//go:build linux

package pluginhost

import (
	"encoding/binary"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// call is one system call as the filter sees it.
type call struct {
	arch uint32
	nr   uint32
	arg1 uint64
	arg0 uint64
}

// runFilter is the kernel's filter machine for the instructions this
// program uses, over the same record the kernel hands it. An instruction
// it does not know, a jump out of the program or a program that runs off
// its end is a failed test: the kernel would refuse to load such a filter,
// and a child whose filter does not load is never run.
func runFilter(t *testing.T, prog []byte, c call) uint32 {
	t.Helper()
	if len(prog) == 0 || len(prog)%8 != 0 {
		t.Fatalf("the filter is %d bytes; it is an array of 8-byte instructions", len(prog))
	}
	record := make([]byte, 64)
	binary.NativeEndian.PutUint32(record[0:], c.nr)
	binary.NativeEndian.PutUint32(record[4:], c.arch)
	binary.NativeEndian.PutUint64(record[16:], c.arg0)
	binary.NativeEndian.PutUint64(record[24:], c.arg1)
	var acc uint32
	for pc := 0; pc < len(prog)/8; pc++ {
		in := prog[8*pc:]
		code, jt, jf, k := binary.NativeEndian.Uint16(in), int(in[2]), int(in[3]), binary.NativeEndian.Uint32(in[4:])
		switch code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			if k%4 != 0 || int(k)+4 > len(record) {
				t.Fatalf("instruction %d loads outside the record: offset %d", pc, k)
			}
			acc = binary.NativeEndian.Uint32(record[k:])
		case unix.BPF_ALU | unix.BPF_AND | unix.BPF_K:
			acc &= k
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K:
			if acc == k {
				pc += jt
			} else {
				pc += jf
			}
		case unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K:
			if acc >= k {
				pc += jt
			} else {
				pc += jf
			}
		case unix.BPF_RET | unix.BPF_K:
			return k
		default:
			t.Fatalf("instruction %d has code %#x, which this program should not contain", pc, code)
		}
	}
	t.Fatal("the filter ran off its end without a verdict")
	return 0
}

// THE FILTER'S VERDICTS, CALL BY CALL. The from-inside test
// (TestAContainedNativeChildMakesNoSocket) shows the kernel keeping these
// under bubblewrap; this one holds the program itself to every row, the
// foreign-ABI rows included, which no 64-bit test child can provoke.
func TestTheSystemCallFilterRefusesTheWaysOutAndNothingElse(t *testing.T) {
	prog, err := syscallFilter(false)
	if err != nil {
		t.Skipf("no filter is written for this machine, and no native child runs on it: %v", err)
	}
	native := map[string]uint32{"amd64": unix.AUDIT_ARCH_X86_64, "arm64": unix.AUDIT_ARCH_AARCH64}[runtime.GOARCH]
	allow := uint32(unix.SECCOMP_RET_ALLOW)
	errno := func(e unix.Errno) uint32 { return unix.SECCOMP_RET_ERRNO | uint32(e) }
	rows := []struct {
		what string
		c    call
		want uint32
	}{
		{"socket, family and type unset", call{arch: native, nr: unix.SYS_SOCKET}, errno(unix.EACCES)},
		{"socket, a local stream", call{arch: native, nr: unix.SYS_SOCKET, arg0: unix.AF_UNIX, arg1: unix.SOCK_STREAM}, errno(unix.EACCES)},
		// The one kind a CUDA child may make is refused to everyone else.
		{"socket, a local packet sequence", call{arch: native, nr: unix.SYS_SOCKET, arg0: unix.AF_UNIX, arg1: unix.SOCK_SEQPACKET | unix.SOCK_CLOEXEC}, errno(unix.EACCES)},
		{"socketpair", call{arch: native, nr: unix.SYS_SOCKETPAIR}, errno(unix.EACCES)},
		{"io_uring_setup", call{arch: native, nr: unix.SYS_IO_URING_SETUP}, errno(unix.ENOSYS)},
		{"keyctl", call{arch: native, nr: unix.SYS_KEYCTL}, errno(unix.ENOSYS)},
		{"add_key", call{arch: native, nr: unix.SYS_ADD_KEY}, errno(unix.ENOSYS)},
		{"request_key", call{arch: native, nr: unix.SYS_REQUEST_KEY}, errno(unix.ENOSYS)},
		{"ioctl TIOCSTI", call{arch: native, nr: unix.SYS_IOCTL, arg1: unix.TIOCSTI}, errno(unix.EPERM)},
		{"ioctl TIOCLINUX", call{arch: native, nr: unix.SYS_IOCTL, arg1: unix.TIOCLINUX}, errno(unix.EPERM)},
		// The kernel reads an ioctl's request as 32 bits; a caller cannot
		// hide one behind a high half.
		{"ioctl TIOCSTI with a high half", call{arch: native, nr: unix.SYS_IOCTL, arg1: 0xdead_0000_0000_0000 | unix.TIOCSTI}, errno(unix.EPERM)},
		{"ioctl TCGETS", call{arch: native, nr: unix.SYS_IOCTL, arg1: unix.TCGETS}, allow},
		{"read", call{arch: native, nr: unix.SYS_READ}, allow},
		{"openat", call{arch: native, nr: unix.SYS_OPENAT}, allow},
		{"execve", call{arch: native, nr: unix.SYS_EXECVE}, allow},
		{"clone", call{arch: native, nr: unix.SYS_CLONE}, allow},
		{"mmap", call{arch: native, nr: unix.SYS_MMAP}, allow},
		// A descriptor it was handed is used as before: these need a
		// socket to act on, and there is none to be had.
		{"connect", call{arch: native, nr: unix.SYS_CONNECT}, allow},
		{"sendto", call{arch: native, nr: unix.SYS_SENDTO}, allow},
		// A foreign ABI is refused whole, whatever the number means there.
		{"i386, any call", call{arch: unix.AUDIT_ARCH_I386, nr: 102}, errno(unix.ENOSYS)},
		{"i386, a call that is read natively", call{arch: unix.AUDIT_ARCH_I386, nr: unix.SYS_READ}, errno(unix.ENOSYS)},
		{"arm, any call", call{arch: unix.AUDIT_ARCH_ARM, nr: 281}, errno(unix.ENOSYS)},
	}
	if runtime.GOARCH == "amd64" {
		rows = append(rows,
			struct {
				what string
				c    call
				want uint32
			}{"x32 socket", call{arch: native, nr: x32Bit | unix.SYS_SOCKET}, errno(unix.ENOSYS)},
			struct {
				what string
				c    call
				want uint32
			}{"x32 read", call{arch: native, nr: x32Bit | unix.SYS_READ}, errno(unix.ENOSYS)})
	}
	for _, row := range rows {
		if got := runFilter(t, prog, row.c); got != row.want {
			t.Errorf("%s: the filter answers %#x, want %#x", row.what, got, row.want)
		}
	}
	// The kernel loads at most 4096 instructions and bubblewrap reads the
	// program from a pipe the supervisor fills in one write.
	if len(prog) > 4096 {
		t.Errorf("the filter is %d bytes; a pipe takes 4096 unread", len(prog))
	}
}

// THE ONE SOCKET A CUDA CHILD MAY MAKE, AND NOTHING ELSE BY IT. CUDA cannot
// start without two packet-sequenced local sockets of its own (the
// filter's comment has the measurement), so a child whose signed backend
// names CUDA gets a filter that admits socket(AF_UNIX, SOCK_SEQPACKET)
// with whatever flags ride above the type. Every other row of the table
// is the same as everyone's: a stream or a datagram, which is what a
// service on the file system is, another family in a packet sequence, a
// pair, a ring, the keyrings, the terminal requests, the foreign ABIs.
func TestACUDAChildsFilterAdmitsOnePacketSequencedLocalSocket(t *testing.T) {
	plain, err := syscallFilter(false)
	if err != nil {
		t.Skipf("no filter is written for this machine, and no native child runs on it: %v", err)
	}
	prog, err := syscallFilter(true)
	if err != nil {
		t.Fatal(err)
	}
	native := map[string]uint32{"amd64": unix.AUDIT_ARCH_X86_64, "arm64": unix.AUDIT_ARCH_AARCH64}[runtime.GOARCH]
	allow := uint32(unix.SECCOMP_RET_ALLOW)
	errno := func(e unix.Errno) uint32 { return unix.SECCOMP_RET_ERRNO | uint32(e) }
	socket := func(family, kind uint64) call {
		return call{arch: native, nr: unix.SYS_SOCKET, arg0: family, arg1: kind}
	}
	admitted := []call{
		socket(unix.AF_UNIX, unix.SOCK_SEQPACKET),
		socket(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC),
		socket(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK),
	}
	for _, c := range admitted {
		if got := runFilter(t, prog, c); got != allow {
			t.Errorf("a CUDA child's socket(%d, %#x) is answered %#x, want it allowed", c.arg0, c.arg1, got)
		}
		if got := runFilter(t, plain, c); got != errno(unix.EACCES) {
			t.Errorf("a child with no CUDA backend is allowed socket(%d, %#x): %#x", c.arg0, c.arg1, got)
		}
	}
	refused := map[string]call{
		"a local stream":                   socket(unix.AF_UNIX, unix.SOCK_STREAM),
		"a local stream with flags":        socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC),
		"a local datagram":                 socket(unix.AF_UNIX, unix.SOCK_DGRAM),
		"a packet sequence on the network": socket(unix.AF_INET, unix.SOCK_SEQPACKET),
		"a netlink socket":                 socket(unix.AF_NETLINK, unix.SOCK_RAW),
		"a pair":                           {arch: native, nr: unix.SYS_SOCKETPAIR, arg0: unix.AF_UNIX, arg1: unix.SOCK_SEQPACKET},
	}
	for what, c := range refused {
		if got := runFilter(t, prog, c); got != errno(unix.EACCES) {
			t.Errorf("a CUDA child's filter answers %s with %#x, want it refused", what, got)
		}
	}
	// Every row that is not socket() is answered as the plain filter
	// answers it: the exception is one call wide.
	for _, c := range []call{
		{arch: native, nr: unix.SYS_IO_URING_SETUP}, {arch: native, nr: unix.SYS_KEYCTL}, {arch: native, nr: unix.SYS_ADD_KEY}, {arch: native, nr: unix.SYS_REQUEST_KEY},
		{arch: native, nr: unix.SYS_IOCTL, arg1: unix.TIOCSTI}, {arch: native, nr: unix.SYS_IOCTL, arg1: unix.TIOCLINUX}, {arch: native, nr: unix.SYS_IOCTL, arg1: unix.TCGETS},
		{arch: native, nr: unix.SYS_READ}, {arch: native, nr: unix.SYS_CONNECT}, {arch: native, nr: unix.SYS_BIND}, {arch: native, nr: unix.SYS_LISTEN},
		{arch: unix.AUDIT_ARCH_I386, nr: 102}, {arch: native, nr: x32Bit | unix.SYS_SOCKET, arg0: unix.AF_UNIX, arg1: unix.SOCK_SEQPACKET},
	} {
		if runtime.GOARCH != "amd64" && c.nr&x32Bit != 0 {
			continue
		}
		if got, want := runFilter(t, prog, c), runFilter(t, plain, c); got != want {
			t.Errorf("call %d (arch %#x, arg1 %#x): a CUDA child is answered %#x and everyone else %#x", c.nr, c.arch, c.arg1, got, want)
		}
	}
	// Which children get it: a backend with a "cuda" part, and no other.
	for backend, want := range map[string]bool{"cuda": true, "cuda+vulkan": true, "vulkan+cuda": true, "": false, "cpu": false, "vulkan": false, "cpu+vulkan": false, "metal": false, "barracuda": false} {
		if got := localPackets(&AcceleratorProfile{Backend: backend}); got != want {
			t.Errorf("backend %q: packet-sequenced local sockets allowed = %v, want %v", backend, got, want)
		}
	}
	if localPackets(nil) {
		t.Error("a child with no profile is given CUDA's sockets")
	}
}

// The argv names the descriptor the supervisor fills, counted after the
// ones the spawn already hands the child, and the containment carries the
// same program the argv asks for.
func TestTheWallAsksForItsFilterOnTheNextDescriptor(t *testing.T) {
	skipWhereTheSandboxCannotBeEstablished(t)
	want, err := syscallFilter(false)
	if err != nil {
		t.Skip(err)
	}
	for inherited, fd := range map[int]string{0: "3", 1: "4", 3: "6"} {
		argv, wall, err := containArgv(t.Context(), []string{"/bin/true"}, nil, nativeFiles{inherited: inherited})
		if err != nil {
			t.Fatal(err)
		}
		at := -1
		for i, a := range argv {
			if a == "--" {
				break
			}
			if a == "--seccomp" {
				at = i
			}
		}
		if at < 0 || argv[at+1] != fd {
			t.Errorf("with %d inherited descriptors the wall must read its filter from %s: %v", inherited, fd, argv)
		}
		if string(wall.Filter) != string(want) {
			t.Errorf("the containment does not carry the filter its argv asks for")
		}
	}
}
