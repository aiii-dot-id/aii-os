//go:build windows

package fileperm

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernelbase                       = windows.NewLazySystemDLL("kernelbase.dll")
	procDeriveCapabilitySidsFromName = kernelbase.NewProc("DeriveCapabilitySidsFromName")
	capabilitySIDs                   sync.Map
)

func CapabilitySID(name string) (*windows.SID, error) {
	if sid, ok := capabilitySIDs.Load(name); ok {
		return sid.(*windows.SID), nil
	}
	if err := procDeriveCapabilitySidsFromName.Find(); err != nil {
		return nil, fmt.Errorf("this Windows cannot derive a capability's SID: %w", err)
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var groups, caps **windows.SID
	var nGroups, nCaps uint32
	r, _, callErr := procDeriveCapabilitySidsFromName.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&groups)), uintptr(unsafe.Pointer(&nGroups)),
		uintptr(unsafe.Pointer(&caps)), uintptr(unsafe.Pointer(&nCaps)))
	if r == 0 {
		return nil, fmt.Errorf("derive the capability %s: %w", name, callErr)
	}
	defer freeSIDs(groups, nGroups)
	defer freeSIDs(caps, nCaps)
	if nCaps == 0 || caps == nil {
		return nil, fmt.Errorf("derive the capability %s: Windows answered no capability SID", name)
	}
	sid, err := (*caps).Copy()
	if err != nil {
		return nil, err
	}
	capabilitySIDs.Store(name, sid)
	return sid, nil
}

func freeSIDs(arr **windows.SID, n uint32) {
	if arr == nil {
		return
	}
	for _, sid := range unsafe.Slice(arr, n) {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(sid)))
	}
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(arr)))
}
