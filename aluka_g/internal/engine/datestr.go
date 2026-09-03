package engine

import (
	"math"
	"strconv"
	"time"
)

// 时间格式化辅助（JS Date.prototype.toString 风格）。

var weekdays = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
var months = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// pad2 将整数补零为至少两位。
func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// pad4 将整数补零为至少四位。
func pad4(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// FormatDateString 生成 JS 风格本地时间字符串：Wed Aug 05 2026 12:34:56 GMT+0800。
func FormatDateString(ms float64) string {
	if math.IsNaN(ms) {
		return "Invalid Date"
	}
	t := time.UnixMilli(int64(ms))
	return t.Format("Mon Jan 02 2006 15:04:05") + " GMT" + gmtOffset(t)
}

// gmtOffset 返回时区偏移串，如 "+0800" / "-0530"。
func gmtOffset(t time.Time) string {
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return sign + pad2(offset/3600) + pad2((offset%3600)/60)
}

// FormatISOString 生成 JS 风格的 UTC ISO 字符串：YYYY-MM-DDTHH:mm:ss.mmmZ。
func FormatISOString(ms float64) string {
	if math.IsNaN(ms) {
		return "Invalid Date"
	}
	t := time.UnixMilli(int64(ms)).UTC()
	return pad4(t.Year()) + "-" + pad2(int(t.Month())) + "-" + pad2(t.Day()) +
		"T" + pad2(t.Hour()) + ":" + pad2(t.Minute()) + ":" + pad2(t.Second()) +
		"." + pad3(int(t.Nanosecond()/1e6)) + "Z"
}

func pad3(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
