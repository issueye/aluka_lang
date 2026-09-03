package nodeutil

// node:url 内置模块——提供 URL 解析与格式化。
// 基于 Go net/url 标准库。
//
// 注意：全局 URL/URLSearchParams 构造器（WHATWG 标准）是一个独立的、更大的
// 实现。此模块提供的是 Node.js 的 legacy url 模块（parse/resolve/format），
// 加上 fileURLToPath/pathToFileURL 转换与 domainToASCII/Unicode。
//
// 已知差异（相对 node22）：
//   - legacy parse 的 search/hash 字段不含前导 '?'/'#'（历史行为，有测试锁定）；
//   - domainToASCII 仅实现基础 punycode（RFC 3492），不做完整 UTS#46 映射。

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
)

// NewURL 构造 node:url 模块的导出对象。
func NewURL(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		input := nodebase.StrArg(args, 0)
		parseQueryString := false
		if len(args) > 1 {
			if b, ok := args[1].Bool(); ok {
				parseQueryString = b
			}
		}
		u, err := url.Parse(input)
		if err != nil {
			return engine.Undefined(), err
		}
		return urlToObj(u, parseQueryString), nil
	}))

	_ = m.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		// 字符串输入：解析后重新格式化（node 语义）。
		if args[0].Type() == engine.TypeString {
			u, err := url.Parse(args[0].String())
			if err != nil {
				return engine.Str(""), nil
			}
			return engine.Str(u.String()), nil
		}
		return engine.Str(formatURL(args[0])), nil
	}))

	_ = m.Set("resolve", engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		from := nodebase.StrArg(args, 0)
		to := nodebase.StrArg(args, 1)
		base, err := url.Parse(from)
		if err != nil {
			return engine.Str(to), nil
		}
		ref, err := url.Parse(to)
		if err != nil {
			return engine.Str(to), nil
		}
		return engine.Str(base.ResolveReference(ref).String()), nil
	}))

	_ = m.Set("fileURLToPath", engine.NewFunction("fileURLToPath", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(fileURLToPath(nodebase.StrArg(args, 0))), nil
	}))

	// fileURLToPathBuffer：node 22.23 新增，返回 Buffer 形式的路径。
	_ = m.Set("fileURLToPathBuffer", engine.NewFunction("fileURLToPathBuffer", func(args []engine.Value) (engine.Value, error) {
		return gbuffer.NewBufferInstance([]byte(fileURLToPath(nodebase.StrArg(args, 0)))), nil
	}))

	_ = m.Set("pathToFileURL", engine.NewFunction("pathToFileURL", func(args []engine.Value) (engine.Value, error) {
		href := pathToFileURL(nodebase.StrArg(args, 0))
		// node 返回 URL 对象；优先用全局 URL 构造器包装（获得 href/protocol 等）。
		if uc, err := ctx.Global().Get("URL"); err == nil && uc.IsFunction() {
			if f, ok := uc.AsFunction(); ok {
				obj, cerr := f.Call([]engine.Value{engine.Str(href)})
				if cerr == nil {
					return obj, nil
				}
			}
		}
		return engine.Str(href), nil
	}))

	_ = m.Set("domainToASCII", engine.NewFunction("domainToASCII", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(domainToASCII(nodebase.StrArg(args, 0))), nil
	}))

	_ = m.Set("domainToUnicode", engine.NewFunction("domainToUnicode", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(domainToUnicode(nodebase.StrArg(args, 0))), nil
	}))

	_ = m.Set("urlToHttpOptions", engine.NewFunction("urlToHttpOptions", func(args []engine.Value) (engine.Value, error) {
		return urlToHttpOptions(args[0]), nil
	}))

	_ = m.Set("resolveObject", engine.NewFunction("resolveObject", func(args []engine.Value) (engine.Value, error) {
		from := nodebase.StrArg(args, 0)
		to := nodebase.StrArg(args, 1)
		r := resolveURLs(from, to)
		u, err := url.Parse(r)
		if err != nil {
			return engine.Undefined(), nil
		}
		return parseUrlFields(engine.NewObject(), u, false), nil
	}))

	registerUrlClass(m)

	return m, nil
}

