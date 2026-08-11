package jit

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// TestInlineCalleeStringConstantMergesPool is the regression for the R3-6
// integration bug: inlining a callee that contains OpConstString must merge
// the callee's string constant pool and remap the inlined operands,
// otherwise the inlined instruction dereferences a nil/foreign object-buffer
// slot and panics.
func TestInlineCalleeStringConstantMergesPool(t *testing.T) {
	// caller: self-recursive shape (PushSelf + SelfCall 1) so the callee
	// specialization path inlines the target body.
	p := &Program{NumLocals: 4, SelfUpvalue: 0}
	p.Code = []Instr{
		{Op: OpPushSelf},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpSelfCall, Operand: 1},
		{Op: OpReturn},
	}
	callee := &Program{NumParams: 1}
	callee.stringConsts = []engine.Value{engine.Str("y"), engine.Str("z")}
	callee.Code = []Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpConstString, Operand: 1}, // "z"
		{Op: OpAdd},
		{Op: OpReturn},
	}
	if !p.inlineCallTarget(callee) {
		t.Fatal("inlineCallTarget rejected a string-constant callee")
	}
	if len(p.stringConsts) != 2 {
		t.Fatalf("pool not merged: %d entries, want 2", len(p.stringConsts))
	}
	remapped := false
	for _, in := range p.Code {
		if in.Op == OpConstString {
			remapped = true
			if int(in.Operand) < 1 || int(in.Operand) >= len(p.stringConsts) {
				t.Fatalf("inlined OpConstString operand %d not remapped into merged pool", in.Operand)
			}
		}
	}
	if !remapped {
		t.Fatal("inlined OpConstString not found")
	}
	if p.stringConsts[1] != engine.Str("z") {
		t.Fatalf("merged pool entry = %v, want z", p.stringConsts[1])
	}
}

// TestBindCallTargetRejectsStringConstantCallee locks the conservative
// fallback: a callee with its own string constant pool cannot run against
// the caller's object buffer in a specialized (non-inlined) call.
func TestBindCallTargetRejectsStringConstantCallee(t *testing.T) {
	p := &Program{NumLocals: 1, SelfUpvalue: 0}
	p.Code = []Instr{{Op: OpLoadLocal, Operand: 1}, {Op: OpReturn}}
	callee := &Program{NumParams: 1}
	callee.stringConsts = []engine.Value{engine.Str("k")}
	callee.Code = []Instr{{Op: OpConstString, Operand: 0}, {Op: OpReturn}}
	if _, err := p.BindCallTarget(callee); err == nil {
		t.Fatal("BindCallTarget accepted a callee with string constants")
	}
}
