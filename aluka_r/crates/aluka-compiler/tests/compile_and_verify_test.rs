//! 前端编译器与字节码验证器/VM 端到端联合测试。
//!
//! 验证 aluka-compiler 发射的代码能够通过 Verifier 的 V1..V16 全部严格静态校验，
//! 并且在 VM 中能够被正确求值。

use aluka_bytecode::BytecodeModule;
use aluka_compiler::compile;
use aluka_parser::ast::{Expr, Program, Stmt};
use aluka_vm::{Value, Vm};

#[test]
fn test_compile_arithmetic_and_verify_and_execute() {
    // 构造表达式: (10 + 20) * 3 - 40 / 2
    // 预期值: 30 * 3 - 20 = 90 - 20 = 70
    let expr = Expr::Binary {
        op: "-".to_owned(),
        left: Box::new(Expr::Binary {
            op: "*".to_owned(),
            left: Box::new(Expr::Binary {
                op: "+".to_owned(),
                left: Box::new(Expr::Number(10.0)),
                right: Box::new(Expr::Number(20.0)),
            }),
            right: Box::new(Expr::Number(3.0)),
        }),
        right: Box::new(Expr::Binary {
            op: "/".to_owned(),
            left: Box::new(Expr::Number(40.0)),
            right: Box::new(Expr::Number(2.0)),
        }),
    };

    let program = Program {
        body: vec![Stmt::Expr(expr)],
    };

    let unit = compile(&program);
    let func = unit.to_func_template("test_arithmetic");

    // 1. 静态验证：发射的代码必须 100% 通过 Verifier 校验
    let module = BytecodeModule {
        version: 30,
        functions: vec![func.clone()],
        classes: Vec::new(),
    };
    assert!(
        module.verify().is_ok(),
        "编译器发射的代码必须通过 Verifier 静态校验"
    );

    // 2. VM 运行求值验证
    let mut vm = Vm::new(0);
    let res = vm.run_func(&func).expect("VM 执行失败");
    match res {
        Value::Number(n) => assert_eq!(n, 70.0, "算术表达式计算结果必须是 70"),
        other => panic!("预期返回 Number(70)，实为 {:?}", other),
    }
}

#[test]
fn test_compile_bitwise_and_unary_and_execute() {
    // 构造表达式: ~( (15 & 7) ^ 2 ) + 10
    // 15 & 7 = 7; 7 ^ 2 = 5; ~5 = -6; -6 + 10 = 4
    let expr = Expr::Binary {
        op: "+".to_owned(),
        left: Box::new(Expr::Unary {
            op: "~".to_owned(),
            expr: Box::new(Expr::Binary {
                op: "^".to_owned(),
                left: Box::new(Expr::Binary {
                    op: "&".to_owned(),
                    left: Box::new(Expr::Number(15.0)),
                    right: Box::new(Expr::Number(7.0)),
                }),
                right: Box::new(Expr::Number(2.0)),
            }),
        }),
        right: Box::new(Expr::Number(10.0)),
    };

    let program = Program {
        body: vec![Stmt::Expr(expr)],
    };

    let unit = compile(&program);
    let func = unit.to_func_template("test_bitwise");

    let module = BytecodeModule {
        version: 30,
        functions: vec![func.clone()],
        classes: Vec::new(),
    };
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let res = vm.run_func(&func).expect("VM 执行失败");
    match res {
        Value::Number(n) => assert_eq!(n, 4.0, "位运算与一元运算结果必须是 4"),
        other => panic!("预期返回 Number(4)，实为 {:?}", other),
    }
}

