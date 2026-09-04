//! 黄金语料执行对拍测试（D1 支撑 / VM 执行验收）。
//!
//! 验证 aluka-vm 能够正确执行收割自 Go 前端的真实 .bc 二进制，
//! 并断言求值结果与 Go 引擎（Oracle）完全一致。

use aluka_bytecode::BytecodeModule;
use aluka_vm::{Value, Vm};
use std::fs;
use std::path::PathBuf;
use std::process::Command;

fn get_corpus_path(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .join("tests")
        .join("golden")
        .join("corpus")
        .join(name)
}

fn get_source_path(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .join("tests")
        .join("golden")
        .join("sources")
        .join(format!("{name}.js"))
}

fn get_go_oracle_exe() -> PathBuf {
    // CI（ubuntu）无法使用仓库内的 Windows 预编译 oracle：rust job 先用 Go
    // 构建同平台 oracle，再经 ALUKA_ORACLE 注入路径（见 .github/workflows/ci.yml）。
    if let Ok(path) = std::env::var("ALUKA_ORACLE") {
        return PathBuf::from(path);
    }
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .join("aluka_g")
        .join("bin")
        .join("aluka.exe")
}

#[test]
fn test_execute_01_arithmetic_bitwise() {
    let bc_path = get_corpus_path("01_arithmetic_bitwise.bc");
    let data = fs::read(&bc_path).expect("读取 01_arithmetic_bitwise.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 01_arithmetic_bitwise 失败");

    // 01_arithmetic_bitwise.js 最后是 console.log(...)，返回值为 undefined
    assert!(matches!(result, Value::Undefined));
    assert_eq!(vm.stdout_records.len(), 1, "期望有 1 行输出");

    let rust_output = vm.stdout_records[0].clone();
    println!("Rust aluka-vm 输出: {}", rust_output);

    // 调用 Go Oracle 验证对拍
    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("01_arithmetic_bitwise");
    if go_exe.exists() && src_path.exists() {
        let output = Command::new(&go_exe).arg("run").arg(&src_path).output();
        if let Ok(go_out) = output {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            println!("Go Oracle  输出: {}", go_stdout);
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "Rust VM 与 Go Oracle 输出必须完全一致！"
            );
        }
    }
}

#[test]
fn test_execute_02_literals_and_stack() {
    let bc_path = get_corpus_path("02_literals_and_stack.bc");
    let data = fs::read(&bc_path).expect("读取 02_literals_and_stack.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 02_literals_and_stack 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 02 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("02_literals_and_stack");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "02 用例输出对拍必须一致"
            );
        }
    }
}

#[test]
fn test_execute_03_comparisons() {
    let bc_path = get_corpus_path("03_comparisons.bc");
    let data = fs::read(&bc_path).expect("读取 03_comparisons.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm.run_module(&module).expect("执行 03_comparisons 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 03 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("03_comparisons");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "03 用例输出对拍必须一致"
            );
        }
    }
}

#[test]
fn test_execute_04_control_flow_jumps() {
    let bc_path = get_corpus_path("04_control_flow_jumps.bc");
    let data = fs::read(&bc_path).expect("读取 04_control_flow_jumps.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 04_control_flow_jumps 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 04 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("04_control_flow_jumps");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "04 用例输出对拍必须一致"
            );
        }
    }
}

#[test]
fn test_execute_07_objects_and_properties() {
    let bc_path = get_corpus_path("07_objects_and_properties.bc");
    let data = fs::read(&bc_path).expect("读取 07_objects_and_properties.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 07_objects_and_properties 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 07 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("07_objects_and_properties");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "07 对象与属性用例输出对拍必须完全一致"
            );
        }
    }
}

#[test]
fn test_execute_05_optional_chaining() {
    let bc_path = get_corpus_path("05_optional_chaining.bc");
    let data = fs::read(&bc_path).expect("读取 05_optional_chaining.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 05_optional_chaining 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 05 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("05_optional_chaining");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "05 可选链用例输出对拍必须完全一致"
            );
        }
    }
}

