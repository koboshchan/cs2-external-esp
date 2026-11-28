//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	pGetWindowRect         = user32.NewProc("GetWindowRect")
	pGetWindowPlacement    = user32.NewProc("GetWindowPlacement")
	pDwmGetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")
	pCoCreateInstance      = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	pMonitorFromWindow     = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW       = user32.NewProc("GetMonitorInfoW")
)

type windowPlacement struct {
	Length, Flags, ShowCommand uint32
	MinPosition, MaxPosition   point
	Normal                     rect
}

func windowProcessID(hwnd uintptr) uint32 {
	var pid uint32
	windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid)
	return pid
}

func processExecutable(pid uint32) string {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, 32768)
	length := uint32(len(buffer))
	if windows.QueryFullProcessImageName(process, 0, &buffer[0], &length) != nil {
		return ""
	}
	return windows.UTF16ToString(buffer[:length])
}

func windowIsDisplayable(hwnd uintptr) bool {
	visible, _, _ := pIsWindowVisible.Call(hwnd)
	minimized, _, _ := pIsIconic.Call(hwnd)
	if visible == 0 || minimized != 0 {
		return false
	}
	var cloaked uint32
	const dwmwaCloaked = 14
	if pDwmGetWindowAttribute.Find() == nil {
		pDwmGetWindowAttribute.Call(hwnd, dwmwaCloaked, uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked))
	}
	return cloaked == 0
}

var geforceCandidates []streamWindow
var geforceProcesses map[uint32]bool

var geforceWindowsCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
	pid := windowProcessID(hwnd)
	if pid == 0 || pid == uint32(os.Getpid()) {
		return 1
	}
	var bounds rect
	if ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds))); ok == 0 {
		return 1
	}
	if minimized, _, _ := pIsIconic.Call(hwnd); minimized != 0 {
		placement := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
		if ok, _, _ := pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&placement))); ok != 0 {
			bounds = placement.Normal
		}
	}
	width, height := bounds.right-bounds.left, bounds.bottom-bounds.top
	if width < 320 || height < 180 {
		return 1
	}
	matched, known := geforceProcesses[pid]
	if !known {
		matched = isGeForceExecutable(processExecutable(pid))
		geforceProcesses[pid] = matched
	}
	if matched {
		front, _, _ := pGetForegroundWindow.Call()
		geforceCandidates = append(geforceCandidates, streamWindow{Handle: hwnd,
			Width: width, Height: height, Visible: windowIsDisplayable(hwnd), Active: front == hwnd})
	}
	return 1
})

func findGeForceWindow() uintptr {
	geforceCandidates = geforceCandidates[:0]
	geforceProcesses = make(map[uint32]bool)
	pEnumWindows.Call(geforceWindowsCallback, 0)
	return chooseStreamWindow(geforceCandidates)
}

func geForceForeground(target uintptr) bool {
	front, _, _ := pGetForegroundWindow.Call()
	if front == 0 || target == 0 {
		return false
	}
	pid := windowProcessID(front)
	return front == target || pid == uint32(os.Getpid()) || pid == windowProcessID(target) ||
		isGeForceExecutable(processExecutable(pid))
}

func placeNativeWindow(hwnd, reference uintptr) {
	monitor, _, _ := pMonitorFromWindow.Call(reference, 2)
	info := struct {
		Size          uint32
		Monitor, Work rect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := pGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return
	}
	var bounds rect
	if ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds))); ok == 0 {
		return
	}
	x := info.Work.left + max(0, (info.Work.right-info.Work.left-(bounds.right-bounds.left))/2)
	y := info.Work.top + max(0, (info.Work.bottom-info.Work.top-(bounds.bottom-bounds.top))/2)
	pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, 0x0001|0x0004|swpNoActivate)
}

// Public IVirtualDesktopManager methods move this process's overlay alongside
// the target window when Windows assigns it to a different virtual desktop.
type virtualDesktopVTable struct {
	QueryInterface, AddRef, Release         uintptr
	IsWindowOnCurrentVirtualDesktop         uintptr
	GetWindowDesktopID, MoveWindowToDesktop uintptr
}

type virtualDesktopManager struct {
	VTable *virtualDesktopVTable
}

func newVirtualDesktopManager() *virtualDesktopManager {
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	if err != nil && err != syscall.Errno(1) {
		return nil
	}
	classID, _ := windows.GUIDFromString("{AA509086-5CA9-4C25-8F95-589D3C07B48A}")
	interfaceID, _ := windows.GUIDFromString("{A5CD92FF-29BE-454C-8D04-D82879FB3F1B}")
	var manager *virtualDesktopManager
	r, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&classID)), 0, 1,
		uintptr(unsafe.Pointer(&interfaceID)), uintptr(unsafe.Pointer(&manager)))
	if int32(r) < 0 || manager == nil {
		windows.CoUninitialize()
		return nil
	}
	return manager
}

func (m *virtualDesktopManager) Close() {
	if m != nil {
		syscall.SyscallN(m.VTable.Release, uintptr(unsafe.Pointer(m)))
		windows.CoUninitialize()
	}
}

func (m *virtualDesktopManager) Follow(target, overlay uintptr) {
	if m == nil || target == 0 || overlay == 0 {
		return
	}
	var targetID, overlayID windows.GUID
	targetResult, _, _ := syscall.SyscallN(m.VTable.GetWindowDesktopID, uintptr(unsafe.Pointer(m)), target, uintptr(unsafe.Pointer(&targetID)))
	overlayResult, _, _ := syscall.SyscallN(m.VTable.GetWindowDesktopID, uintptr(unsafe.Pointer(m)), overlay, uintptr(unsafe.Pointer(&overlayID)))
	if int32(targetResult) >= 0 && int32(overlayResult) >= 0 && targetID != overlayID {
		syscall.SyscallN(m.VTable.MoveWindowToDesktop, uintptr(unsafe.Pointer(m)), overlay, uintptr(unsafe.Pointer(&targetID)))
	}
}
