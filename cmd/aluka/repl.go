package main

// aluka repl — 交互式读取-求值-打印循环（Phase 1D.15）。
//
// REPL 在单一引擎上下文中持续执行用户输入：
//   - 变量/函数声明通过累积重放保持状态（每次 Eval 全部历史 + 新输入）
//   - 表达式结果自动打印（非 undefined/null 时）
//   - 多行输入：检测未闭合的括号/引号/模板字符串，继续读取下一行
//   - 语法错误不终止会话，打印错误后继续
//   - .exit 或 EOF(Ctrl+D) 退出

import (
	"bufio"
	"path/filepath"
	"fmt"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

const replPrompt = "> "
const replContPrompt = ". "

// startREPL 启动交互式 REPL 会话。
func startREPL(vm bool) {
	var eng engine.Engine
	if vm {
		eng = interpreter.NewVMEngine()
	} else {
		eng = interpreter.NewEngine()
	}
	defer eng.Shutdown()

	ctx, err := eng.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aluka: cannot create context:", err)
		os.Exit(1)
	}
	defer ctx.Close()

	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		fmt.Fprintln(os.Stderr, "aluka: cannot register console:", err)
		os.Exit(1)
	}
	if err := globals.NewProcess(ctx, globals.ProcessConfig{}); err != nil {
		fmt.Fprintln(os.Stderr, "aluka: cannot register process:", err)
		os.Exit(1)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	_ = ctx.Global().Set("global", ctx.Global())

	fmt.Printf("aluka %s REPL (pure Go)\n", version)
	fmt.Println("Type .help for help, .exit to quit.")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB 缓冲

	// sessionHistory 累积全部已执行的会话代码（用于 var/function 状态保持）。
	// 每次新输入完整后，Eval(sessionHistory + 新输入)，使声明跨输入持久。
	// 副作用会重复执行——这是累积重放方案的已知限制，对 REPL 场景可接受。
	var sessionHistory strings.Builder
	// pendingInput 累积当前未完成的多行输入。
	var pendingInput strings.Builder
	historyLines := 0 // sessionHistory 中的行数（用于分隔）
	// editorMode：.editor 模式（空行结束并执行）。
	editorMode := false
	// replHistory：历史记录（成功执行的输入，跨会话持久化）。
	var replHistory []string
	historyPath := replHistoryPath()
	if data, err := os.ReadFile(historyPath); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			if ln != "" {
				replHistory = append(replHistory, ln)
			}
		}
	}
	// 退出时写回历史文件。
	defer func() {
		_ = os.MkdirAll(filepath.Dir(historyPath), 0755)
		f, err := os.Create(historyPath)
		if err == nil {
			defer f.Close()
			for _, h := range replHistory {
				_, _ = f.WriteString(h + "\n")
			}
		}
	}()

	for {
		// .editor 请求（点命令置位）→ 进入编辑器模式。
		if replEditorRequested {
			replEditorRequested = false
			editorMode = true
			continue
		}

		// 选择提示符
		prompt := replPrompt
		if pendingInput.Len() > 0 {
			prompt = replContPrompt
		}
		fmt.Print(prompt)

		if !scanner.Scan() {
			// EOF (Ctrl+D)
			fmt.Println()
			break
		}
		line := scanner.Text()

		// .editor 模式：空行结束并执行累积内容。
		if editorMode {
			if line == "" {
				editorMode = false
				line = pendingInput.String()
				pendingInput.Reset()
				if strings.TrimSpace(line) == "" {
					continue
				}
			} else {
				if pendingInput.Len() > 0 {
					pendingInput.WriteString("\n")
				}
				pendingInput.WriteString(line)
				continue
			}
		}

		// 处理点命令（仅在无待完成输入时）
		if pendingInput.Len() == 0 && strings.HasPrefix(line, ".") {
			if handleDotCommand(line, ctx) {
				return
			}
			continue
		}

		// 累积到 pendingInput
		if pendingInput.Len() > 0 {
			pendingInput.WriteString("\n")
		}
		pendingInput.WriteString(line)

		// 检查输入是否完整（括号/引号/模板是否平衡）
		if !isInputComplete(pendingInput.String()) {
			continue // 继续读取下一行
		}

		// 输入完整。构造完整代码 = 历史代码 + 新输入。
		newInput := pendingInput.String()
		pendingInput.Reset()

		var fullCode string
		if sessionHistory.Len() > 0 {
			fullCode = sessionHistory.String() + "\n" + newInput
		} else {
			fullCode = newInput
		}

		result, err := ctx.Eval(fullCode, "[repl]")
		if err != nil {
			// 错误不终止 REPL。但成功的声明仍累积（容错：即使报错也记录，
			// 因为部分代码可能已执行，如 var 声明在错误前的行）。
			fmt.Fprintln(os.Stderr, err)
			// 不累积出错的输入，避免错误累积放大。
			continue
		}

		// 成功执行：累积新输入到会话历史（用于后续 var/function 状态保持）。
		if sessionHistory.Len() > 0 {
			sessionHistory.WriteString("\n")
		}
		sessionHistory.WriteString(newInput)
		historyLines++

		// 历史记录（跨会话持久化）。
		replHistory = append(replHistory, newInput)

		// 打印非空结果
		printREPLResult(result)
	}
}