#[test]
fn test_execute_06_closures_and_upvalues() {
    let bc_path = get_corpus_path("06_closures_and_upvalues.bc");
    let data = fs::read(&bc_path).expect("读取 06_closures_and_upvalues.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 06_closures_and_upvalues 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 06 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("06_closures_and_upvalues");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "06 闭包与上值用例输出对拍必须完全一致"
            );
        }
    }
}

#[test]
fn test_execute_08_arrays_and_methods() {
    let bc_path = get_corpus_path("08_arrays_and_methods.bc");
    let data = fs::read(&bc_path).expect("读取 08_arrays_and_methods.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 08_arrays_and_methods 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 08 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("08_arrays_and_methods");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "08 数组与解构用例输出对拍必须完全一致"
            );
        }
    }
}

#[test]
fn test_execute_09_classes_and_inheritance() {
    let bc_path = get_corpus_path("09_classes_and_inheritance.bc");
    let data = fs::read(&bc_path).expect("读取 09_classes_and_inheritance.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 09_classes_and_inheritance 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 09 输出:\n{}", rust_output);

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("09_classes_and_inheritance");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "09 类与继承用例输出对拍必须完全一致"
            );
        }
    }
}

#[test]
fn test_execute_10_try_catch_finally() {
    let bc_path = get_corpus_path("10_try_catch_finally.bc");
    let data = fs::read(&bc_path).expect("读取 10_try_catch_finally.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 10_try_catch_finally 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 10 输出:\n{}", rust_output);
    // 期望：try-try_end-finally / try-catch:fail-finally（后者走 THROW→catch→finally）
    assert_eq!(
        rust_output.trim(),
        "try-try_end-finally\ntry-catch:fail-finally",
        "try/catch/finally 语义必须与 Go Oracle 一致"
    );

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("10_try_catch_finally");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "10 try/catch/finally 用例输出对拍必须完全一致"
            );
        }
    }
}

#[test]
fn test_execute_27_chained_try_finally() {
    let bc_path = get_corpus_path("27_chained_try_finally.bc");
    let data = fs::read(&bc_path).expect("读取 27_chained_try_finally.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 27_chained_try_finally 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 27 输出:\n{}", rust_output);
    // 期望：VAL（return 穿过嵌套 finally B、A 后值不被吞）
    assert_eq!(
        rust_output.trim(),
        "VAL",
        "嵌套 finally 的 return 穿越语义必须正确"
    );

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("27_chained_try_finally");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "27 嵌套 finally 用例输出对拍必须完全一致"
            );
        }
    }
}

