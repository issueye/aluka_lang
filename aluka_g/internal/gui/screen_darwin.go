//go:build darwin

package gui

import (
	"fmt"
)

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

func getAllDisplays() ([]DisplayInfo, error) {
	if err := ensureObjC(); err != nil {
		return nil, err
	}
	mainScreen := objcCall0(objcClass("NSScreen"), "mainScreen")
	if mainScreen == 0 {
		return nil, fmt.Errorf("gui: NSScreen.mainScreen returned nil")
	}

	// 默认兜底主屏
	return []DisplayInfo{
		{
			ID: "PRIMARY",
			Bounds: Rect{
				X:      0,
				Y:      0,
				Width:  1920,
				Height: 1080,
			},
			WorkArea: Rect{
				X:      0,
				Y:      0,
				Width:  1920,
				Height: 1055,
			},
			ScaleFactor: 2.0,
			IsPrimary:   true,
		},
	}, nil
}
