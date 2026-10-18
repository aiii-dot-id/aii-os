# github.com/wlynxg/anet v0.0.5, without its link names

This directory is the tree's own copy of `github.com/wlynxg/anet` at
v0.0.5 (BSD-3-Clause, `LICENSE` beside this file; copyright wlynxg, and for
the netlink code The Go Authors). The tree's `go.mod` replaces that module
with this directory. Nothing else in the tree imports it: the WebRTC library
does (`github.com/pion/transport/v5/stdnet`), to read a device's network
interfaces on Android, where the standard library's own reading has been
refused to an application since Android 11.

## Why a copy

The module as published reaches into the standard library by link name:

    //go:linkname zoneCache net.zoneCache
    //go:linkname zoneCacheX golang.org/x/net/internal/socket.zoneCache

The Go linker has refused that since Go 1.23 ("invalid reference to
net.zoneCache") unless its check is turned off for the whole program
(`-ldflags=-checklinkname=0`). The tree does not turn the check off. With
the module as published the Android program did not link (found 2026-10-12).

## What differs from v0.0.5

Removed, and nothing put in its place:

- the two link-name variables above;
- the type `ipv6ZoneCache` and its method `update`, which only they used;
- the two places, in `Interfaces` and in `InterfaceByName`, where a table
  just read was written into those two caches.

Moved, not changed, so that the netlink reading is also built and tested on
Linux, where the same kernel answers it:

- `netlink_android.go` is `netlink.go`, built for Linux (Android is Linux to
  the Go tool);
- the table readers and their helpers (`interfaceTable`, `newLink`,
  `linkFlags`, `interfaceAddrTable`, `addrTable`, `newAddr`, `ioctl`,
  `indexToName`, `nameToMTU`, `nameToFlags`, `ifReq`) are in
  `interface_netlink.go`, built for Linux; `interface_android.go` keeps the
  exported functions, their errors and `interfaceByIndex`.

Not carried: the published module's own `README`, its `mobile/` example
application and its empty `go.sum`. The copy's own tests are not part of
this source release.

## What the removal costs

The two caches map an IPv6 zone name (`wlan0`) to its interface index. On
Android 11 and later the standard library cannot fill its own, so without
the write a zone given by name resolves to no interface. The WebRTC library
gives an IPv6 link-local address the name of its interface as its zone and
binds a socket to it; that bind is then refused and the library goes on to
the next address. So on such a device an IPv6 link-local address is not
offered as a path. Every IPv4 address and every other IPv6 address is
unaffected: they carry no zone. This is read from the source of the three
packages; it has not been seen on a device.

## What is proved, and what is not

Proved on Linux: the reading Android takes on 11 and later, a netlink
request with no bind, gives the interfaces that have an address and every
address of each, as the standard library gives them on the same machine.
Proved by a cross-build: the Android program links with the WebRTC library
in it and the linker's check on.

Run on one device, 2026-10-13, a Pixel 10 Pro (Android 17, API level 37,
arm64), from the shell: the cgo call answered 37, the netlink reading was
taken and returned the device's interfaces with their addresses, and the
standard library's own reading, which the shell is allowed, agreed with it.

Run inside an application's sandbox, the same day and device, as the
application's own user (the `runas_app` domain), with a diagnostic that
prints no address. Inside the AII OS application: an internet socket
opens, the netlink dump returns thirteen addresses on three interfaces,
each interface's name, MTU and flags are answered, this copy's reading is
three interfaces, and the standard library's reading is refused
("netlinkrib: permission denied"). That is the case this copy is for.
Inside a test application with no INTERNET permission the dump returned the
same addresses and the reading came back empty: the device refuses such an
application the internet socket on which names are asked, and an interface
whose name cannot be asked is dropped, as in the published module.

Not proved: a channel opening on a device, which wants a relay to offer
one; the IPv6 link-local cost above.

## Keeping it

Before the replaced version is raised, read the published module's change:
if it has dropped the link names itself, the replacement and this directory
go.