#[test]
fn test_execute_32_try_exit_jmp_loop() {
    let bc_path = get_corpus_path("32_try_exit_jmp_loop.bc");
    let data = fs::read(&bc_path).expect("读取 32_try_exit_jmp_loop.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .expect("执行 32_try_exit_jmp_loop 失败");
    assert!(matches!(result, Value::Undefined));

    let rust_output = vm.stdout_records.join("\n");
    println!("Rust 32 输出:\n{}", rust_output);
    // 期望：fin: 0 / fin: 1（i==1 时 break 经 TRY_EXIT_JMP 先跑 finally 再跳出循环）
    assert_eq!(
        rust_output.trim(),
        "fin: 0\nfin: 1",
        "break 穿越 finally 语义必须正确"
    );

    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path("32_try_exit_jmp_loop");
    if go_exe.exists() && src_path.exists() {
        if let Ok(go_out) = Command::new(&go_exe).arg("run").arg(&src_path).output() {
            let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
            assert_eq!(
                rust_output.trim(),
                go_stdout.trim(),
                "32 break 穿越用例输出对拍必须完全一致"
            );
        }
    }
}

/// 参数化对拍驱动：加载语料 → Verifier 校验 → VM 执行 → 与 Go Oracle 逐字比对。
fn assert_corpus_matches_go(stem: &str) {
    let bc_path = get_corpus_path(&format!("{stem}.bc"));
    let data = fs::read(&bc_path).unwrap_or_else(|e| panic!("读取 {stem}.bc 失败: {e}"));
    let module = BytecodeModule::deserialize_go(&data)
        .unwrap_or_else(|e| panic!("反序列化 {stem} 失败: {e}"));
    module
        .verify()
        .unwrap_or_else(|e| panic!("{} 字节码校验失败: {e}", stem));

    let mut vm = Vm::new(0);
    let result = vm
        .run_module(&module)
        .unwrap_or_else(|e| panic!("执行 {stem} 失败: {e}"));
    assert!(
        matches!(result, Value::Undefined),
        "{stem} 顶层应为 undefined"
    );

    let rust_output = vm.stdout_records.join("\n");
    let go_exe = get_go_oracle_exe();
    let src_path = get_source_path(stem);
    assert!(
        go_exe.exists() && src_path.exists(),
        "Oracle 或源码缺失: {stem}"
    );
    let go_out = Command::new(&go_exe)
        .arg("run")
        .arg(&src_path)
        .output()
        .expect("运行 Go Oracle 失败");
    let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
    println!("Rust {stem} 输出:\n{rust_output}");
    assert_eq!(
        rust_output.trim(),
        go_stdout,
        "{stem} 输出与 Go Oracle 必须完全一致"
    );
}

#[test]
fn test_execute_15_update_expressions() {
    assert_corpus_matches_go("15_update_expressions");
}

#[test]
fn test_execute_17_in_and_instanceof() {
    assert_corpus_matches_go("17_in_and_instanceof");
}

#[test]
fn test_execute_18_switch_statement() {
    assert_corpus_matches_go("18_switch_statement");
}

#[test]
fn test_execute_23_computed_getter_setter() {
    assert_corpus_matches_go("23_computed_getter_setter");
}

#[test]
fn test_execute_24_typeof_global() {
    assert_corpus_matches_go("24_typeof_global");
}

#[test]
fn test_execute_25_call_this_constructor() {
    assert_corpus_matches_go("25_call_this_constructor");
}

#[test]
fn test_execute_29_dynamic_arithmetic_ops() {
    assert_corpus_matches_go("29_dynamic_arithmetic_ops");
}

#[test]
fn test_execute_30_dynamic_globals_and_undef() {
    assert_corpus_matches_go("30_dynamic_globals_and_undef");
}

#[test]
fn test_execute_12_for_in_keys() {
    assert_corpus_matches_go("12_for_in_keys");
}

#[test]
fn test_execute_16_destructuring_and_spread() {
    assert_corpus_matches_go("16_destructuring_and_spread");
}

#[test]
fn test_execute_20_apply_and_spread_call() {
    assert_corpus_matches_go("20_apply_and_spread_call");
}

#[test]
fn test_execute_28_super_methods() {
    assert_corpus_matches_go("28_super_methods");
}

#[test]
fn test_execute_31_dynamic_props_and_spread() {
    assert_corpus_matches_go("31_dynamic_props_and_spread");
}

#[test]
fn test_execute_11_generators_and_iterators() {
    assert_corpus_matches_go("11_generators_and_iterators");
}

#[test]
fn test_execute_13_async_await() {
    assert_corpus_matches_go("13_async_await");
}

#[test]
fn test_execute_26_for_await_of() {
    assert_corpus_matches_go("26_for_await_of");
}

#[test]
fn test_execute_14_regexp_and_types() {
    assert_corpus_matches_go("14_regexp_and_types");
}

#[test]
fn test_execute_19_while_dowhile() {
    assert_corpus_matches_go("19_while_dowhile");
}

#[test]
fn test_execute_21_template_literals() {
    assert_corpus_matches_go("21_template_literals");
}

#[test]
fn test_execute_22_nested_closures() {
    assert_corpus_matches_go("22_nested_closures");
}

#[test]
fn test_execute_99_synthetic_special_opcodes() {
    // 99 号是合成模块（harvest 脚本直构 .bc，覆盖遗留/内部指令），没有 JS 源码，
    // 无法做「源码 vs Go Oracle」对拍——改为断言其可被 VM 完整加载、校验并执行
    //（遗留指令的语义正确性由专用单测与 32 个真实语料对拍共同背书）。
    let bc_path = get_corpus_path("99_synthetic_special_opcodes.bc");
    let data = fs::read(&bc_path).expect("读取 99_synthetic_special_opcodes.bc 失败");
    let module = BytecodeModule::deserialize_go(&data).expect("反序列化模块失败");
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    vm.run_module(&module)
        .expect("合成模块（遗留指令）应能在 Rust VM 上完整执行");
}