// legacyUrlKeys 是 node Url 实例的 12 个字段（保持 node 顺序）。
var legacyUrlKeys = []string{
	"protocol", "slashes", "auth", "host", "port", "hostname",
	"hash", "search", "query", "pathname", "path", "href",
}

// resolveURLs 解析相对 URL（失败或空 base 时回退 to，node 语义）。
func resolveURLs(from, to string) string {
	if from == "" {
		return to
	}
	base, err := url.Parse(from)
	if err != nil {
		return to
	}
	ref, err := url.Parse(to)
	if err != nil {
		return to
	}
	return base.ResolveReference(ref).String()
}

// parseUrlFields 按 node legacy 语义（search/hash 含 '?'/'#' 前缀）把解析结果
// 填入 obj 的 12 个字段，返回 obj。用于 Url 类与 resolveObject。
func parseUrlFields(obj engine.Object, u *url.URL, parseQueryString bool) engine.Value {
	if u.Scheme != "" {
		_ = obj.Set("protocol", engine.Str(u.Scheme+":"))
		_ = obj.Set("slashes", engine.Boolean(true))
	} else {
		_ = obj.Set("protocol", engine.Null())
		_ = obj.Set("slashes", engine.Null())
	}
	host := u.Host
	hostname := u.Hostname()
	port := u.Port()
	if host == "" {
		_ = obj.Set("host", engine.Null())
		_ = obj.Set("hostname", engine.Null())
		_ = obj.Set("port", engine.Null())
	} else {
		_ = obj.Set("host", engine.Str(host))
		_ = obj.Set("hostname", engine.Str(hostname))
		if port != "" {
			_ = obj.Set("port", engine.Str(port))
		} else {
			_ = obj.Set("port", engine.Null())
		}
	}
	_ = obj.Set("pathname", engine.Str(u.Path))
	path := u.Path
	if u.RawQuery != "" {
		_ = obj.Set("search", engine.Str("?"+u.RawQuery))
		if parseQueryString {
			_ = obj.Set("query", queryToObject(u.Query()))
		} else {
			_ = obj.Set("query", engine.Str(u.RawQuery))
		}
		path += "?" + u.RawQuery
	} else {
		_ = obj.Set("search", engine.Null())
		_ = obj.Set("query", engine.Null())
	}
	_ = obj.Set("path", engine.Str(path))
	if u.Fragment != "" {
		_ = obj.Set("hash", engine.Str("#"+u.Fragment))
	} else {
		_ = obj.Set("hash", engine.Null())
	}
	if u.User != nil {
		_ = obj.Set("auth", engine.Str(u.User.String()))
	} else {
		_ = obj.Set("auth", engine.Null())
	}
	_ = obj.Set("href", engine.Str(u.String()))
	return obj
}

