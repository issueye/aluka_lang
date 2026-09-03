package jitdiff

import (
	"fmt"
	"strings"
)

var valueDomainOperations = []string{"return", "shortCircuit", "comparison", "guardChange"}

type valueDomainSpec struct {
	name  string
	value string
	same  string
	other string
	warm  string
}

func valueDomainSpecs() []valueDomainSpec {
	specs := make([]valueDomainSpec, 0, len(numberLeaves)+len(stringLeaves)+len(bigintLeaves)+8)
	for _, value := range numberLeaves {
		specs = append(specs, valueDomainSpec{
			name: "number:" + value, value: value, same: value, other: "1", warm: "true",
		})
	}
	for _, spec := range []valueDomainSpec{
		{name: "boolean:true", value: "true", same: "true", other: "false", warm: "1"},
		{name: "boolean:false", value: "false", same: "false", other: "true", warm: "1"},
		{name: "null", value: "null", same: "null", other: "undefined", warm: "1"},
		{name: "undefined", value: "undefined", same: "undefined", other: "null", warm: "1"},
	} {
		specs = append(specs, spec)
	}
	for _, value := range stringLeaves {
		specs = append(specs, valueDomainSpec{
			name: "string:" + value, value: value, same: value, other: `"different"`, warm: "1",
		})
	}
	for _, value := range bigintLeaves {
		specs = append(specs, valueDomainSpec{
			name: "bigint:" + value, value: value, same: value, other: "99n", warm: "1",
		})
	}
	specs = append(specs,
		valueDomainSpec{name: "symbol:SYM1", value: "SYM1", same: "SYM1", other: "SYM2", warm: "1"},
		valueDomainSpec{name: "symbol:SYM2", value: "SYM2", same: "SYM2", other: "SYM1", warm: "1"},
		valueDomainSpec{name: "object:OBJ_A", value: "OBJ_A", same: "OBJ_A", other: "OBJ_B", warm: "1"},
		valueDomainSpec{name: "object:OBJ_B", value: "OBJ_B", same: "OBJ_B", other: "OBJ_A", warm: "1"},
	)
	return specs
}

// valueDomainCases creates deterministic R1-2 cases. Every value is used in
// a return, all three short-circuit operators, strict comparison, and a hot
// function called first with another type so the second call exercises a
// runtime type guard change.
func valueDomainCases() []*Case {
	specs := valueDomainSpecs()
	cases := make([]*Case, 0, len(specs))
	params := Params{MaxExprDepth: 3, MaxLoopBound: 24, TraceBudget: 3}
	for i, spec := range specs {
		fn := fmt.Sprintf("vd%d", i)
		id := -1000 - i
		var body strings.Builder
		fmt.Fprintf(&body, "function %sRet(v) { return v; }\n", fn)
		fmt.Fprintf(&body, `function %sShort(v, mode) {
  if (mode === 0) return v && 11;
  if (mode === 1) return v || 12;
  return v ?? 13;
}
`, fn)
		fmt.Fprintf(&body, `function %sCompare(v, same, other) {
  let result = 0;
  if (v === same) result += 1;
  if (v !== other) result += 2;
  return result;
}
`, fn)
		fmt.Fprintf(&body, "function %sGuard(v) { if (v) return 1; return 0; }\n", fn)
		body.WriteString(tryLog(id, fmt.Sprintf("%sRet(%s)", fn, spec.value)))
		for mode := 0; mode < 3; mode++ {
			body.WriteString(tryLog(id, fmt.Sprintf("%sShort(%s, %d)", fn, spec.value, mode)))
		}
		body.WriteString(tryLog(id, fmt.Sprintf("%sCompare(%s, %s, %s)", fn, spec.value, spec.same, spec.other)))
		body.WriteString(tryLog(id, fmt.Sprintf("%sGuard(%s)", fn, spec.value)))
		body.WriteString(tryLog(id, fmt.Sprintf("%sGuard(%s)", fn, spec.value)))
		body.WriteString(tryLog(id, fmt.Sprintf("%sGuard(%s)", fn, spec.warm)))

		c := &Case{
			ID: id, Kind: KindBranch, Seed: int64(1000 + i), Params: params,
			ValueDomain: spec.name,
			Coverage:    append([]string(nil), valueDomainOperations...),
			Body:        body.String(),
		}
		c.applySource()
		cases = append(cases, c)
	}
	return cases
}
