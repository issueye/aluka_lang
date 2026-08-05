package interpreter

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// setupDate 注册全局 Date 构造器与 Date.prototype（开发优化方案 P0-3）。
//
//   - Date(...) / new Date(...)：返回 DateValue（持有毫秒时间值）
//   - Date.now() / Date.parse(str) / Date.UTC(y, m, ...)
//   - Date.prototype：全套 get/set 方法（本地 + UTC）、toString/toISOString/
//     toJSON/valueOf/toUTCString/toDateString/toTimeString
func (interp *Interpreter) setupDate() {
	dateProto := engine.NewObject()

	// --- Date 构造器 ---
	ctor := interp.makeFunc("Date", func(args []engine.Value) (engine.Value, error) {
		d := newDateValue(args)
		engine.SetProto(d, dateProto)
		return d, nil
	})
	_ = ctor.Set("prototype", dateProto)
	_ = dateProto.Set("constructor", ctor)

	// 静态方法
	_ = ctor.Set("now", interp.makeFunc("now", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(time.Now().UnixMilli())), nil
	}))
	_ = ctor.Set("parse", interp.makeFunc("parse", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Number(math.NaN()), nil
		}
		ms, ok := parseDateString(args[0].String())
		if !ok {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(ms), nil
	}))
	_ = ctor.Set("UTC", interp.makeFunc("UTC", func(args []engine.Value) (engine.Value, error) {
		ms, err := utcFromParts(args)
		if err != nil {
			return engine.Number(math.NaN()), nil
		}
		return engine.Number(ms), nil
	}))

	// --- Date.prototype 方法 ---
	// get*（本地时间）
	_ = dateProto.Set("getTime", interp.nativeMethod("getTime", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(dateMs(this)), nil
	}))
	_ = dateProto.Set("valueOf", interp.nativeMethod("valueOf", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(dateMs(this)), nil
	}))
	_ = dateProto.Set("getFullYear", interp.nativeMethod("getFullYear", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Year())), nil
	}))
	_ = dateProto.Set("getMonth", interp.nativeMethod("getMonth", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Month() - 1)), nil
	}))
	_ = dateProto.Set("getDate", interp.nativeMethod("getDate", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Day())), nil
	}))
	_ = dateProto.Set("getDay", interp.nativeMethod("getDay", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Weekday())), nil
	}))
	_ = dateProto.Set("getHours", interp.nativeMethod("getHours", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Hour())), nil
	}))
	_ = dateProto.Set("getMinutes", interp.nativeMethod("getMinutes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Minute())), nil
	}))
	_ = dateProto.Set("getSeconds", interp.nativeMethod("getSeconds", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Second())), nil
	}))
	_ = dateProto.Set("getMilliseconds", interp.nativeMethod("getMilliseconds", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateLocal(this).Nanosecond() / 1e6)), nil
	}))
	_ = dateProto.Set("getTimezoneOffset", interp.nativeMethod("getTimezoneOffset", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		t := dateLocal(this)
		_, offset := t.Zone()
		return engine.Number(float64(-offset / 60)), nil
	}))

	// getUTC*
	_ = dateProto.Set("getUTCFullYear", interp.nativeMethod("getUTCFullYear", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Year())), nil
	}))
	_ = dateProto.Set("getUTCMonth", interp.nativeMethod("getUTCMonth", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Month() - 1)), nil
	}))
	_ = dateProto.Set("getUTCDate", interp.nativeMethod("getUTCDate", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Day())), nil
	}))
	_ = dateProto.Set("getUTCDay", interp.nativeMethod("getUTCDay", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Weekday())), nil
	}))
	_ = dateProto.Set("getUTCHours", interp.nativeMethod("getUTCHours", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Hour())), nil
	}))
	_ = dateProto.Set("getUTCMinutes", interp.nativeMethod("getUTCMinutes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Minute())), nil
	}))
	_ = dateProto.Set("getUTCSeconds", interp.nativeMethod("getUTCSeconds", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Second())), nil
	}))
	_ = dateProto.Set("getUTCMilliseconds", interp.nativeMethod("getUTCMilliseconds", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(dateUTC(this).Nanosecond() / 1e6)), nil
	}))

	// set*（本地时间，简化：直接设置整体 ms）
	_ = dateProto.Set("setTime", interp.nativeMethod("setTime", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		d, ok := this.(*engine.DateValue)
		if !ok {
			return engine.Number(math.NaN()), nil
		}
		ms := math.NaN()
		if len(args) > 0 {
			ms, _ = args[0].Float()
		}
		d.SetTimeMs(ms)
		return engine.Number(ms), nil
	}))
	_ = dateProto.Set("setFullYear", interp.nativeMethod("setFullYear", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		v, err := setLocalParts(this, args, setFullYear)
		return v, err
	}))
	_ = dateProto.Set("setMonth", interp.nativeMethod("setMonth", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		v, err := setLocalParts(this, args, setMonth)
		return v, err
	}))
	_ = dateProto.Set("setDate", interp.nativeMethod("setDate", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		v, err := setLocalParts(this, args, setDate)
		return v, err
	}))
	_ = dateProto.Set("setHours", interp.nativeMethod("setHours", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		v, err := setLocalParts(this, args, setHours)
		return v, err
	}))
	_ = dateProto.Set("setMinutes", interp.nativeMethod("setMinutes", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		v, err := setLocalParts(this, args, setMinutes)
		return v, err
	}))
	_ = dateProto.Set("setSeconds", interp.nativeMethod("setSeconds", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		v, err := setLocalParts(this, args, setSeconds)
		return v, err
	}))
	_ = dateProto.Set("setMilliseconds", interp.nativeMethod("setMilliseconds", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		v, err := setLocalParts(this, args, setMilliseconds)
		return v, err
	}))

	// toString 系列
	_ = dateProto.Set("toString", interp.nativeMethod("toString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(this.String()), nil
	}))
	_ = dateProto.Set("toLocaleString", interp.nativeMethod("toLocaleString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return engine.Str(this.String()), nil
	}))
	_ = dateProto.Set("toISOString", interp.nativeMethod("toISOString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		ms := dateMs(this)
		if math.IsNaN(ms) {
			return engine.Undefined(), fmt.Errorf("%w: Invalid time value", engine.ErrRangeError)
		}
		return engine.Str(engine.FormatISOString(ms)), nil
	}))
	_ = dateProto.Set("toJSON", interp.nativeMethod("toJSON", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		ms := dateMs(this)
		if math.IsNaN(ms) {
			return engine.Undefined(), nil
		}
		return engine.Str(engine.FormatISOString(ms)), nil
	}))
	_ = dateProto.Set("toUTCString", interp.nativeMethod("toUTCString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		ms := dateMs(this)
		if math.IsNaN(ms) {
			return engine.Str("Invalid Date"), nil
		}
		t := time.UnixMilli(int64(ms)).UTC()
		days := [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
		months := [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
		return engine.Str(days[t.Weekday()] + ", " + pad2(t.Day()) + " " + months[t.Month()-1] + " " +
			pad4(t.Year()) + " " + pad2(t.Hour()) + ":" + pad2(t.Minute()) + ":" + pad2(t.Second()) + " GMT"), nil
	}))
	_ = dateProto.Set("toDateString", interp.nativeMethod("toDateString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		ms := dateMs(this)
		if math.IsNaN(ms) {
			return engine.Str("Invalid Date"), nil
		}
		return engine.Str(time.UnixMilli(int64(ms)).Format("Mon Jan 02 2006")), nil
	}))
	_ = dateProto.Set("toTimeString", interp.nativeMethod("toTimeString", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		ms := dateMs(this)
		if math.IsNaN(ms) {
			return engine.Str("Invalid Date"), nil
		}
		t := time.UnixMilli(int64(ms))
		_, offset := t.Zone()
		sign := "+"
		if offset < 0 {
			sign = "-"
			offset = -offset
		}
		return engine.Str(t.Format("15:04:05 GMT") + sign + pad2(offset/3600) + pad2((offset%3600)/60)), nil
	}))

	_ = interp.globalObj.Set("Date", ctor)
	interp.constructors["Date"] = ctor
}

