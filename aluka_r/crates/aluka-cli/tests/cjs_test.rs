//! CJS 模块系统端到端测试（M1）：Go 前端编译多模块 + 循环依赖 + fs IO，
//! `aluvm` 执行并与 Go Oracle 逐字对拍。
//!
//! 字节码分发约定：`require("./x")` 解析为入口目录下的 `x.bc`
//! （`.js`/无后缀 → `.bc` 替换/补全）。

use std::path::{Path, PathBuf};
use std::process::Command;

fn repo_root() -> PathBuf {
    // CARGO_MANIFEST_DIR = <root>/aluka_r/crates/aluka-cli → 上跳两级到 aluka_r，再一级到仓库根
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .to_path_buf()
}

fn aluvm_exe() -> PathBuf {
    Path::new(env!("CARGO_BIN_EXE_aluvm")).to_path_buf()
}

fn go_oracle() -> PathBuf {
    if let Ok(p) = std::env::var("ALUKA_ORACLE") {
        return PathBuf::from(p);
    }
    repo_root().join("aluka_g/bin/aluka.exe")
}

/// 用 Go 前端跑一次入口（require 触发全图编译），按 disasm 常量特征
/// 把产出的各模块 .bc 分发为 `<stem>.bc`（入口编译的 wrapper 形态依赖
/// 加载器视角，必须整图一次编译，不能逐模块独立编译）。
fn compile_graph(go_exe: &Path, alukac_exe: &Path, dir: &Path, entry: &str) {
    let src = dir.join(entry);
    let out = Command::new(go_exe)
        .arg("run")
        .arg(&src)
        .current_dir(dir) // Go 编译即执行模块：fs 相对路径必须落在工作目录内
        .output()
        .expect("Go 前端运行失败");
    assert!(out.status.success(), "Go 前端执行 {entry} 失败");

    // 特征 → 模块名（常量池在 disasm 报告中的独有字符串）
    let signature_of = |bc: &Path| -> String {
        let out = Command::new(alukac_exe)
            .arg("disasm")
            .arg(bc)
            .output()
            .expect("alukac disasm 失败");
        String::from_utf8_lossy(&out.stdout).to_string()
    };
    let mut assigned: Vec<(PathBuf, &str)> = Vec::new();
    for bc in walk_bc(&dir.join("node_modules")) {
        let sig = signature_of(&bc);
        let stem = if sig.contains("writeFileSync") {
            "app"
        } else if sig.contains("dep loading") {
            "dep"
        } else if sig.contains("loop-b.js") && sig.contains("A(") {
            "loop-a"
        } else if sig.contains("b sees a partially:") {
            "loop-b"
        } else {
            continue;
        };
        let target = dir.join(format!("{stem}.bc"));
        std::fs::copy(&bc, &target).expect("拷贝 .bc 失败");
        assigned.push((target, stem));
    }
    let names: Vec<&str> = assigned.iter().map(|(_, s)| *s).collect();
    if names.contains(&"app") {
        for expected in ["app", "dep", "loop-a", "loop-b"] {
            assert!(
                names.contains(&expected),
                "模块 {expected} 的 .bc 未被识别分发（实际 {names:?}）"
            );
        }
    } else {
        // 单入口用例：全部未识别 bc 中取一个作为入口分发布局
        let stem = entry.strip_suffix(".js").unwrap_or(entry);
        let fallback = walk_bc(&dir.join("node_modules"))
            .into_iter()
            .next()
            .expect("单入口用例应有 bc");
        std::fs::copy(&fallback, dir.join(format!("{stem}.bc"))).expect("拷贝 .bc 失败");
    }
}

