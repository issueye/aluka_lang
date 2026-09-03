package gui

// Rect 表示屏幕矩形区域。
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// DisplayInfo 描述一个显示器的几何信息与缩放。
type DisplayInfo struct {
	ID          string  `json:"id"`
	Bounds      Rect    `json:"bounds"`
	WorkArea    Rect    `json:"workArea"`
	ScaleFactor float64 `json:"scaleFactor"`
	IsPrimary   bool    `json:"isPrimary"`
}

// GetPrimaryDisplay 获取主显示器几何与属性信息。
func GetPrimaryDisplay() (DisplayInfo, error) {
	return getPrimaryDisplay()
}

// GetAllDisplays 获取当前系统连接的所有显示器列表。
func GetAllDisplays() ([]DisplayInfo, error) {
	return getAllDisplays()
}
