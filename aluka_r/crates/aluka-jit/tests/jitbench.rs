//! M5 性能基线：热循环 JIT vs 解释器（方法学：交替执行 + 冷却 + min-of-5）。
//!
//! 负载：countdown 累加循环（`n` 轮 f64 算术），子集内可 JIT。与 node 的
//! 对拍数据由 `.work/evidence/20260905/m5-report.md` 记录（node 侧同语义
//! JS 脚本，跨进程测量）。
//!
//! 本测试断言 JIT **不慢于**解释器（保守门禁，避免机器噪声导致 flaky）；
//! 具体倍数写进证据报告。

use aluka_bytecode::{Constant, FuncTemplate, Instr, Op};
use aluka_jit::jit_compile;
use std::time::Instant;

/// 构造热循环：`n = N; acc = 0; while n > 0 { acc = acc + n * 1.5; n -= 1 } return acc`。
fn hot_loop(iterations: f64) -> FuncTemplate {
    let consts = vec![
        Constant::Number(0.0),        // 0
        Constant::Number(1.0),        // 1
        Constant::Number(iterations), // 2
        Constant::Number(1.5),        // 3
    ];
    let mut code = vec![
        Instr::new(Op::PushConst, 2), // n = N
        Instr::new(Op::StoreLocal, 1),
        Instr::new(Op::PushConst, 0), // acc = 0
        Instr::new(Op::StoreLocal, 2),
    ];
    let loop_start = code.len();
    code.push(Instr::new(Op::LoadLocal, 1));
    code.push(Instr::new(Op::PushConst, 0));
    code.push(Instr::new(Op::Gt, 0));
    let jfp_at = code.len();
    code.push(Instr::new(Op::JmpFalsePop, 0));
    // acc = acc + n * 1.5
    code.push(Instr::new(Op::LoadLocal, 2));
    code.push(Instr::new(Op::LoadLocal, 1));
    code.push(Instr::new(Op::PushConst, 3));
    code.push(Instr::new(Op::Mul, 0));
    code.push(Instr::new(Op::Add, 0));
    code.push(Instr::new(Op::StoreLocal, 2));
    // n -= 1
    code.push(Instr::new(Op::LoadLocal, 1));
    code.push(Instr::new(Op::PushConst, 1));
    code.push(Instr::new(Op::Sub, 0));
    code.push(Instr::new(Op::StoreLocal, 1));
    let jmp_at = code.len();
    code.push(Instr::new(Op::Jmp, 0));
    let exit_pc = code.len();
    code.push(Instr::new(Op::LoadLocal, 2));
    code.push(Instr::new(Op::Return, 0));

    let patch = |code: &mut Vec<Instr>, at: usize, target: usize| {
        let signed = (target as i32 * 4) - ((at as i32 * 4) + 4);
        code[at].operand = (signed as i64 & 0xFF_FFFF) as u32;
    };
    patch(&mut code, jfp_at, exit_pc);
    patch(&mut code, jmp_at, loop_start);

    FuncTemplate {
        name: "hot_loop".to_owned(),
        num_params: 0,
        num_locals: 3,
        is_var_args: false,
        is_generator: false,
        is_async: false,
        is_arrow: false,
        code,
        max_stack: 16,
        source_file: String::new(),
        constants: consts,
        upvalues: Vec::new(),
        try_table: Vec::new(),
    }
}

/// min-of-N 计时（毫秒）。
fn min_of<F: FnMut() -> f64>(rounds: usize, mut f: F) -> (f64, f64) {
    let mut best = f64::INFINITY;
    let mut last = 0.0;
    for _ in 0..rounds {
        let t0 = Instant::now();
        last = f();
        let dt = t0.elapsed().as_secs_f64() * 1000.0;
        if dt < best {
            best = dt;
        }
        std::thread::sleep(std::time::Duration::from_millis(50)); // 冷却
    }
    (best, last)
}

/// 热循环基线：JIT 与解释器结果一致，且 JIT 不慢于解释器。
#[test]
fn hot_loop_jit_not_slower_than_interpreter() {
    let iterations = 200_000.0;
    let func = hot_loop(iterations);
    let jit = jit_compile(&func).expect("JIT 编译");

    // 交替执行 + 冷却 + min-of-5（总 TODO §1 性能方法学）
    let mut interp_best = f64::INFINITY;
    let mut jit_best = f64::INFINITY;
    let mut interp_val = 0.0;
    let mut jit_val = 0.0;
    for _ in 0..5 {
        let (i_ms, i_v) = min_of(1, || {
            let mut vm = aluka_vm::Vm::new(0);
            match vm.run_func(&func).expect("解释执行") {
                aluka_vm::Value::Number(n) => n,
                _ => f64::NAN,
            }
        });
        if i_ms < interp_best {
            interp_best = i_ms;
        }
        interp_val = i_v;
        let (j_ms, j_v) = min_of(1, || jit.call(&[]));
        if j_ms < jit_best {
            jit_best = j_ms;
        }
        jit_val = j_v;
    }

    assert_eq!(
        interp_val.to_bits(),
        jit_val.to_bits(),
        "结果必须逐位一致（interp={interp_val} jit={jit_val}）"
    );
    println!(
        "hot_loop({iterations} 轮) min-of-5: interp={interp_best:.2}ms jit={jit_best:.2}ms 加速={:.1}×",
        interp_best / jit_best.max(f64::MIN_POSITIVE)
    );
    assert!(
        jit_best <= interp_best,
        "JIT 不应慢于解释器（interp={interp_best:.2}ms jit={jit_best:.2}ms）"
    );
}