/// 递归收集 .bc 文件（按文件名排序保证确定性）。
fn walk_bc(dir: &Path) -> Vec<PathBuf> {
    let mut out = Vec::new();
    let mut stack = vec![dir.to_path_buf()];
    while let Some(d) = stack.pop() {
        let entries = match std::fs::read_dir(&d) {
            Ok(e) => e,
            Err(_) => continue,
        };
        for e in entries.flatten() {
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

#[test]
fn aluvm_cjs_modules_cycle_and_fs_end_to_end() {
    let go_exe = go_oracle();
    if !go_exe.exists() {
        panic!(
            "Go oracle 不存在（CI 经 ALUKA_ORACLE 注入）: {}",
            go_exe.display()
        );
    }
    let work = std::env::temp_dir().join(format!("aluvm_cjs_e2e_{}", std::process::id()));
    std::fs::create_dir_all(&work).expect("创建工作目录");

    // 四模块应用：CJS 基本流 + 循环依赖 + fs IO
    std::fs::write(
        work.join("app.js"),
        concat!(
            "const dep = require(\"./dep.js\");\n",
            "const loop = require(\"./loop-a.js\");\n",
            "const fs = require(\"fs\");\n",
            "fs.writeFileSync(\"m1_io.txt\", \"written-by-aluvm\");\n",
            "const back = fs.readFileSync(\"m1_io.txt\");\n",
            "console.log(\"main got:\", dep.value);\n",
            "console.log(\"loop:\", loop.tag);\n",
            "console.log(\"fs:\", back);\n",
        ),
    )
    .unwrap();
    std::fs::write(
        work.join("dep.js"),
        "console.log(\"dep loading\");\nexports.value = 42;\n",
    )
    .unwrap();
    std::fs::write(
        work.join("loop-a.js"),
        concat!(
            "const b = require(\"./loop-b.js\");\n",
            "exports.tag = \"A(\" + b.tag + \")\";\n",
        ),
    )
    .unwrap();
    std::fs::write(
        work.join("loop-b.js"),
        concat!(
            "const a = require(\"./loop-a.js\");\n",
            "exports.tag = \"B\";\n",
            "console.log(\"b sees a partially:\", typeof a.tag, a.tag === undefined);\n",
        ),
    )
    .unwrap();

    // Go 前端整图编译一次（require 触发全模块），按特征分发为分发布局
    let alukac_exe = Path::new(env!("CARGO_BIN_EXE_alukac")).to_path_buf();
    compile_graph(&go_exe, &alukac_exe, &work, "app.js");

    // Rust VM 执行入口模块
    let out = Command::new(aluvm_exe())
        .arg("run")
        .arg(work.join("app.bc"))
        .current_dir(&work)
        .output()
        .expect("运行 aluvm 失败");
    assert!(
        out.status.success(),
        "CJS 应用应执行成功: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    let rust_out = String::from_utf8_lossy(&out.stdout).trim().to_string();

    // Go Oracle 同负载对照（源码输入）
    let go_out = Command::new(&go_exe)
        .arg("run")
        .arg(work.join("app.js"))
        .current_dir(&work)
        .output()
        .expect("运行 Go Oracle 失败");
    let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();

    println!("Rust:\n{rust_out}\nGo:\n{go_stdout}");
    assert_eq!(
        rust_out, go_stdout,
        "CJS 多模块 + 循环依赖 + fs IO 的输出必须与 Go Oracle 一致"
    );
    // 关键语义断言
    assert!(rust_out.contains("main got: 42"), "exports 传递");
    assert!(rust_out.contains("loop: A(B)"), "循环依赖完成态");
    assert!(
        rust_out.contains("b sees a partially: undefined true"),
        "循环依赖中后加载方持有未完成 exports"
    );
    assert!(rust_out.contains("fs: written-by-aluvm"), "fs 同步读写");

    // fs 落盘验证
    let io = std::fs::read_to_string(work.join("m1_io.txt")).expect("fs 产物读取");
    assert_eq!(io, "written-by-aluvm");
}

/// `node:path` / `fs.existsSync` / `process.env` 轻量内置端到端（M2 内置库起步）。
#[test]
fn aluvm_node_path_fs_env_builtins_e2e() {
    let go_exe = go_oracle();
    if !go_exe.exists() {
        panic!("Go oracle 不存在（CI 经 ALUKA_ORACLE 注入）");
    }
    let work = std::env::temp_dir().join(format!("aluvm_builtins_e2e_{}", std::process::id()));
    std::fs::create_dir_all(&work).expect("创建工作目录");
    std::fs::write(
        work.join("probe.js"),
        concat!(
            "const path = require(\"path\");\n",
            "console.log(\"j1:\" + path.join(\"a\", \"b\"));\n",
            "console.log(\"j2:\" + path.join(\"/x\", \"y\", \"z\"));\n",
            "console.log(\"b1:\" + path.basename(\"/a/b/file.txt\"));\n",
            "console.log(\"b2:\" + path.basename(\"/a/b/file.txt\", \".txt\"));\n",
            "console.log(\"d1:\" + path.dirname(\"/a/b/file.txt\"));\n",
            "console.log(\"e1:\" + path.extname(\"/a/b/file.txt\"));\n",
            "const fs = require(\"fs\");\n",
            "console.log(\"ex:\" + fs.existsSync(\"probe.js\"));\n",
            "console.log(\"no:\" + fs.existsSync(\"definitely_missing_xyz\"));\n",
            "console.log(\"env:\" + (process.env.PATH ? \"yes\" : \"no\"));\n",
        ),
    )
    .unwrap();

    let alukac_exe = Path::new(env!("CARGO_BIN_EXE_alukac")).to_path_buf();
    compile_graph(&go_exe, &alukac_exe, &work, "probe.js");

    let out = Command::new(aluvm_exe())
        .arg("run")
        .arg(work.join("probe.bc"))
        .current_dir(&work)
        .output()
        .expect("运行 aluvm 失败");
    assert!(
        out.status.success(),
        "内置库用例应成功: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    let rust_out = String::from_utf8_lossy(&out.stdout).trim().to_string();

    let go_out = Command::new(&go_exe)
        .arg("run")
        .arg(work.join("probe.js"))
        .current_dir(&work)
        .output()
        .expect("运行 Go Oracle 失败");
    let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();

    println!("Rust:\n{rust_out}\nGo:\n{go_stdout}");
    assert_eq!(
        rust_out, go_stdout,
        "path/fs/env 内置输出必须与 Go Oracle 一致"
    );
}

/// `os` / `new URL(...)` 轻量内置端到端（M2 内置库）。
#[test]
fn aluvm_os_and_url_builtins_e2e() {
    let go_exe = go_oracle();
    if !go_exe.exists() {
        panic!("Go oracle 不存在（CI 经 ALUKA_ORACLE 注入）");
    }
    let work = std::env::temp_dir().join(format!("aluvm_osurl_e2e_{}", std::process::id()));
    std::fs::create_dir_all(&work).expect("创建工作目录");
    std::fs::write(
        work.join("probe.js"),
        concat!(
            "const os = require(\"os\");\n",
            "console.log(\"p:\" + os.platform());\n",
            "console.log(\"h:\" + os.homedir());\n",
            "console.log(\"t:\" + os.tmpdir());\n",
            "const u = new URL(\"https://user:pass@example.com:8080/p/q?a=1&b=2#frag\");\n",
            "console.log(\"q:\" + u.protocol + \"|\" + u.hostname + \"|\" + u.port + \"|\" + u.pathname + \"|\" + u.search + \"|\" + u.hash + \"|\" + u.href);\n",
            "console.log(\"s:\" + u.host + \"|\" + u.origin);\n",
        ),
    )
    .unwrap();

    let alukac_exe = Path::new(env!("CARGO_BIN_EXE_alukac")).to_path_buf();
    compile_graph(&go_exe, &alukac_exe, &work, "probe.js");

    let out = Command::new(aluvm_exe())
        .arg("run")
        .arg(work.join("probe.bc"))
        .current_dir(&work)
        .output()
        .expect("运行 aluvm 失败");
    assert!(
        out.status.success(),
        "os/url 用例应成功: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    let rust_out = String::from_utf8_lossy(&out.stdout).trim().to_string();
    let go_out = Command::new(&go_exe)
        .arg("run")
        .arg(work.join("probe.js"))
        .current_dir(&work)
        .output()
        .expect("运行 Go Oracle 失败");
    let go_stdout = String::from_utf8_lossy(&go_out.stdout).trim().to_string();
    println!("Rust:\n{rust_out}\nGo:\n{go_stdout}");
    assert_eq!(rust_out, go_stdout, "os/url 内置输出必须与 Go Oracle 一致");
}
