package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 操作数类别名称映射
var 操作数类别名称 = map[string]string{
	"OperandNone":           "OperandNone(无)",
	"OperandConstIdx":       "OperandConstIdx(常量池索引)",
	"OperandInt":            "OperandInt(24位整数)",
	"OperandSlot":           "OperandSlot(局部槽位)",
	"OperandUpvalueIdx":     "OperandUpvalueIdx(闭包上值索引)",
	"OperandTemplateIdx":    "OperandTemplateIdx(模板索引)",
	"OperandTryIdx":         "OperandTryIdx(Try表索引)",
	"OperandSignedOff":      "OperandSignedOff(有符号相对跳转偏移)",
	"OperandCount":          "OperandCount(参数或元素数量)",
	"OperandPackedSlotName": "OperandPackedSlotName(打包槽位与属性名)",
	"OperandPackedCall":     "OperandPackedCall(打包参数数量与方法名)",
}

// 操作数类别数值映射（对应 Go 侧 OperandKind iota）
var 操作数类别数值 = map[string]uint8{
	"OperandNone":           0,
	"OperandConstIdx":       1,
	"OperandInt":            2,
	"OperandSlot":           3,
	"OperandUpvalueIdx":     4,
	"OperandTemplateIdx":    5,
	"OperandTryIdx":         6,
	"OperandSignedOff":      7,
	"OperandCount":          8,
	"OperandPackedSlotName": 9,
	"OperandPackedCall":     10,
}

// 指令事实条目
type 指令事实条目 struct {
	Opcode       uint8  `json:"opcode"`
	Ident        string `json:"ident"`
	Name         string `json:"name"`
	OperandKindId uint8 `json:"operand_kind_id"`
	OperandKind  string `json:"operand_kind"`
	OperandDesc  string `json:"operand_desc"`
	HasOperand   bool   `json:"has_operand"`
	Pops         uint8  `json:"pops"`
	Pushes       uint8  `json:"pushes"`
	StackEffect  string `json:"stack_effect"`
	PurePush     bool   `json:"pure_push"`
	IsJump       bool   `json:"is_jump"`
	IsTerminal   bool   `json:"is_terminal"`
	StackCond    bool   `json:"stack_cond"`
	VarStack     bool   `json:"var_stack"`
	SpecialNotes string `json:"special_notes"`
}

