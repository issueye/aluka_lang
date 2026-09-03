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
