//go:build windows

package gui

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")

	shcore                = syscall.NewLazyDLL("shcore.dll")
	procGetDpiForMonitor  = shcore.NewProc("GetDpiForMonitor")
	procGetDpiForSystem   = user32.NewProc("GetDpiForSystem")
)

const (
	monitorinfoFPrimary = 0x00000001
	mdtEffectiveDpi     = 0
)

type monitorInfoExW struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
	szDevice  [32]uint16
}

func getMonitorScale(hMonitor uintptr) float64 {
	if procGetDpiForMonitor.Find() == nil {
		var dpiX, dpiY uint32
		r1, _, _ := procGetDpiForMonitor.Call(hMonitor, mdtEffectiveDpi, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY)))
		if r1 == 0 && dpiX > 0 {
			return float64(dpiX) / 96.0
		}
	}
	if procGetDpiForSystem.Find() == nil {
		r1, _, _ := procGetDpiForSystem.Call()
		if r1 > 0 {
			return float64(r1) / 96.0
		}
	}
	return 1.0
}

func getAllDisplays() ([]DisplayInfo, error) {
	var displays []DisplayInfo

	callback := syscall.NewCallback(func(hMonitor, hdcMonitor, lprcMonitor, dwData uintptr) uintptr {
		var info monitorInfoExW
		info.cbSize = uint32(unsafe.Sizeof(info))
		r1, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&info)))
		if r1 == 0 {
			return 1 // 继续枚举
		}

		devName := syscall.UTF16ToString(info.szDevice[:])
		isPrimary := (info.dwFlags & monitorinfoFPrimary) != 0
		scale := getMonitorScale(hMonitor)

		d := DisplayInfo{
			ID: devName,
			Bounds: Rect{
				X:      int(info.rcMonitor.Left),
				Y:      int(info.rcMonitor.Top),
				Width:  int(info.rcMonitor.Right - info.rcMonitor.Left),
				Height: int(info.rcMonitor.Bottom - info.rcMonitor.Top),
			},
			WorkArea: Rect{
				X:      int(info.rcWork.Left),
				Y:      int(info.rcWork.Top),
				Width:  int(info.rcWork.Right - info.rcWork.Left),
				Height: int(info.rcWork.Bottom - info.rcWork.Top),
			},
			ScaleFactor: scale,
			IsPrimary:   isPrimary,
		}

		displays = append(displays, d)
		return 1
	})

	r1, _, _ := procEnumDisplayMonitors.Call(0, 0, callback, 0)
	if r1 == 0 || len(displays) == 0 {
		// 回退使用 GetSystemMetrics
		scrW, _, _ := procGetSystemMetrics.Call(smCxScreen)
		scrH, _, _ := procGetSystemMetrics.Call(smCyScreen)
		return []DisplayInfo{
			{
				ID: "PRIMARY",
				Bounds: Rect{
					X:      0,
					Y:      0,
					Width:  int(scrW),
					Height: int(scrH),
				},
				WorkArea: Rect{
					X:      0,
					Y:      0,
					Width:  int(scrW),
					Height: int(scrH),
				},
				ScaleFactor: 1.0,
				IsPrimary:   true,
			},
		}, nil
	}

	return displays, nil
}

func getPrimaryDisplay() (DisplayInfo, error) {
	displays, err := getAllDisplays()
	if err != nil {
		return DisplayInfo{}, err
	}
	for _, d := range displays {
		if d.IsPrimary {
			return d, nil
		}
	}
	if len(displays) > 0 {
		return displays[0], nil
	}
	return DisplayInfo{}, fmt.Errorf("gui: no display found")
}