// handleDotCommand 处理 REPL 点命令（.help/.exit/.editor/.version 等）。
// 返回 true 表示应退出 REPL（仅 .exit）。
func handleDotCommand(line string, ctx engine.Context) bool {
	cmd := strings.TrimSpace(line)
	switch {
	case cmd == ".exit" || cmd == ".quit":
		return true
	case cmd == ".help":
		fmt.Println(`REPL commands:
  .help     Show this help
  .exit     Exit REPL (or Ctrl+D)
  .editor   Enter editor mode (empty line executes)
  .version  Print aluka version`)
	case cmd == ".editor":
		// 进入编辑器模式：由主循环处理（空行结束并执行）。
		fmt.Println("Entering editor mode (Ctrl+D to finish, empty line to execute)")
		// 用全局标志通知主循环。
		replEditorRequested = true
	case cmd == ".version":
		fmt.Println("aluka " + version)
	default:
		fmt.Fprintln(os.Stderr, "aluka: unknown REPL command:", cmd, "(try .help)")
	}
	return false
}

// replEditorRequested 由 .editor 命令置位，主循环下一轮进入编辑器模式。
var replEditorRequested bool

// replHistoryPath 历史文件路径（跨会话持久化，Node 风格 ~/.aluka_repl_history）。
func replHistoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aluka_repl_history"
	}
	return filepath.Join(home, ".aluka_repl_history")
}

// printREPLResult 按格式打印求值结果。
func printREPLResult(v engine.Value) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return
	}
	fmt.Println(v.String())
}

// isInputComplete 检查输入是否语法完整（括号/引号/模板平衡）。
// 用于 REPL 的多行输入：若不完整则继续读取下一行。
func isInputComplete(s string) bool {
	parens, braces, brackets := 0, 0, 0
	inString := false
	stringQuote := byte(0)
	inTemplate := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(s) && s[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inString {
			if ch == '\\' && i+1 < len(s) {
				i++ // 跳过转义字符
				continue
			}
			if ch == stringQuote {
				inString = false
			}
			continue
		}
		if inTemplate {
			if ch == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if ch == '`' {
				inTemplate = false
			}
			continue
		}

		// 不在任何字符串/注释内
		if ch == '/' && i+1 < len(s) {
			if s[i+1] == '/' {
				inLineComment = true
				i++
				continue
			}
			if s[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}
		if ch == '"' || ch == '\'' {
			inString = true
			stringQuote = ch
			continue
		}
		if ch == '`' {
			inTemplate = true
			continue
		}
		switch ch {
		case '(':
			parens++
		case ')':
			parens--
		case '{':
			braces++
		case '}':
			braces--
		case '[':
			brackets++
		case ']':
			brackets--
		}
	}

	// 平衡且不在字符串/模板/注释内
	return parens <= 0 && braces <= 0 && brackets <= 0 && !inString && !inTemplate
}