// registerUrlClass 注册 node:url 的 legacy Url 类（构造参数被忽略，须显式 .parse()）。
func registerUrlClass(m engine.Object) {
	ctor := engine.NewFunction("Url", func(args []engine.Value) (engine.Value, error) {
		obj := engine.NewObject()
		for _, k := range legacyUrlKeys {
			_ = obj.Set(k, engine.Null())
		}

		// parse(url[, parseQueryString])：解析并填充 12 字段，返回 this。
		_ = obj.Set("parse", engine.NewFunction("parse", func(callArgs []engine.Value) (engine.Value, error) {
			pq := false
			if len(callArgs) > 1 {
				if b, ok := callArgs[1].Bool(); ok {
					pq = b
				}
			}
			if u, err := url.Parse(nodebase.StrArg(callArgs, 0)); err == nil {
				parseUrlFields(obj, u, pq)
			}
			return obj, nil
		}))

		// format()：用当前字段重算 href，返回 href 字符串。
		_ = obj.Set("format", engine.NewFunction("format", func(callArgs []engine.Value) (engine.Value, error) {
			href := formatURL(obj)
			_ = obj.Set("href", engine.Str(href))
			return engine.Str(href), nil
		}))

		// resolve(relative)：返回解析后的 URL 字符串。
		_ = obj.Set("resolve", engine.NewFunction("resolve", func(callArgs []engine.Value) (engine.Value, error) {
			href, _ := obj.Get("href")
			return engine.Str(resolveURLs(href.String(), nodebase.StrArg(callArgs, 0))), nil
		}))

		// resolveObject(relative)：返回解析后的 UrlObject。
		_ = obj.Set("resolveObject", engine.NewFunction("resolveObject", func(callArgs []engine.Value) (engine.Value, error) {
			href, _ := obj.Get("href")
			r := resolveURLs(href.String(), nodebase.StrArg(callArgs, 0))
			if u, err := url.Parse(r); err == nil {
				return parseUrlFields(engine.NewObject(), u, false), nil
			}
			return engine.Undefined(), nil
		}))

		// parseHost()：内部钩子，node 为无操作（仅设置 port/hostname）。
		_ = obj.Set("parseHost", engine.NewFunction("parseHost", func(callArgs []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))

		return obj, nil
	})
	_ = m.Set("Url", ctor)
}

// urlToObj 将 *url.URL 转为 Node.js UrlObject。
// search/hash 不含前导 '?'/'#'（历史行为，builtin_test 锁定）。
func urlToObj(u *url.URL, parseQueryString bool) engine.Value {
	obj := engine.NewObject()
	if u.Scheme != "" {
		_ = obj.Set("protocol", engine.Str(u.Scheme+":"))
		_ = obj.Set("slashes", engine.Boolean(true))
	} else {
		_ = obj.Set("protocol", engine.Null())
		_ = obj.Set("slashes", engine.Null())
	}
	host := u.Host
	hostname := u.Hostname()
	port := u.Port()
	if host == "" {
		_ = obj.Set("host", engine.Null())
		_ = obj.Set("hostname", engine.Null())
		_ = obj.Set("port", engine.Null())
	} else {
		_ = obj.Set("host", engine.Str(host))
		_ = obj.Set("hostname", engine.Str(hostname))
		if port != "" {
			_ = obj.Set("port", engine.Str(port))
		} else {
			_ = obj.Set("port", engine.Null())
		}
	}
	_ = obj.Set("pathname", engine.Str(u.Path))
	if u.RawQuery != "" {
		_ = obj.Set("search", engine.Str(u.RawQuery))
		_ = obj.Set("path", engine.Str(u.Path+"?"+u.RawQuery))
		if parseQueryString {
			_ = obj.Set("query", queryToObject(u.Query()))
		} else {
			_ = obj.Set("query", engine.Str(u.RawQuery))
		}
	} else {
		_ = obj.Set("search", engine.Null())
		_ = obj.Set("path", engine.Str(u.Path))
		_ = obj.Set("query", engine.Null())
	}
	if u.Fragment != "" {
		_ = obj.Set("hash", engine.Str(u.Fragment))
	} else {
		_ = obj.Set("hash", engine.Null())
	}
	if u.User != nil {
		_ = obj.Set("auth", engine.Str(u.User.String()))
	} else {
		_ = obj.Set("auth", engine.Null())
	}
	_ = obj.Set("href", engine.Str(u.String()))
	return obj
}

// queryToObject 把 url.Values 转为 JS 对象（重复键 → 数组，node querystring.parse 语义）。
func queryToObject(v url.Values) engine.Value {
	obj := engine.NewObject()
	for k, vals := range v {
		if len(vals) == 1 {
			_ = obj.Set(k, engine.Str(vals[0]))
		} else {
			arr := make([]engine.Value, 0, len(vals))
			for _, s := range vals {
				arr = append(arr, engine.Str(s))
			}
			_ = obj.Set(k, engine.NewArray(arr))
		}
	}
	return obj
}

// formatURL 实现 node:url 的 legacy format（UrlObject → 字符串）。
func formatURL(v engine.Value) string {
	obj, ok := v.AsObject()
	if !ok {
		return ""
	}
	get := func(k string) string {
		p, err := obj.Get(k)
		if err != nil || p.IsUndefined() || p.IsNull() {
			return ""
		}
		return p.String()
	}
	protocol := strings.TrimSuffix(get("protocol"), ":")
	auth := get("auth")
	host := get("host")
	hostname := get("hostname")
	port := get("port")
	pathname := get("pathname")
	search := get("search")
	hash := get("hash")

	var b strings.Builder
	if protocol != "" {
		b.WriteString(protocol)
		b.WriteByte(':')
	}
	// slashes 默认 true（有协议且有 host/hostname 时）。
	if protocol != "" && (host != "" || hostname != "") {
		b.WriteString("//")
	}
	if auth != "" {
		b.WriteString(auth)
		b.WriteByte('@')
	}
	if host != "" {
		b.WriteString(host)
	} else if hostname != "" {
		b.WriteString(hostname)
		if port != "" {
			b.WriteString(":")
			b.WriteString(port)
		}
	}
	if pathname != "" {
		b.WriteString(pathname)
	}
	if search != "" {
		b.WriteString("?")
		b.WriteString(strings.TrimPrefix(search, "?"))
	}
	if hash != "" {
		b.WriteString("#")
		b.WriteString(strings.TrimPrefix(hash, "#"))
	}
	return b.String()
}

// fileURLToPath 将 file:// URL 转成本机路径。
func fileURLToPath(input string) string {
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "file" {
		return input
	}
	if u.Host != "" && u.Host != "localhost" {
		return input
	}
	p := u.Path // 已解码
	if runtime.GOOS == "windows" {
		// 需绝对路径（node: ERR_INVALID_FILE_URL_PATH 近似语义）。
		if !strings.HasPrefix(p, "/") {
			return p
		}
		if len(p) >= 3 && p[2] == ':' {
			p = p[1:] // /C:/x → C:/x
		}
	}
	return filepath.FromSlash(p)
}

// pathToFileURL 将本机路径转成 file:// URL 字符串（调用方包成 URL 对象）。
func pathToFileURL(input string) string {
	abs, err := filepath.Abs(input)
	if err != nil {
		return ""
	}
	slash := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && len(slash) >= 2 && slash[1] == ':' {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}

// urlToHttpOptions 将 URL 对象转为 http.request options（node:url 导出）。
func urlToHttpOptions(v engine.Value) engine.Value {
	obj := engine.NewObject()
	if o, ok := v.AsObject(); ok {
		href, _ := o.Get("href")
		if href != nil && !href.IsUndefined() {
			_ = obj.Set("protocol", href)
			_ = obj.Set("href", href)
		}
		// 用 href 字符串重新解析以拆分组件。
		u, err := url.Parse(href.String())
		if err == nil {
			_ = obj.Set("protocol", engine.Str(u.Scheme+":"))
			_ = obj.Set("hostname", engine.Str(u.Hostname()))
			_ = obj.Set("hash", engine.Str("#"+u.Fragment))
			_ = obj.Set("search", engine.Str("?"+u.RawQuery))
			_ = obj.Set("pathname", engine.Str(u.Path))
			path := u.Path
			if u.RawQuery != "" {
				path += "?" + u.RawQuery
			}
			_ = obj.Set("path", engine.Str(path))
			_ = obj.Set("port", engine.Str(u.Port()))
			if u.User != nil {
				_ = obj.Set("auth", engine.Str(u.User.String()))
			}
		}
	}
	return obj
}

// --- domainToASCII / domainToUnicode（基础 punycode，RFC 3492）-------------

// punycodeParams 是 RFC 3492 常量。
const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
)

// punyAdapt 计算 RFC 3492 的 bias 调整量。
func punyAdapt(delta, numPoints int, firstTime bool) int {
	if firstTime {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := 0
	for delta > ((punyBase-punyTMin)*punyTMax)/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}

// punyEncode 对 UTF-8 字符串编码为 punycode（不含 xn-- 前缀）。
func punyEncode(input string) string {
	if input == "" {
		return ""
	}
	runes := []rune(input)
	// 全 ASCII：原样返回。
	allASCII := true
	basic := make([]byte, 0, len(input))
	for _, r := range runes {
		if r < 0x80 {
			basic = append(basic, byte(r))
		} else {
			allASCII = false
		}
	}
	var out []byte
	basicLen := len(basic)
	if basicLen > 0 {
		out = append(out, basic...)
	}
	if allASCII {
		return string(out)
	}
	b := punyInitialBias
	n := punyInitialN
	h := basicLen
	if basicLen > 0 {
		out = append(out, '-')
	}
	processed := make(map[rune]bool)
	for _, r := range runes {
		if r < 0x80 {
			processed[r] = true
		}
	}
	// 与 node:lib/punycode.js 编码器逐行对齐（含其特有的尾部 ++delta 与
	// 内层对全部 c < n 计数——不排除 basic/已处理码点）。
	numPoints := len(runes)
	delta := 0
	for h < numPoints {
		// m = 未处理码点中 >= n 的最小者（已处理码点必 < n，天然被排除）。
		minCP := -1
		for _, r := range runes {
			if r >= rune(n) && !processed[r] && (minCP == -1 || r < rune(minCP)) {
				minCP = int(r)
			}
		}
		if minCP < 0 {
			break
		}
		delta += (minCP - n) * (h + 1)
		n = minCP
		for _, r := range runes {
			if r < rune(n) {
				delta++
			}
			if r == rune(n) {
				// 编码 delta（RFC 3492 §6.3：q 不在此循环内增量）。
				q := delta
				k := punyBase
				for {
					t := punyBase
					if k <= b {
						t = punyTMin
					} else if k >= b+punyTMax {
						t = punyTMax
					} else {
						t = k - b
					}
					if q < t {
						break
					}
					out = append(out, punyDigitToChar(t+(q-t)%(punyBase-t)))
					q = (q - t) / (punyBase - t)
					k += punyBase
				}
				out = append(out, punyDigitToChar(q))
				b = punyAdapt(delta, h+1, h == basicLen)
				delta = 0
				h++
			}
		}
		delta++ // node lib/punycode.js 特有：每轮外循环尾部额外 +1
		n++     // 处理下一个码点
	}
	return string(out)
}

func punyDigitToChar(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

// punyDecode 解码 punycode 为 UTF-8（不含 xn-- 前缀）。
func punyDecode(input string) string {
	lastDash := strings.LastIndexByte(input, '-')
	var basic string
	var encoded string
	if lastDash >= 0 {
		basic = input[:lastDash]
		encoded = input[lastDash+1:]
	} else {
		encoded = input
	}
	var out []rune
	for _, c := range basic {
		out = append(out, c)
	}
	n := punyInitialN
	i := 0
	bias := punyInitialBias
	idx := 0
	for idx < len(encoded) {
		oldi := i
		w := 1
		for k := punyBase; ; k += punyBase {
			cp := encoded[idx]
			idx++
			digit := punyCharToDigit(cp)
			i += digit * w
			t := punyBase
			if k <= bias {
				t = punyTMin
			} else if k >= bias+punyTMax {
				t = punyTMax
			} else {
				t = k - bias
			}
			if digit < t {
				break
			}
			w *= punyBase - t
		}
		outLen := len(out) + 1
		bias = punyAdapt(i-oldi, outLen, oldi == 0)
		n += i / outLen
		i %= outLen
		out = append(out, 0)
		copy(out[i+1:], out[i:])
		out[i] = rune(n)
		i++
	}
	return string(out)
}

func punyCharToDigit(c byte) int {
	switch {
	case c >= 'a' && c <= 'z':
		return int(c - 'a')
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= '0' && c <= '9':
		return int(c-'0') + 26
	default:
		return 0
	}
}

// domainToASCII 将 Unicode 域名转为 ASCII（punycode 标签 + xn-- 前缀）。
func domainToASCII(domain string) string {
	if domain == "" {
		return ""
	}
	domain = strings.ToLower(domain)
	labels := strings.Split(domain, ".")
	for i, lab := range labels {
		ascii := true
		for _, r := range lab {
			if r > 0x7f {
				ascii = false
				break
			}
		}
		if !ascii {
			labels[i] = "xn--" + punyEncode(lab)
		}
	}
	return strings.Join(labels, ".")
}

// domainToUnicode 将 ASCII 域名（xn-- 前缀）转为 Unicode。
func domainToUnicode(domain string) string {
	labels := strings.Split(domain, ".")
	for i, lab := range labels {
		if strings.HasPrefix(lab, "xn--") {
			labels[i] = punyDecode(lab[4:])
		}
	}
	return strings.Join(labels, ".")
}