// --- 辅助函数 -------------------------------------------------------------

// newDateValue 按 JS Date 构造语义计算毫秒时间值：
//   - 无参数 → now
//   - 单个字符串 → Date.parse
//   - 单个数字 → 作为毫秒
//   - Date 对象 → 克隆
//   - 多个参数 → 本地时间构造
func newDateValue(args []engine.Value) *engine.DateValue {
	if len(args) == 0 {
		return engine.NewDateValue(float64(time.Now().UnixMilli()))
	}
	if len(args) == 1 {
		a := args[0]
		if dv, ok := a.(*engine.DateValue); ok {
			return engine.NewDateValue(dv.TimeMs())
		}
		if a.Type() == engine.TypeString {
			ms, ok := parseDateString(a.String())
			if !ok {
				return engine.NewDateValue(math.NaN())
			}
			return engine.NewDateValue(ms)
		}
		if n, ok := a.Float(); ok {
			return engine.NewDateValue(n)
		}
		return engine.NewDateValue(math.NaN())
	}
	// 多参数：本地时间，缺失部分取最小值。
	parts := make([]float64, 7)
	for i := range parts {
		parts[i] = math.NaN()
	}
	for i := 0; i < len(args) && i < 7; i++ {
		f, _ := args[i].Float()
		parts[i] = f
	}
	y := parts[0]
	if y >= 0 && y < 100 {
		y += 1900
	}
	t := time.Date(int(y), monthOf(parts[1], 1), dayOf(parts[2], 1),
		int(math.Floor(parts[3]+0.5)), int(math.Floor(parts[4]+0.5)), int(math.Floor(parts[5]+0.5)),
		int(math.Floor(parts[6]+0.5))*1e6, time.Local)
	return engine.NewDateValue(float64(t.UnixMilli()))
}