#[test]
fn test_compile_comparison_and_execute() {
    // 构造表达式: (10 < 20) === true
    let expr = Expr::Binary {
        op: "===".to_owned(),
        left: Box::new(Expr::Binary {
            op: "<".to_owned(),
            left: Box::new(Expr::Number(10.0)),
            right: Box::new(Expr::Number(20.0)),
        }),
        right: Box::new(Expr::Boolean(true)),
    };

    let program = Program {
        body: vec![Stmt::Expr(expr)],
    };

    let unit = compile(&program);
    let func = unit.to_func_template("test_compare");

    let module = BytecodeModule {
        version: 30,
        functions: vec![func.clone()],
        classes: Vec::new(),
    };
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let res = vm.run_func(&func).expect("VM 执行失败");
    match res {
        Value::Boolean(b) => assert!(b, "10 < 20 === true 必须为真"),
        other => panic!("预期返回 Boolean(true)，实为 {:?}", other),
    }
}

#[test]
fn test_compile_variable_declaration_and_scoping() {
    // 构造程序:
    // let a = 10;
    // let b = 20;
    // a = a + b;
    // a * 2;
    // 预期求值: (10 + 20) * 2 = 60
    let program = Program {
        body: vec![
            Stmt::VarDecl {
                name: "a".to_owned(),
                init: Some(Expr::Number(10.0)),
            },
            Stmt::VarDecl {
                name: "b".to_owned(),
                init: Some(Expr::Number(20.0)),
            },
            Stmt::Expr(Expr::Assign {
                name: "a".to_owned(),
                value: Box::new(Expr::Binary {
                    op: "+".to_owned(),
                    left: Box::new(Expr::Ident("a".to_owned())),
                    right: Box::new(Expr::Ident("b".to_owned())),
                }),
            }),
            Stmt::Expr(Expr::Binary {
                op: "*".to_owned(),
                left: Box::new(Expr::Ident("a".to_owned())),
                right: Box::new(Expr::Number(2.0)),
            }),
        ],
    };

    let unit = compile(&program);
    assert_eq!(unit.locals, 2, "应该分配两个局部槽位 a 和 b");
    let func = unit.to_func_template("test_vars");

    let module = BytecodeModule {
        version: 30,
        functions: vec![func.clone()],
        classes: Vec::new(),
    };
    module.verify().expect("字节码校验失败");

    let mut vm = Vm::new(0);
    let res = vm.run_func(&func).expect("VM 执行失败");
    match res {
        Value::Number(n) => assert_eq!(n, 60.0, "预期计算结果为 60"),
        other => panic!("预期返回 Number(60)，实为 {:?}", other),
    }
}

#[test]
fn test_compile_control_flow_if_while_and_execute() {
    // 构造程序:
    // let sum = 0;
    // let i = 1;
    // while (i <= 10) {
    //     sum = sum + i;
    //     i = i + 1;
    // }
    // if (sum > 50) {
    //     sum = sum * 2;
    // } else {
    //     sum = 0;
    // }
    // sum;
    // 预期求值: (1+..+10 = 55) > 50 => 55 * 2 = 110
    let program = Program {
        body: vec![
            Stmt::VarDecl {
                name: "sum".to_owned(),
                init: Some(Expr::Number(0.0)),
            },
            Stmt::VarDecl {
                name: "i".to_owned(),
                init: Some(Expr::Number(1.0)),
            },
            Stmt::While {
                cond: Expr::Binary {
                    op: "<=".to_owned(),
                    left: Box::new(Expr::Ident("i".to_owned())),
                    right: Box::new(Expr::Number(10.0)),
                },
                body: Box::new(Stmt::Block(vec![
                    Stmt::Expr(Expr::Assign {
                        name: "sum".to_owned(),
                        value: Box::new(Expr::Binary {
                            op: "+".to_owned(),
                            left: Box::new(Expr::Ident("sum".to_owned())),
                            right: Box::new(Expr::Ident("i".to_owned())),
                        }),
                    }),
                    Stmt::Expr(Expr::Assign {
                        name: "i".to_owned(),
                        value: Box::new(Expr::Binary {
                            op: "+".to_owned(),
                            left: Box::new(Expr::Ident("i".to_owned())),
                            right: Box::new(Expr::Number(1.0)),
                        }),
                    }),
                ])),
            },
            Stmt::If {
                cond: Expr::Binary {
                    op: ">".to_owned(),
                    left: Box::new(Expr::Ident("sum".to_owned())),
                    right: Box::new(Expr::Number(50.0)),
                },
                then_branch: Box::new(Stmt::Expr(Expr::Assign {
                    name: "sum".to_owned(),
                    value: Box::new(Expr::Binary {
                        op: "*".to_owned(),
                        left: Box::new(Expr::Ident("sum".to_owned())),
                        right: Box::new(Expr::Number(2.0)),
                    }),
                })),
                else_branch: Some(Box::new(Stmt::Expr(Expr::Assign {
                    name: "sum".to_owned(),
                    value: Box::new(Expr::Number(0.0)),
                }))),
            },
            Stmt::Expr(Expr::Ident("sum".to_owned())),
        ],
    };

    let unit = compile(&program);
    let func = unit.to_func_template("test_flow");

    let module = BytecodeModule {
        version: 30,
        functions: vec![func.clone()],
        classes: Vec::new(),
    };
    module.verify().expect("控制流字节码校验失败");

    let mut vm = Vm::new(0);
    let res = vm.run_func(&func).expect("VM 执行失败");
    match res {
        Value::Number(n) => assert_eq!(n, 110.0, "预期 55 * 2 = 110"),
        other => panic!("预期返回 Number(110)，实为 {:?}", other),
    }
}

