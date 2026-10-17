//go:build linux

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// reachPlatform, Linux: what the system-call filter refuses, each tried
// with the call itself so that the answer is the kernel's.
//
//	socket          make a socket of each family a child might try
//	pair            make a connected pair
//	dgram=<path>    make a datagram pair and send one end's datagram to
//	                the socket at that path
//	uring           set up an io_uring
//	tiocsti         type a byte into the terminal behind stdin
//	keyring         ask the kernel for the user's keyring, and add a key
//	                to this process's own
//	packets         what CUDA does at start: a packet-sequenced local
//	                socket, bound to an abstract name and listening
//	seqconnect=<p>  a packet-sequenced local socket connecting to the
//	                socket at that path
func reachPlatform(key, value string) {
	switch key {
	case "socket":
		for _, family := range []struct {
			name string
			id   int
		}{{"unix", syscall.AF_UNIX}, {"inet", syscall.AF_INET}, {"inet6", syscall.AF_INET6}, {"netlink", syscall.AF_NETLINK}} {
			fd, err := syscall.Socket(family.id, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
			if family.id == syscall.AF_NETLINK {
				fd, err = syscall.Socket(family.id, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
			}
			if err != nil {
				fmt.Printf("socket.%s=refused %s\n", family.name, errnoName(err))
				continue
			}
			syscall.Close(fd)
			fmt.Printf("socket.%s=made\n", family.name)
		}
	case "pair":
		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
		if err != nil {
			fmt.Printf("pair=refused %s\n", errnoName(err))
			return
		}
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		fmt.Println("pair=made")
	case "dgram":
		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
		if err != nil {
			fmt.Printf("dgram=refused %s\n", errnoName(err))
			return
		}
		defer syscall.Close(fds[0])
		defer syscall.Close(fds[1])
		if err := syscall.Sendto(fds[0], []byte("from inside"), 0, &syscall.SockaddrUnix{Name: value}); err != nil {
			fmt.Printf("dgram=refused %s\n", errnoName(err))
			return
		}
		fmt.Println("dgram=sent")
	case "uring":
		// io_uring_setup(entries, params): 425 on every architecture
		// that has it. A zeroed params block is a valid request.
		var params [120]byte
		fd, _, errno := syscall.Syscall(425, 4, uintptr(unsafe.Pointer(&params[0])), 0)
		if errno != 0 {
			fmt.Printf("uring=refused %s\n", errnoName(errno))
			return
		}
		syscall.Close(int(fd))
		fmt.Println("uring=made")
	case "tiocsti":
		// On a pipe the kernel's own answer is ENOTTY; the filter's is
		// EPERM, before the kernel looks at what the descriptor is.
		c := byte('x')
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TIOCSTI, uintptr(unsafe.Pointer(&c))); errno != 0 {
			fmt.Printf("tiocsti=refused %s\n", errnoName(errno))
			return
		}
		fmt.Println("tiocsti=typed")
	case "packets":
		fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_SEQPACKET|syscall.SOCK_CLOEXEC, 0)
		if err != nil {
			fmt.Printf("packets=refused %s\n", errnoName(err))
			return
		}
		defer syscall.Close(fd)
		// An abstract name: it lives in this process's network
		// namespace and on no file system.
		if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: "@wall-probe-" + fmt.Sprint(syscall.Getpid())}); err != nil {
			fmt.Printf("packets=made bind=refused %s\n", errnoName(err))
			return
		}
		if err := syscall.Listen(fd, 8); err != nil {
			fmt.Printf("packets=made listen=refused %s\n", errnoName(err))
			return
		}
		fmt.Println("packets=listening")
	case "seqconnect":
		fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_SEQPACKET|syscall.SOCK_CLOEXEC, 0)
		if err != nil {
			fmt.Printf("seqconnect=refused socket %s\n", errnoName(err))
			return
		}
		defer syscall.Close(fd)
		if err := syscall.Connect(fd, &syscall.SockaddrUnix{Name: value}); err != nil {
			fmt.Printf("seqconnect=refused %s\n", errnoName(err))
			return
		}
		fmt.Println("seqconnect=connected")
	case "keyring":
		// keyctl(KEYCTL_GET_KEYRING_ID, KEY_SPEC_USER_KEYRING, create):
		// the keyring every process of the user shares.
		const keyctlGetKeyringID, userKeyring = 0, ^uintptr(3) // -4
		if _, _, errno := syscall.Syscall(syscall.SYS_KEYCTL, keyctlGetKeyringID, userKeyring, 1); errno != 0 {
			fmt.Printf("keyring=refused %s\n", errnoName(errno))
		} else {
			fmt.Println("keyring=reached")
		}
		// add_key("user", description, payload, length, KEY_SPEC_PROCESS_KEYRING):
		// a key of this process's own, gone with it.
		kind, name, payload := []byte("user\x00"), []byte("wall-probe\x00"), []byte("x")
		const processKeyring = ^uintptr(1) // -2
		if _, _, errno := syscall.Syscall6(syscall.SYS_ADD_KEY, uintptr(unsafe.Pointer(&kind[0])), uintptr(unsafe.Pointer(&name[0])),
			uintptr(unsafe.Pointer(&payload[0])), 1, processKeyring, 0); errno != 0 {
			fmt.Printf("addkey=refused %s\n", errnoName(errno))
		} else {
			fmt.Println("addkey=made")
		}
	default:
		fmt.Printf("%s=unknown\n", key)
	}
}
