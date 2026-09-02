package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gintl"
)

func newIntlTestContext(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := gintl.NewIntl(ctx, gintl.IntlConfig{}); err != nil {
		t.Fatalf("NewIntl: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	return ctx
}

// TestIntlDateTimeFormat 测试日期时间格式化
func TestIntlDateTimeFormat(t *testing.T) {
	ctx := newIntlTestContext(t)

	src := `
		globalThis.pass = false;
		var dtf = new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "numeric", day: "numeric" });
		var formatted = dtf.format(new Date(1700000000000).getTime());
		if (formatted.includes("2023") || formatted.includes("/")) {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "intl_dtf.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("Intl.DateTimeFormat failed, pass=%v", v)
	}
}

// TestIntlNumberFormat 测试数字与货币格式化
func TestIntlNumberFormat(t *testing.T) {
	ctx := newIntlTestContext(t)

	src := `
		globalThis.pass = false;
		var nfCurrency = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });
		var currencyStr = nfCurrency.format(1234567.89);

		var nfDecimal = new Intl.NumberFormat("en-US");
		var decimalStr = nfDecimal.format(1234567.89);

		if (currencyStr === "$1234567.89" && decimalStr === "1,234,567.89") {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "intl_nf.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("Intl.NumberFormat failed, pass=%v", v)
	}
}

// TestIntlRelativeTimeFormat 测试相对时间格式化
func TestIntlRelativeTimeFormat(t *testing.T) {
	ctx := newIntlTestContext(t)

	src := `
		globalThis.pass = false;
		var rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
		var yest = rtf.format(-1, "day");
		var in3Days = rtf.format(3, "day");

		var rtfZh = new Intl.RelativeTimeFormat("zh", { numeric: "auto" });
		var yestZh = rtfZh.format(-1, "day");

		if (yest === "yesterday" && in3Days === "in 3 days" && yestZh === "昨天") {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "intl_rtf.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("Intl.RelativeTimeFormat failed, pass=%v", v)
	}
}

// TestIntlListFormat 测试列表连接格式化
func TestIntlListFormat(t *testing.T) {
	ctx := newIntlTestContext(t)

	src := `
		globalThis.pass = false;
		var lfEn = new Intl.ListFormat("en", { style: "long", type: "conjunction" });
		var enStr = lfEn.format(["Motorcycle", "Bus", "Car"]);

		var lfZh = new Intl.ListFormat("zh");
		var zhStr = lfZh.format(["苹果", "香蕉", "橙子"]);

		if (enStr === "Motorcycle, Bus, and Car" && zhStr === "苹果、香蕉和橙子") {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "intl_lf.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("Intl.ListFormat failed, pass=%v", v)
	}
}

// TestIntlCollator 测试多语言排序
func TestIntlCollator(t *testing.T) {
	ctx := newIntlTestContext(t)

	src := `
		globalThis.pass = false;
		var col = new Intl.Collator("en", { numeric: true });
		var arr = ["item20", "item3", "item1"].sort(col.compare);
		if (arr[0] === "item1" && arr[1] === "item3" && arr[2] === "item20") {
			globalThis.pass = true;
		}
	`
	if _, err := ctx.Eval(src, "intl_col.js"); err != nil {
		t.Fatalf("Eval error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("Intl.Collator failed, pass=%v", v)
	}
}
