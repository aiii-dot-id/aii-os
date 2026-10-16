//go:build linux || android

package pluginhost

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

type filterVerdict uint32

const (
	seccompNr   = 0
	seccompArch = 4

	seccompArg0Low = 16
	seccompArg1Low = 24

	bpfLoadWord    = unix.BPF_LD | unix.BPF_W | unix.BPF_ABS
	bpfJumpEqual   = unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K
	bpfJumpAtLeast = unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K
	bpfAndConstant = unix.BPF_ALU | unix.BPF_AND | unix.BPF_K
	bpfReturn      = unix.BPF_RET | unix.BPF_K

	socketTypeMask = 0xf

	x32Bit = 0x40000000
)

func refuseWith(errno unix.Errno) filterVerdict {
	return filterVerdict(unix.SECCOMP_RET_ERRNO | uint32(errno)&unix.SECCOMP_RET_DATA)
}

func localPackets(profile *AcceleratorProfile) bool {
	if profile == nil {
		return false
	}
	for _, part := range strings.Split(profile.Backend, "+") {
		if part == "cuda" {
			return true
		}
	}
	return false
}

func syscallFilter(packets bool) ([]byte, error) {
	var arch uint32
	switch runtime.GOARCH {
	case "amd64":
		arch = unix.AUDIT_ARCH_X86_64
	case "arm64":
		arch = unix.AUDIT_ARCH_AARCH64
	default:
		return nil, fmt.Errorf("no system-call filter is written for linux/%s, so native T3 plugins are not run on it (they are never run without the filter)", runtime.GOARCH)
	}
	const (
		toAllow = iota
		toNoSocket
		toNoSuchCall
		toNoTyping

		atTerminalRequest
		labels
	)
	type insn struct {
		code     uint16
		k        uint32
		yes, no  int
		isReturn bool
		plain    bool
	}
	next := -1
	at := [labels]int{}
	var prog []insn
	load := func(offset uint32) {
		prog = append(prog, insn{code: bpfLoadWord, k: offset, yes: next, no: next, plain: true})
	}
	equal := func(k uint32, yes, no int) { prog = append(prog, insn{code: bpfJumpEqual, k: k, yes: yes, no: no}) }

	load(seccompArch)
	equal(arch, next, toNoSuchCall)
	load(seccompNr)
	if runtime.GOARCH == "amd64" {
		prog = append(prog, insn{code: bpfJumpAtLeast, k: x32Bit, yes: toNoSuchCall, no: next})
	}
	equal(unix.SYS_SOCKETPAIR, toNoSocket, next)
	equal(unix.SYS_IO_URING_SETUP, toNoSuchCall, next)
	equal(unix.SYS_KEYCTL, toNoSuchCall, next)
	equal(unix.SYS_ADD_KEY, toNoSuchCall, next)
	equal(unix.SYS_REQUEST_KEY, toNoSuchCall, next)
	equal(unix.SYS_IOCTL, atTerminalRequest, next)
	if !packets {
		equal(unix.SYS_SOCKET, toNoSocket, toAllow)
	} else {

		equal(unix.SYS_SOCKET, next, toAllow)
		load(seccompArg0Low)
		equal(unix.AF_UNIX, next, toNoSocket)
		load(seccompArg1Low)
		prog = append(prog, insn{code: bpfAndConstant, k: socketTypeMask, yes: next, no: next, plain: true})
		equal(unix.SOCK_SEQPACKET, toAllow, toNoSocket)
	}
	at[atTerminalRequest] = len(prog)
	load(seccompArg1Low)
	equal(unix.TIOCSTI, toNoTyping, next)
	equal(unix.TIOCLINUX, toNoTyping, toAllow)

	for label, verdict := range [atTerminalRequest]filterVerdict{
		toAllow:      unix.SECCOMP_RET_ALLOW,
		toNoSocket:   refuseWith(unix.EACCES),
		toNoSuchCall: refuseWith(unix.ENOSYS),
		toNoTyping:   refuseWith(unix.EPERM),
	} {
		at[label] = len(prog)
		prog = append(prog, insn{code: bpfReturn, k: uint32(verdict), isReturn: true})
	}
	out := make([]byte, 0, 8*len(prog))
	for i, in := range prog {
		var jt, jf int
		if !in.isReturn && !in.plain {

			if in.yes != next {
				jt = at[in.yes] - i - 1
			}
			if in.no != next {
				jf = at[in.no] - i - 1
			}
			if jt < 0 || jf < 0 || jt > 255 || jf > 255 {
				return nil, fmt.Errorf("system-call filter: a jump of %d or %d does not fit", jt, jf)
			}
		}
		out = binary.NativeEndian.AppendUint16(out, in.code)
		out = append(out, uint8(jt), uint8(jf))
		out = binary.NativeEndian.AppendUint32(out, in.k)
	}
	return out, nil
}
