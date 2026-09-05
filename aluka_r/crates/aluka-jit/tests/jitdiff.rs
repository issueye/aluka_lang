//! M5 jitdiff：生成式差分——同一子集字节码在解释器与 JIT 上零失配。
//!
//! 生成器构造随机算术/条件/有界循环函数，三路验证：
//! 1. `BytecodeModule::verify`（静态 ISA 规范）；
//! 2. aluvm 解释器执行（`Vm::run_func`）；
//! 3. `aluka-jit` 机器码执行。
//!
//! 解释器与 JIT 的 f64 结果**逐位相等**（含 NaN/Infinity 位型一致）。
//!
//! 生成的函数不读实参（解释器经帧槽绑定、JIT 经指针数组，参数约定不同），
//! 随机程序以常量初始化局部，保证两侧执行同一份纯数值计算。

use aluka_bytecode::{BytecodeModule, Constant, FuncTemplate, Instr, Op};
use aluka_jit::jit_compile;
use aluka_vm::Value;

/// 简易确定性 RNG（xorshift64，可复现）。
struct Rng(u64);

impl Rng {
    fn new(seed: u64) -> Self {
        Self(seed | 1)
    }
    fn next(&mut self) -> u64 {
        let mut x = self.0;
        x ^= x << 13;
        x ^= x >> 7;
        x ^= x << 17;
        self.0 = x;
        x
    }
    fn below(&mut self, n: u64) -> u64 {
        self.next() % n
    }
}

/// 数值化 VM 结果（Boolean → 1.0/0.0，与 JIT 表示对齐）。
fn to_f64(v: Value) -> f64 {
    match v {
        Value::Number(n) => n,
        Value::Boolean(b) => {
            if b {
                1.0
            } else {
                0.0
            }
        }
        _ => f64::NAN,
    }
}

