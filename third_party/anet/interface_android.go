package anet

import (
	"errors"
	"net"
)

const (
	android11ApiLevel = 30
)

var (
	customAndroidApiLevel       = -1
	errInvalidInterface         = errors.New("invalid network interface")
	errInvalidInterfaceIndex    = errors.New("invalid network interface index")
	errInvalidInterfaceName     = errors.New("invalid network interface name")
	errNoSuchInterface          = errors.New("no such network interface")
	errNoSuchMulticastInterface = errors.New("no such multicast network interface")
)

// Interfaces returns a list of the system's network interfaces.
func Interfaces() ([]net.Interface, error) {
	if androidApiLevel() < android11ApiLevel {
		return net.Interfaces()
	}

	ift, err := interfaceTable(0)
	if err != nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	return ift, nil
}

// InterfaceAddrs returns a list of the system's unicast interface
// addresses.
//
// The returned list does not identify the associated interface; use
// Interfaces and Interface.Addrs for more detail.
func InterfaceAddrs() ([]net.Addr, error) {
	if androidApiLevel() < android11ApiLevel {
		return net.InterfaceAddrs()
	}

	ifat, err := interfaceAddrTable(nil)
	if err != nil {
		err = &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	return ifat, err
}

// InterfaceByIndex returns the interface specified by index.
//
// On Solaris, it returns one of the logical network interfaces
// sharing the logical data link; for more precision use
// InterfaceByName.
func InterfaceByIndex(index int) (*net.Interface, error) {
	if androidApiLevel() < android11ApiLevel {
		return net.InterfaceByIndex(index)
	}

	if index <= 0 {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errInvalidInterfaceIndex}
	}
	ift, err := interfaceTable(index)
	if err != nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	ifi, err := interfaceByIndex(ift, index)
	if err != nil {
		err = &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	return ifi, err
}

// InterfaceByName returns the interface specified by name.
func InterfaceByName(name string) (*net.Interface, error) {
	if name == "" {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errInvalidInterfaceName}
	}
	ift, err := interfaceTable(0)
	if err != nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	for _, ifi := range ift {
		if name == ifi.Name {
			return &ifi, nil
		}
	}
	return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errNoSuchInterface}
}

// InterfaceAddrsByInterface returns a list of the system's unicast
// interface addresses by specific interface.
func InterfaceAddrsByInterface(ifi *net.Interface) ([]net.Addr, error) {
	if ifi == nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errInvalidInterface}
	}

	if androidApiLevel() < android11ApiLevel {
		return ifi.Addrs()
	}

	ifat, err := interfaceAddrTable(ifi)
	if err != nil {
		err = &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	return ifat, err
}

// SetAndroidVersion set the Android environment in which the program runs.
// The Android system version number can be obtained through
// `android.os.Build.VERSION.RELEASE` of the Android framework.
// If version is 0 the actual version will be detected automatically if possible.
func SetAndroidVersion(version uint) {
	switch {
	case version == 0:
		customAndroidApiLevel = -1
	case version >= 11:
		customAndroidApiLevel = android11ApiLevel
	default:
		customAndroidApiLevel = 0
	}
}

func androidApiLevel() int {
	if customAndroidApiLevel != -1 {
		// user-provided api level should be used
		return customAndroidApiLevel
	}

	// try to autodetect api level
	return androidDeviceApiLevel()
}

func interfaceByIndex(ift []net.Interface, index int) (*net.Interface, error) {
	for _, ifi := range ift {
		if index == ifi.Index {
			return &ifi, nil
		}
	}
	return nil, errNoSuchInterface
}
