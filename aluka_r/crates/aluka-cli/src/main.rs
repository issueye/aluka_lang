//! aluka 命令行入口。
//!
//! M0 阶段只提供 `--version` 与 `--capabilities`（打印装配出的能力域与
//! 内置模块迁移进度）。`run` / `repl` / `build` 等子命令随 M1 起逐步接入，
//! 参数语义以 Go 版 `cmd/aluka` 为准。

use std::process::ExitCode;

use aluka_runtime::Runtime;

const VERSION: &str = env!("CARGO_PKG_VERSION");

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    match args.first().map(String::as_str) {
        Some("--version" | "-v") => {
            println!("aluka {VERSION} (rust skeleton)");
            ExitCode::SUCCESS
        }
        Some("--capabilities") => {
            print_capabilities();
            ExitCode::SUCCESS
        }
        None | Some("--help" | "-h") => {
            print_usage();
            ExitCode::SUCCESS
        }
        Some(other) => {
            eprintln!("aluka: unknown option {other}");
            eprintln!("run `aluka --help` for usage");
            ExitCode::FAILURE
        }
    }
}

fn print_usage() {
    println!("aluka {VERSION} (rust skeleton)");
    println!();
    println!("USAGE:");
    println!("  aluka --version         打印版本");
    println!("  aluka --capabilities    打印已装配能力域与内置模块迁移进度");
    println!();
    println!("run / repl / build 等子命令随里程碑 M1 起接入，");
    println!("参数语义以 Go 版 cmd/aluka 为准。");
}

fn print_capabilities() {
    use aluka_builtins::ModuleStatus;

    let runtime = Runtime::new();
    println!("capabilities ({}):", runtime.capabilities().len());
    for capability in runtime.capabilities() {
        let deps = capability.dependencies();
        if deps.is_empty() {
            println!("  {capability:?}");
        } else {
            println!("  {capability:?} <- {deps:?}");
        }
    }

    let builtins = runtime.builtins();
    println!();
    println!("builtin modules: {} registered", builtins.len());
    println!("  native:  {}", builtins.count(ModuleStatus::Native));
    println!("  bridged: {}", builtins.count(ModuleStatus::ForeignBridge));
    println!("  planned: {}", builtins.count(ModuleStatus::Planned));
}
