//! 端到端测试共享 helper（Go oracle / 前端编译 / bc 分发）。
//!
//! 并行开发的测试基建：各能力模块的 e2e 测试文件以
//! `mod common;` 引入，只依赖本文件（不触碰 `cjs_test.rs` 与核心 crate）。

use std::path::{Path, PathBuf};
use std::process::Command;

pub fn repo_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .to_path_buf()
}

pub fn aluvm_exe() -> PathBuf {
    Path::new(env!("CARGO_BIN_EXE_aluvm")).to_path_buf()
}

pub fn go_oracle() -> PathBuf {
    if let Ok(p) = std::env::var("ALUKA_ORACLE") {
        return PathBuf::from(p);
    }
    repo_root().join("aluka_g/bin/aluka.exe")
}

/// 递归收集 .bc 文件（排序保证确定性）。
pub fn walk_bc(dir: &Path) -> Vec<PathBuf> {
    let mut out = Vec::new();
    let mut stack = vec![dir.to_path_buf()];
    while let Some(d) = stack.pop() {
        for e in std::fs::read_dir(&d).into_iter().flatten().flatten() {
            let p = e.path();
            if p.is_dir() {
                stack.push(p);
            } else if p.extension().is_some_and(|x| x == "bc") {
                out.push(p);
            }
        }
    }
    out.sort();
    out
}

/// Go 前端编译入口（require 触发整图编译），单入口用例分发为 `<stem>.bc`。
pub fn compile_graph(go_exe: &Path, work: &Path, entry: &str) {
    let src = work.join(entry);
    let out = Command::new(go_exe)
        .arg("run")
        .arg(&src)
        .current_dir(work)
        .output()
        .expect("Go 前端运行失败");
    assert!(out.status.success(), "Go 前端执行 {entry} 失败");

    // 单入口用例：取任一 .bc 分发为 `<stem>.bc`（多模块用例按计划使用特征分发）
    let stem = entry.strip_suffix(".js").unwrap_or(entry);
    let fallback = walk_bc(&work.join("node_modules"))
        .into_iter()
        .next()
        .unwrap_or_else(|| panic!("{entry}: 未捕获到 .bc 缓存"));
    std::fs::copy(&fallback, work.join(format!("{stem}.bc"))).expect("拷贝 .bc 失败");
}

/// 运行 aluvm 并返回输出（trim 后）。
pub fn aluvm_run(bc: &Path) -> String {
    let out = Command::new(aluvm_exe())
        .arg("run")
        .arg(bc)
        .current_dir(bc.parent().expect("bc 有父目录"))
        .output()
        .expect("运行 aluvm 失败");
    assert!(
        out.status.success(),
        "aluvm 执行失败: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8_lossy(&out.stdout).trim().to_string()
}

/// 运行 Go Oracle（源码输入）并返回输出（trim 后）。
pub fn go_run(go_exe: &Path, js: &Path) -> String {
    let out = Command::new(go_exe)
        .arg("run")
        .arg(js)
        .current_dir(js.parent().expect("js 有父目录"))
        .output()
        .expect("运行 Go Oracle 失败");
    String::from_utf8_lossy(&out.stdout).trim().to_string()
}

/// 标准 e2e 一步：写文件 → Go 整图编译 → aluvm 执行 → 与 Go Oracle 逐字对拍。
pub fn assert_e2e_matches_go(work: &Path, entry: &str) -> String {
    let go_exe = go_oracle();
    assert!(
        go_exe.exists(),
        "Go oracle 不存在（CI 经 ALUKA_ORACLE 注入）"
    );
    compile_graph(&go_exe, work, entry);
    let bc = work.join(entry.replace(".js", ".bc"));
    let rust_out = aluvm_run(&bc);
    let go_out = go_run(&go_exe, &work.join(entry));
    assert_eq!(rust_out, go_out, "e2e 输出必须与 Go Oracle 一致（{entry}）");
    rust_out
}
