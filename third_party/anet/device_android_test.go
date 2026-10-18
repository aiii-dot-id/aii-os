//go:build android

package anet

import (
	"net"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// ON AN ANDROID DEVICE, THE READING ITS API LEVEL CALLS FOR GIVES THE
// DEVICE'S INTERFACES. No row of the tree's gate runs this: it is built for
// Android through cgo and run on a device or in an emulator, by hand (the
// README says how). It asks the device its API level through the same call
// the exported functions use, reads the interfaces through those functions,
// and wants the loopback interface with its address among them. Where the
// standard library's own reading is allowed (it is refused to an application
// from Android 11, and allowed to the shell), the two are held to each other
// as they are on Linux.
func TestOnADeviceTheReadingIsTheOneItsAPILevelCallsFor(t *testing.T) {
	level := androidApiLevel()
	t.Logf("the device says its API level is %d; the netlink reading is taken from %d", level, android11ApiLevel)
	if level <= 0 {
		t.Fatalf("the device gave no API level (%d): this test is for a cgo build on a device", level)
	}
	ifaces, err := Interfaces()
	if err != nil {
		t.Fatalf("the interfaces: %v", err)
	}
	loopback := false
	for i := range ifaces {
		addrs, err := InterfaceAddrsByInterface(&ifaces[i])
		if err != nil {
			t.Fatalf("the addresses of %s: %v", ifaces[i].Name, err)
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ifaces[i].Flags&net.FlagLoopback != 0 && ipnet.IP.IsLoopback() {
				loopback = true
			}
		}
	}
	got := described(t, ifaces, InterfaceAddrsByInterface)
	t.Logf("read:\n  %s", strings.Join(got, "\n  "))
	if !loopback {
		t.Fatal("the reading has no loopback interface with a loopback address")
	}

	own, err := net.Interfaces()
	if err != nil {
		t.Logf("the standard library's own reading is refused here (%v): nothing to hold this one to", err)
		return
	}
	want := described(t, own, func(i *net.Interface) ([]net.Addr, error) { return i.Addrs() })
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the reading differs from the standard library's own on this device\nstandard library:\n  %s", strings.Join(want, "\n  "))
	}
	t.Logf("the standard library's own reading is allowed here and agrees")
}

// WHAT EACH STEP OF THE READING MEETS ON THIS DEVICE, said and not judged: how
// many addresses the netlink dump returns, and for each interface it names
// whether its name, MTU and flags could be asked for. The exported functions
// drop an interface silently when one of those fails, so a reading that
// comes back empty says nothing of why; this does. It prints counts and
// errors, never an address.
func TestOnADeviceEachStepOfTheReadingSaysWhatItMet(t *testing.T) {
	t.Logf("API level %d", androidApiLevel())
	if fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0); err != nil {
		t.Logf("an internet socket, which each interface's name, MTU and flags are asked on: %v", err)
	} else {
		syscall.Close(fd)
		t.Logf("an internet socket: opened")
	}
	tab, err := NetlinkRIB(syscall.RTM_GETADDR, syscall.AF_UNSPEC)
	if err != nil {
		t.Fatalf("the netlink address dump: %v", err)
	}
	msgs, err := syscall.ParseNetlinkMessage(tab)
	if err != nil {
		t.Fatalf("the dump did not parse: %v", err)
	}
	seen := map[uint32]bool{}
	addresses := 0
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWADDR {
			continue
		}
		addresses++
		ifam := (*syscall.IfAddrmsg)(unsafe.Pointer(&m.Data[0]))
		if seen[ifam.Index] {
			continue
		}
		seen[ifam.Index] = true
		name, nameErr := indexToName(ifam.Index)
		_, mtuErr := nameToMTU(name)
		_, flagsErr := nameToFlags(name)
		label := false
		if attrs, err := syscall.ParseNetlinkRouteAttr(&m); err == nil {
			for _, a := range attrs {
				if a.Attr.Type == syscall.IFA_LABEL {
					label = true
				}
			}
		}
		t.Logf("interface index %d: name by ioctl: %v; MTU: %v; flags: %v; the dump itself carries its name: %v", ifam.Index, nameErr, mtuErr, flagsErr, label)
	}
	t.Logf("the netlink dump: %d addresses on %d interfaces", addresses, len(seen))
	table, err := interfaceTable(0)
	t.Logf("the copy's reading: %d interfaces, %v", len(table), err)
	own, err := net.Interfaces()
	t.Logf("the standard library's reading: %d interfaces, %v", len(own), err)
}