func monthOf(v float64, def int) time.Month {
	if math.IsNaN(v) {
		return time.Month(def)
	}
	return time.Month(int(v) + 1)
}

func dayOf(v float64, def int) int {
	if math.IsNaN(v) {
		return def
	}
	return int(v)
}

// dateMs 返回 this 的毫秒时间值；非 Date 返回 NaN。
func dateMs(this engine.Value) float64 {
	if d, ok := this.(*engine.DateValue); ok {
		return d.TimeMs()
	}
	return math.NaN()
}

// dateLocal 返回本地时区的 time.Time；无效日期返回零值。
func dateLocal(this engine.Value) time.Time {
	ms := dateMs(this)
	if math.IsNaN(ms) {
		return time.UnixMilli(0)
	}
	return time.UnixMilli(int64(ms))
}

func dateUTC(this engine.Value) time.Time {
	ms := dateMs(this)
	if math.IsNaN(ms) {
		return time.UnixMilli(0).UTC()
	}
	return time.UnixMilli(int64(ms)).UTC()
}

// pad2 / pad4 本地实现（避免与 engine 包导出冲突）。
func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func pad4(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// setParts 标识 setFullYear/setMonth/... 各方法。
type setKind int

const (
	setFullYear setKind = iota
	setMonth
	setDate
	setHours
	setMinutes
	setSeconds
	setMilliseconds
)

// setLocalParts 实现 Date.prototype.set* 的本地时间语义（简化实现）。
// 缺失参数沿用当前值；首个参数缺失则设为 NaN（保持未定义部分）。
func setLocalParts(this engine.Value, args []engine.Value, kind setKind) (engine.Value, error) {
	d, ok := this.(*engine.DateValue)
	if !ok {
		return engine.Number(math.NaN()), nil
	}
	ms := d.TimeMs()
	if math.IsNaN(ms) {
		ms = float64(time.Now().UnixMilli())
	}
	t := time.UnixMilli(int64(ms))
	var v float64
	_ = v
	// 读取参数（缺失 → 沿用当前值）。
	getArg := func(i int, cur int) int {
		if len(args) > i {
			if f, ok := args[i].Float(); ok && !math.IsNaN(f) {
				return int(f)
			}
		}
		return cur
	}
	nt := t
	switch kind {
	case setFullYear:
		nt = time.Date(getArg(0, t.Year()), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	case setMonth:
		nt = time.Date(t.Year(), time.Month(getArg(0, int(t.Month()))+1), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	case setDate:
		nt = time.Date(t.Year(), t.Month(), getArg(0, t.Day()), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	case setHours:
		nt = time.Date(t.Year(), t.Month(), t.Day(), getArg(0, t.Hour()), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	case setMinutes:
		nt = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), getArg(0, t.Minute()), t.Second(), t.Nanosecond(), time.Local)
	case setSeconds:
		nt = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), getArg(0, t.Second()), t.Nanosecond(), time.Local)
	case setMilliseconds:
		nt = t.Add(time.Duration(getArg(0, t.Nanosecond()/1e6)-t.Nanosecond()/1e6) * time.Millisecond)
	}
	newMs := float64(nt.UnixMilli())
	d.SetTimeMs(newMs)
	return engine.Number(newMs), nil
}

// utcFromParts 实现 Date.UTC(y, m, d, h, min, s, ms) 的 UTC 语义。
func utcFromParts(args []engine.Value) (float64, error) {
	parts := make([]float64, 7)
	for i := range parts {
		parts[i] = math.NaN()
	}
	for i := 0; i < len(args) && i < 7; i++ {
		f, _ := args[i].Float()
		parts[i] = f
	}
	if len(args) == 0 {
		return math.NaN(), nil
	}
	y := parts[0]
	if y >= 0 && y < 100 {
		y += 1900
	}
	t := time.Date(int(y), monthOf(parts[1], 1), dayOf(parts[2], 1),
		int(math.Floor(parts[3]+0.5)), int(math.Floor(parts[4]+0.5)), int(math.Floor(parts[5]+0.5)),
		int(math.Floor(parts[6]+0.5))*1e6, time.UTC)
	return float64(t.UnixMilli()), nil
}

var isoRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,3}))?)?(Z|[+-]\d{2}:?\d{2})?$`)

// parseDateString 解析常见日期字符串（ISO 8601 及部分扩展格式）。
// 返回毫秒时间值；无法解析时 ok=false。
func parseDateString(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	m := isoRe.FindStringSubmatch(s)
	if m != nil {
		year, _ := strconv.Atoi(m[1])
		mon, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		hour, _ := strconv.Atoi(m[4])
		min, _ := strconv.Atoi(m[5])
		sec, _ := strconv.Atoi(m[6])
		ms := 0
		if m[7] != "" {
			ms, _ = strconv.Atoi(m[7])
			for len(strconv.Itoa(ms)) < 3 {
				ms *= 10
			}
		}
		loc := time.Local
		if m[8] == "Z" {
			loc = time.UTC
		} else if m[8] != "" {
			off := strings.Replace(m[8], ":", "", 1)
			sign := 1
			if strings.HasPrefix(off, "-") {
				sign = -1
			}
			off = strings.TrimPrefix(strings.TrimPrefix(off, "+"), "-")
			oh, _ := strconv.Atoi(off[:2])
			om := 0
			if len(off) > 2 {
				om, _ = strconv.Atoi(off[2:])
			}
			loc = time.FixedZone("", sign*(oh*3600+om*60))
		}
		t := time.Date(year, time.Month(mon), day, hour, min, sec, ms*1e6, loc)
		return float64(t.UnixMilli()), true
	}
	// 兼容 "Mon Jan 02 2006 15:04:05 GMT+0800" 与 "Mon, 02 Jan 2006 15:04:05 GMT"
	for _, layout := range []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC822,
		time.RFC822Z,
		time.RFC3339,
		"Mon Jan 02 2006 15:04:05 GMT-0700",
		"Mon Jan 02 2006 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.UnixMilli()), true
		}
	}
	return 0, false
}