/// 生成随机子集函数：常量初始化局部 → 有界 countdown 循环 → 尾表达式。
fn generate(rng: &mut Rng, id: usize) -> FuncTemplate {
    let mut consts: Vec<Constant> = vec![
        Constant::Number(0.0), // 0
        Constant::Number(1.0), // 1
        Constant::Number(2.0), // 2
    ];
    let mut code: Vec<Instr> = Vec::new();
    let n_slot = 1u32;
    let acc: [u32; 3] = [2, 3, 4];

    // n = 1..=12（有界终止）
    let n0 = 1.0 + rng.below(12) as f64;
    let idx_n0 = consts.len() as u32;
    consts.push(Constant::Number(n0));
    code.push(Instr::new(Op::PushConst, idx_n0));
    code.push(Instr::new(Op::StoreLocal, n_slot));

    for &slot in &acc {
        let v = (rng.below(50) as f64) - 20.0 + (rng.below(4) as f64) / 4.0;
        let idx = consts.len() as u32;
        consts.push(Constant::Number(v));
        code.push(Instr::new(Op::PushConst, idx));
        code.push(Instr::new(Op::StoreLocal, slot));
    }

    // while n > 0 { acc0 = acc0 op1 acc1; acc1 = acc1 op2 k; n -= 1 }
    let loop_start = code.len();
    code.push(Instr::new(Op::LoadLocal, n_slot));
    code.push(Instr::new(Op::PushConst, 0));
    code.push(Instr::new(Op::Gt, 0));
    let jfp_at = code.len();
    code.push(Instr::new(Op::JmpFalsePop, 0));

    let op1 = match rng.below(4) {
        0 => Op::Add,
        1 => Op::Sub,
        2 => Op::Mul,
        _ => Op::Div,
    };
    code.push(Instr::new(Op::LoadLocal, acc[0]));
    code.push(Instr::new(Op::LoadLocal, acc[1]));
    code.push(Instr::new(op1, 0));
    code.push(Instr::new(Op::StoreLocal, acc[0]));

    let op2 = match rng.below(4) {
        0 => Op::Add,
        1 => Op::Sub,
        2 => Op::Mul,
        _ => Op::Div,
    };
    let k = 1.0 + (rng.below(6) as f64) / 2.0;
    let idx_k = consts.len() as u32;
    consts.push(Constant::Number(k));
    code.push(Instr::new(Op::LoadLocal, acc[1]));
    code.push(Instr::new(Op::PushConst, idx_k));
    code.push(Instr::new(op2, 0));
    code.push(Instr::new(Op::StoreLocal, acc[1]));

    code.push(Instr::new(Op::LoadLocal, n_slot));
    code.push(Instr::new(Op::PushConst, 1));
    code.push(Instr::new(Op::Sub, 0));
    code.push(Instr::new(Op::StoreLocal, n_slot));

    let jmp_at = code.len();
    code.push(Instr::new(Op::Jmp, 0));
    let exit_pc = code.len();

    // 尾表达式：随机组合累加器，可选比较收尾
    let tail_op = match rng.below(3) {
        0 => Op::Add,
        1 => Op::Sub,
        _ => Op::Mul,
    };
    code.push(Instr::new(Op::LoadLocal, acc[0]));
    code.push(Instr::new(Op::LoadLocal, acc[1]));
    code.push(Instr::new(tail_op, 0));
    if rng.below(2) == 0 {
        code.push(Instr::new(Op::LoadLocal, acc[2]));
        code.push(Instr::new(Op::Add, 0));
    }
    if rng.below(3) == 0 {
        code.push(Instr::new(Op::PushConst, 2));
        let cmp = match rng.below(3) {
            0 => Op::Lt,
            1 => Op::Gt,
            _ => Op::Eq,
        };
        code.push(Instr::new(cmp, 0));
    }
    code.push(Instr::new(Op::Return, 0));

    // 回填跳转（相对下一指令的字节偏移）
    let patch = |code: &mut Vec<Instr>, at: usize, target: usize| {
        let signed = (target as i32 * 4) - ((at as i32 * 4) + 4);
        code[at].operand = (signed as i64 & 0xFF_FFFF) as u32;
    };
    patch(&mut code, jfp_at, exit_pc);
    patch(&mut code, jmp_at, loop_start);

    FuncTemplate {
        name: format!("jitdiff_{id}"),
        num_params: 0,
        num_locals: 5,
        is_var_args: false,
        is_generator: false,
        is_async: false,
        is_arrow: false,
        code,
        max_stack: 64,
        source_file: format!("gen_{id}"),
        constants: consts,
        upvalues: Vec::new(),
        try_table: Vec::new(),
    }
}

/// 解释器执行。
fn interp_run(func: &FuncTemplate) -> f64 {
    let mut vm = aluka_vm::Vm::new(0);
    let ret = vm.run_func(func).expect("解释器执行");
    to_f64(ret)
}

/// jitdiff 主体：3200 例生成式差分，解释器 vs JIT 逐位相等。
#[test]
fn jitdiff_3200_generated_cases_zero_mismatch() {
    let mut rng = Rng::new(0x4D35_2026);
    let mut mismatch = 0usize;
    let mut executed = 0usize;
    for id in 0..3200usize {
        let func = generate(&mut rng, id);
        let module = BytecodeModule {
            version: 30,
            functions: vec![func.clone()],
            classes: Vec::new(),
        };
        if let Err(e) = module.verify() {
            panic!("case {id} 未过静态校验: {e:?}");
        }
        let expected = interp_run(&func);
        let jit = match jit_compile(&func) {
            Ok(j) => j,
            Err(e) => panic!("case {id} JIT 编译失败: {e:?}"),
        };
        let got = jit.call(&[]);
        executed += 1;
        if expected.to_bits() != got.to_bits() {
            mismatch += 1;
            eprintln!("失配 case {id}: interp={expected:?} jit={got:?}");
            if mismatch > 5 {
                break;
            }
        }
    }
    assert_eq!(mismatch, 0, "{executed} 例中存在失配");
    assert!(executed >= 3000, "差分例数须 ≥3000（实际 {executed}）");
}
