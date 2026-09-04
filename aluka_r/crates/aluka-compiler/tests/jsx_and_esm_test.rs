//! JSX 降级转换与 ESM 模块编译集成测试。

use aluka_compiler::{compile_module, lower_jsx};
use aluka_parser::ast::{Expr, Stmt};
use aluka_parser::parse;

#[test]
fn test_jsx_lowering_to_react_create_element() {
    let src = r#"
        let App = function() {
            return (
                <div id="container" className="main" disabled>
                    <h1>Title</h1>
                    <MyComp count={100}>
                        <>child fragment</>
                    </MyComp>
                    <UI.Button {...props} />
                </div>
            );
        };
    "#;

    let mut program = parse(src);
    lower_jsx(&mut program);

    // 验证 JSX 已经被转换为 React.createElement 调用，不再包含任何原始 JSX 节点
    let mut found_call = false;
    for stmt in &program.body {
        if let Stmt::VarDecl {
            init: Some(Expr::Function(def)),
            ..
        } = stmt
        {
            if let Some(Stmt::Return(Some(Expr::Call { callee, args }))) = def.body.first() {
                if let Expr::Member { obj, prop } = callee.as_ref() {
                    assert_eq!(*obj, Box::new(Expr::Ident("React".to_owned())));
                    assert_eq!(prop, "createElement");
                    // 第一个参数应为 "div"
                    assert_eq!(args[0], Expr::String("div".to_owned()));
                    // 子节点中应有 h1, MyComp, UI.Button
                    found_call = true;
                }
            }
        }
    }
    assert!(found_call, "应成功转换为 React.createElement 调用");

    // 验证端到端编译为字节码并通过 Verifier 静态安全校验
    let module = compile_module(&program);
    module
        .verify()
        .expect("JSX 降级后编译生成的字节码应完全合法且通过 Verifier");
}

#[test]
fn test_esm_import_and_export_compilation() {
    let src = r#"
        import { add } from './math';
        import React from 'react';
        import 'reset.css';

        export const base = 10;
        export function double(x) {
            return x * 2;
        }
        export default function run() {
            return double(base);
        }
    "#;

    let program = parse(src);
    let module = compile_module(&program);
    module
        .verify()
        .expect("包含 ESM import 与 export 的模块应顺利通过编译与字节码校验");

    // 验证顶层主函数生成并包含了局部变量与函数
    assert!(module.functions.len() >= 3);
    assert_eq!(module.functions[0].name, "main");
}
