package jit

import "testing"

// M2 阶段 1：liveness 分析正确性测试。

func mkProg(code []Instr, numLocals int) *Program {
	return &Program{NumLocals: numLocals, Code: code}
}

func TestLocalLivenessStraightLine(t *testing.T) {
	// LOAD 0; ADD; STORE 0; LOAD 0; RETURN
	//        ^       ^        ^
	// local 0 在 STORE 0 之后（第 2 条之后）不再活跃（被覆盖），
	// 在 LOAD 0（第 3 条）处重新活跃。
	p := mkProg([]Instr{
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpAdd},
		{Op: OpStoreLocal, Operand: 0},
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpReturn},
	}, 1)
	live, err := localLiveness(p)
	if err != nil {
		t.Fatal(err)
	}
	// 指令 i 的 live[i] 是执行 i 之后的活跃集合。
	// LOAD 0 (i=0) 之后：0 活跃（供 ADD 用）
	if live[0]&1 == 0 {
		t.Errorf("live[0] = %b, want bit0 set (local 0 live after load)", live[0])
	}
	// STORE 0 (i=2) 之后：0 不再活跃（被覆盖，直到下次 load）
	if live[2]&1 != 0 {
		t.Errorf("live[2] = %b, want bit0 clear (local 0 dead after store)", live[2])
	}
	// LOAD 0 (i=3) 之后：0 活跃（供 return）
	if live[3]&1 == 0 {
		t.Errorf("live[3] = %b, want bit0 set", live[3])
	}
}

func TestLocalLivenessBranch(t *testing.T) {
	// LOAD 0; JUMP_TRUE 2; LOAD 1; JUMP 3; RETURN
	// 两条路径都到 RETURN，local 1 仅在条件为 false 的路径上活跃。
	p := mkProg([]Instr{
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpJumpTrue, Operand: 2},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpJump, Operand: 4},
		{Op: OpReturn},
	}, 2)
	live, err := localLiveness(p)
	if err != nil {
		t.Fatal(err)
	}
	// LOAD 1 (i=2) 之后：local 1 活跃（供 return 路径）
	if live[2]&2 == 0 {
		t.Errorf("live[2] = %b, want bit1 set", live[2])
	}
	// LOAD 0 (i=0) 之后：local 0 活跃；local 1 是否活跃取决于路径。
	if live[0]&1 == 0 {
		t.Errorf("live[0] = %b, want bit0 set", live[0])
	}
}

func TestLocalLivenessLoopBackedge(t *testing.T) {
	// 0: LOAD 0; 1: LOAD 1; 2: ADD; 3: STORE 0; 4: JUMP 0（回边）
	// 循环内 local 0 和 local 1 都持续活跃。
	p := mkProg([]Instr{
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpAdd},
		{Op: OpStoreLocal, Operand: 0},
		{Op: OpJump, Operand: 0},
	}, 2)
	live, err := localLiveness(p)
	if err != nil {
		t.Fatal(err)
	}
	// 回边处（i=4）之前，local 0 和 1 都活跃（下轮循环还要读）。
	if live[4]&3 != 3 {
		t.Errorf("live[4] = %b, want bit0|bit1 set", live[4])
	}
}

func TestAnalyzeHotLocals(t *testing.T) {
	// LOAD 0; LOAD 0; STORE 1; LOAD 1; RETURN
	p := mkProg([]Instr{
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpStoreLocal, Operand: 1},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpReturn},
	}, 2)
	_, rep, err := analyzeHotLocals(p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.useCounts[0] != 2 {
		t.Errorf("useCounts[0] = %d, want 2", rep.useCounts[0])
	}
	if rep.useCounts[1] != 1 {
		t.Errorf("useCounts[1] = %d, want 1", rep.useCounts[1])
	}
}

func TestFindLoop(t *testing.T) {
	p := mkProg([]Instr{
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpAdd},
		{Op: OpStoreLocal, Operand: 0},
		{Op: OpJump, Operand: 0}, // 回边到 0
	}, 1)
	loop, ok := findLoop(p)
	if !ok {
		t.Fatal("expected a loop")
	}
	if loop.header != 0 || loop.backedge != 3 {
		t.Errorf("got header=%d backedge=%d, want 0/3", loop.header, loop.backedge)
	}
}

func TestFindLoopNone(t *testing.T) {
	p := mkProg([]Instr{
		{Op: OpLoadLocal, Operand: 0},
		{Op: OpReturn},
	}, 1)
	if _, ok := findLoop(p); ok {
		t.Fatal("unexpected loop in straight-line program")
	}
}

func TestSelectHotLocals(t *testing.T) {
	// 循环：LOAD 1; LOAD 1; ADD; STORE 2; JUMP 0（slot 0 是 this，跳过）
	p := mkProg([]Instr{
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpLoadLocal, Operand: 1},
		{Op: OpAdd},
		{Op: OpStoreLocal, Operand: 2},
		{Op: OpJump, Operand: 0},
	}, 3)
	// 所有指令都证明 slot 1/2 是 Number。
	assigned := []uint64{6, 6, 6, 6, 6}
	loop, _ := findLoop(p)
	hot := selectHotLocals(p, assigned, loop, 8)
	// slot 1 被读 2 次，slot 2 只被写不被读 → 只选 slot 1。
	if len(hot) != 1 || hot[0] != 1 {
		t.Errorf("hot = %v, want [1]", hot)
	}
}