#[test]
fn test_compile_object_array_and_member_access() {
    // 构造测试:
    // let obj = { x: 10, y: 20 };
    // let arr = [obj.x, obj.y, 30];
    // arr[0] + arr[1] + arr[2]; // 10 + 20 + 30 = 60
    let program = Program {
        body: vec![
            Stmt::VarDecl {
                name: "obj".to_owned(),
                init: Some(Expr::Object(vec![
                    ("x".to_owned(), Expr::Number(10.0)),
                    ("y".to_owned(), Expr::Number(20.0)),
                ])),
            },
            Stmt::VarDecl {
                name: "arr".to_owned(),
                init: Some(Expr::Array(vec![
                    Expr::Member {
                        obj: Box::new(Expr::Ident("obj".to_owned())),
                        prop: "x".to_owned(),
                    },
                    Expr::Member {
                        obj: Box::new(Expr::Ident("obj".to_owned())),
                        prop: "y".to_owned(),
                    },
                    Expr::Number(30.0),
                ])),
            },
            Stmt::Expr(Expr::Binary {
                op: "+".to_owned(),
                left: Box::new(Expr::Binary {
                    op: "+".to_owned(),
                    left: Box::new(Expr::Index {
                        obj: Box::new(Expr::Ident("arr".to_owned())),
                        index: Box::new(Expr::Number(0.0)),
                    }),
                    right: Box::new(Expr::Index {
                        obj: Box::new(Expr::Ident("arr".to_owned())),
                        index: Box::new(Expr::Number(1.0)),
                    }),
                }),
                right: Box::new(Expr::Index {
                    obj: Box::new(Expr::Ident("arr".to_owned())),
                    index: Box::new(Expr::Number(2.0)),
                }),
            }),
        ],
    };

    let unit = compile(&program);
    let func = unit.to_func_template("test_obj_arr");

    let module = BytecodeModule {
        version: 30,
        functions: vec![func.clone()],
        classes: Vec::new(),
    };
    module.verify().expect("对象数组字节码校验失败");

    let mut vm = Vm::new(0);
    let res = vm.run_func(&func).expect("VM 执行失败");
    match res {
        Value::Number(n) => assert_eq!(n, 60.0, "预期 10 + 20 + 30 = 60"),
        other => panic!("预期返回 Number(60)，实为 {:?}", other),
    }
}