func main() {
	fmt.Println("开始从 aluka_g AST 源码反推并导出 106 条 ISA 事实表...")

	// 定位源码路径
	根目录, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法获取绝对路径: %v\n", err)
		os.Exit(1)
	}

	操作码文件路径 := filepath.Join(根目录, "aluka_g", "internal", "engine", "bytecode", "opcodes.go")
	元数据文件路径 := filepath.Join(根目录, "aluka_g", "internal", "engine", "bytecode", "meta.go")

	文件集 := token.NewFileSet()

	// 1. 解析 opcodes.go，提取所有指令标识符与 iota 数值
	操作码语法树, err := parser.ParseFile(文件集, 操作码文件路径, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析 opcodes.go 失败: %v\n", err)
		os.Exit(1)
	}

	var 操作码标识列表 []string
	标识到数值 := make(map[string]uint8)
	数值到标识 := make(map[uint8]string)

	当前数值 := 0
	在指令常量块 := false

	for _, 声明 := range 操作码语法树.Decls {
		通用声明, 是通用声明 := 声明.(*ast.GenDecl)
		if !是通用声明 || 通用声明.Tok != token.CONST {
			continue
		}

		for _, 规格 := range 通用声明.Specs {
			值规格, 是值规格 := 规格.(*ast.ValueSpec)
			if !是值规格 {
				continue
			}

			for _, 标识 := range 值规格.Names {
				if 标识.Name == "OpNop" {
					在指令常量块 = true
					当前数值 = 0
				}
				if 在指令常量块 {
					操作码标识列表 = append(操作码标识列表, 标识.Name)
					标识到数值[标识.Name] = uint8(当前数值)
					数值到标识[uint8(当前数值)] = 标识.Name
					当前数值++
					if 标识.Name == "OpEnd" {
						在指令常量块 = false
						break
					}
				}
			}
			if !在指令常量块 && len(操作码标识列表) > 0 {
				break
			}
		}
		if len(操作码标识列表) > 0 {
			break
		}
	}

	if len(操作码标识列表) != 106 {
		fmt.Fprintf(os.Stderr, "从 opcodes.go 解析出的指令数量不为 106，实为 %d\n", len(操作码标识列表))
		os.Exit(1)
	}
	fmt.Printf("成功从 opcodes.go 识别 %d 条指令（从 %s=0 到 %s=105）\n", len(操作码标识列表), 操作码标识列表[0], 操作码标识列表[len(操作码标识列表)-1])

	// 2. 解析 meta.go，提取 opMeta 表定义
	元数据语法树, err := parser.ParseFile(文件集, 元数据文件路径, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析 meta.go 失败: %v\n", err)
		os.Exit(1)
	}

	type 元数据结构 struct {
		Name       string
		Operand    string
		Pops       uint8
		Pushes     uint8
		PurePush   bool
		IsJump     bool
		IsTerminal bool
		StackCond  bool
		VarStack   bool
	}

	元数据映射 := make(map[string]元数据结构)

	for _, 声明 := range 元数据语法树.Decls {
		通用声明, 是通用声明 := 声明.(*ast.GenDecl)
		if !是通用声明 || 通用声明.Tok != token.VAR {
			continue
		}

		for _, 规格 := range 通用声明.Specs {
			值规格, 是值规格 := 规格.(*ast.ValueSpec)
			if !是值规格 {
				continue
			}

			for 下标, 标识 := range 值规格.Names {
				if 标识.Name != "opMeta" {
					continue
				}

				字面量, 是字面量 := 值规格.Values[下标].(*ast.CompositeLit)
				if !是字面量 {
					continue
				}

				for _, 元素 := range 字面量.Elts {
					键值表达式, 是键值 := 元素.(*ast.KeyValueExpr)
					if !是键值 {
						continue
					}

					键标识, 是键标识 := 键值表达式.Key.(*ast.Ident)
					if !是键标识 {
						continue
					}

					条目字面量, 是条目字面量 := 键值表达式.Value.(*ast.CompositeLit)
					if !是条目字面量 {
						continue
					}

					条目 := 元数据结构{Operand: "OperandNone"}

					for _, 字段元素 := range 条目字面量.Elts {
						字段键值, 是字段键值 := 字段元素.(*ast.KeyValueExpr)
						if !是字段键值 {
							continue
						}
						字段标识, 是字段标识 := 字段键值.Key.(*ast.Ident)
						if !是字段标识 {
							continue
						}

						switch 字段标识.Name {
						case "Name":
							if 基础字面量, 确定 := 字段键值.Value.(*ast.BasicLit); 确定 {
								条目.Name = strings.Trim(基础字面量.Value, "\"")
							}
						case "Operand":
							if 操作数标识, 确定 := 字段键值.Value.(*ast.Ident); 确定 {
								条目.Operand = 操作数标识.Name
							}
						case "Pops":
							if 基础字面量, 确定 := 字段键值.Value.(*ast.BasicLit); 确定 {
								数值, _ := strconv.Atoi(基础字面量.Value)
								条目.Pops = uint8(数值)
							}
						case "Pushes":
							if 基础字面量, 确定 := 字段键值.Value.(*ast.BasicLit); 确定 {
								数值, _ := strconv.Atoi(基础字面量.Value)
								条目.Pushes = uint8(数值)
							}
						case "PurePush":
							if 布尔标识, 确定 := 字段键值.Value.(*ast.Ident); 确定 {
								条目.PurePush = (布尔标识.Name == "true")
							}
						case "IsJump":
							if 布尔标识, 确定 := 字段键值.Value.(*ast.Ident); 确定 {
								条目.IsJump = (布尔标识.Name == "true")
							}
						case "IsTerminal":
							if 布尔标识, 确定 := 字段键值.Value.(*ast.Ident); 确定 {
								条目.IsTerminal = (布尔标识.Name == "true")
							}
						case "StackCond":
							if 布尔标识, 确定 := 字段键值.Value.(*ast.Ident); 确定 {
								条目.StackCond = (布尔标识.Name == "true")
							}
						case "VarStack":
							if 布尔标识, 确定 := 字段键值.Value.(*ast.Ident); 确定 {
								条目.VarStack = (布尔标识.Name == "true")
							}
						}
					}

					元数据映射[键标识.Name] = 条目
				}
			}
		}
	}

	fmt.Printf("成功从 meta.go 解析出 %d 个 opMeta 注册条目\n", len(元数据映射))

	// 3. 组合 106 条事实数据
	var 条目列表 []指令事实条目
	类别计数 := make(map[string]int)

	for _, 标识名称 := range 操作码标识列表 {
		数值 := 标识到数值[标识名称]
		元数据, 存在 := 元数据映射[标识名称]
		if !存在 {
			fmt.Fprintf(os.Stderr, "错误: 指令 %s (%d) 未在 meta.go 中登记元数据！\n", 标识名称, 数值)
			os.Exit(1)
		}

		if 元数据.Name == "" {
			fmt.Fprintf(os.Stderr, "错误: 指令 %s (%d) 的 Name 为空！\n", 标识名称, 数值)
			os.Exit(1)
		}

		操作数说明, 说明存在 := 操作数类别名称[元数据.Operand]
		if !说明存在 {
			fmt.Fprintf(os.Stderr, "错误: 未知的操作数类别 %s\n", 元数据.Operand)
			os.Exit(1)
		}

		操作数数值 := 操作数类别数值[元数据.Operand]
		含操作数 := (元数据.Operand != "OperandNone")
		类别计数[元数据.Operand]++

		// 计算净栈变动
		栈变动表达 := ""
		if 元数据.StackCond || 元数据.VarStack {
			栈变动表达 = "动态"
		} else {
			净变动 := int(元数据.Pushes) - int(元数据.Pops)
			栈变动表达 = fmt.Sprintf("%+d", 净变动)
		}

		// 特殊说明
		特殊说明 := ""
		switch 标识名称 {
		case "OpForInNext":
			特殊说明 = "遗留指令；VM中无对应dispatch分支"
		case "OpEnd":
			特殊说明 = "代码结尾哨兵指令；不实际执行"
		case "OpGetPropLocal":
			特殊说明 = "超指令：打包局部槽位与属性名(slot<<16 | nameIdx)"
		case "OpCallMethod":
			特殊说明 = "打包调用：参数数量与方法名(numArgs<<16 | nameIdx)"
		case "OpPushInt":
			特殊说明 = "立即数：24位无符号小整数(0..16777215)"
		case "OpPushNegInt":
			特殊说明 = "立即数：24位负整数，压入 -(A)"
		case "OpJmp", "OpJmpTrue", "OpJmpFalse",
			"OpJmpTrueKeep", "OpJmpFalseKeep",
			"OpJmpNullishKeep", "OpJmpNotNullishKeep":
			特殊说明 = "相对跳转；跳转目标允许 target == len(code)"
		case "OpCloseUpvalues":
			特殊说明 = "闭包作用域：关闭从指定槽位开始的捕获上值"
		case "OpMakeRegexp":
			特殊说明 = "弹栈flags+pattern，生成RegExp对象"
		case "OpEnumKeys":
			特殊说明 = "弹栈对象，压入原型链可枚举键数组"
		}

		条目 := 指令事实条目{
			Opcode:       数值,
			Ident:        标识名称,
			Name:         元数据.Name,
			OperandKindId: 操作数数值,
			OperandKind:  元数据.Operand,
			OperandDesc:  操作数说明,
			HasOperand:   含操作数,
			Pops:         元数据.Pops,
			Pushes:       元数据.Pushes,
			StackEffect:  栈变动表达,
			PurePush:     元数据.PurePush,
			IsJump:       元数据.IsJump,
			IsTerminal:   元数据.IsTerminal,
			StackCond:    元数据.StackCond,
			VarStack:     元数据.VarStack,
			SpecialNotes: 特殊说明,
		}

		条目列表 = append(条目列表, 条目)
	}

	// 验证 11 种操作数全部被使用
	for 类别名, 说明 := range 操作数类别名称 {
		if 类别计数[类别名] == 0 {
			fmt.Fprintf(os.Stderr, "错误: 操作数类别 %s 没有任何指令使用！\n", 说明)
			os.Exit(1)
		}
	}

	// 4. 创建证据产物目录
	产物目录 := filepath.Join(根目录, ".work", "evidence", "20260904")
	if err := os.MkdirAll(产物目录, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "创建目录失败: %v\n", err)
		os.Exit(1)
	}

	// 5. 写入 TSV
	tsv路径 := filepath.Join(产物目录, "isa-facts.tsv")
	tsv文件, err := os.Create(tsv路径)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 TSV 文件失败: %v\n", err)
		os.Exit(1)
	}
	defer tsv文件.Close()

	fmt.Fprintf(tsv文件, "操作码数值\t常量标识符\t指令名称\t操作数类别数值\t操作数类别标识\t操作数说明\t含操作数\t出栈\t入栈\t净栈变动\t纯压栈\t跳转\t终结\t条件栈\t动态栈\t特殊说明\n")
	for _, 条目 := range 条目列表 {
		fmt.Fprintf(tsv文件, "%d\t%s\t%s\t%d\t%s\t%s\t%t\t%d\t%d\t%s\t%t\t%t\t%t\t%t\t%t\t%s\n",
			条目.Opcode,
			条目.Ident,
			条目.Name,
			条目.OperandKindId,
			条目.OperandKind,
			条目.OperandDesc,
			条目.HasOperand,
			条目.Pops,
			条目.Pushes,
			条目.StackEffect,
			条目.PurePush,
			条目.IsJump,
			条目.IsTerminal,
			条目.StackCond,
			条目.VarStack,
			条目.SpecialNotes,
		)
	}

	// 6. 写入 JSON
	json路径 := filepath.Join(产物目录, "isa-facts.json")
	json数据, err := json.MarshalIndent(条目列表, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化 JSON 失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(json路径, json数据, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "写入 JSON 文件失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n[验证成功] 106 条 ISA 事实表已成功导出：\n")
	fmt.Printf(" - 指令条数: %d 行（连续 0..105）\n", len(条目列表))
	fmt.Printf(" - TSV 产物: %s\n", tsv路径)
	fmt.Printf(" - JSON 产物: %s\n", json路径)
	fmt.Printf(" - 11 种操作数类别分布:\n")
	for _, 类别名 := range []string{
		"OperandNone",
		"OperandConstIdx",
		"OperandInt",
		"OperandSlot",
		"OperandUpvalueIdx",
		"OperandTemplateIdx",
		"OperandTryIdx",
		"OperandSignedOff",
		"OperandCount",
		"OperandPackedSlotName",
		"OperandPackedCall",
	} {
		fmt.Printf("   * %-25s (%s) : %d 条指令\n", 类别名, 操作数类别名称[类别名], 类别计数[类别名])
	}
}
