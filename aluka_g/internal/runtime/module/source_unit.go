package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// SourceKind 描述源码语言层；它与 ESM/CJS 模块语义相互独立。
type SourceKind uint8

const (
	SourceJavaScript SourceKind = iota
	SourceTypeScript
	SourceJSON
)

func (k SourceKind) String() string {
	switch k {
	case SourceTypeScript:
		return "typescript"
	case SourceJSON:
		return "json"
	default:
		return "javascript"
	}
}

// ModuleKind 描述模块封装与执行协议。
type ModuleKind uint8

const (
	ModuleScript ModuleKind = iota
	ModuleESM
	ModuleCommonJS
)

func (k ModuleKind) String() string {
	switch k {
	case ModuleESM:
		return "esm"
	case ModuleCommonJS:
		return "cjs"
	default:
		return "script"
	}
}

// TransformStage 记录 SourceUnit 已完成的单向变换阶段。
type TransformStage uint16

const (
	StageParsed TransformStage = 1 << iota
	StageTypeStripped
	StageShaken
	StageMinified
	StageESMLowered
	StageWrapped
	StageBytecodeOptimized
)

// MarkStage 把 stage 标记到单元上（只增不减）。若该阶段已存在，返回诊断，
// 防止 pass 乱序/重复执行破坏单向阶段流（P2-5）。
func (u *SourceUnit) MarkStage(stage TransformStage) error {
	if u == nil {
		return fmt.Errorf("module: cannot mark stage on nil source unit")
	}
	if u.Stage&stage != 0 {
		return fmt.Errorf("module: %q: transform stage %d already applied (current %d)", u.Path, stage, u.Stage)
	}
	u.Stage |= stage
	return nil
}

// RequireStages 校验单元已完成全部给定阶段；缺失时返回诊断（P2-5）。
func (u *SourceUnit) RequireStages(stages TransformStage) error {
	if u == nil {
		return fmt.Errorf("module: nil source unit")
	}
	if missing := stages &^ u.Stage; missing != 0 {
		return fmt.Errorf("module: %q: missing required transform stages %d (current %d)", u.Path, missing, u.Stage)
	}
	return nil
}

// SourceUnit 是 JS/TS 前端与模块/优化后端之间的稳定中间表示。
// Program 在一条构建管线内由单一所有者按阶段原地变换，禁止从 Source 重新解析。
type SourceUnit struct {
	Path       string
	Source     []byte
	SourceKind SourceKind
	ModuleKind ModuleKind
	Program    *ast.Program
	HasTLA     bool
	Stage      TransformStage
}

// DetectSourceKind 仅按规范化扩展名识别语言层。
func DetectSourceKind(path string) SourceKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts", ".tsx":
		return SourceTypeScript
	case ".json":
		return SourceJSON
	default:
		return SourceJavaScript
	}
}

// ParseSourceUnit 执行一次源码前端处理：TS policy 校验与 JS AST 解析。
func ParseSourceUnit(src []byte, path string, kind ModuleKind) (*SourceUnit, error) {
	src = stripSourceBOM(src)
	sourceKind := DetectSourceKind(path)
	if sourceKind == SourceJSON {
		return &SourceUnit{Path: path, Source: src, SourceKind: sourceKind, ModuleKind: kind}, nil
	}
	if err := checkUnsupportedTS(string(src), path); err != nil {
		return nil, err
	}
	prog, err := parser.ParseModule(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse error in %q: %w", path, err)
	}
	prog = ast.LowerJSX(prog).(*ast.Program)
	stage := StageParsed
	if sourceKind == SourceTypeScript {
		// 当前 parser 在建 AST 时执行 strip-only；显式记录该事实，后续可替换为
		// 独立 tsstrip pass，而不让模块层重新推断。
		stage |= StageTypeStripped
	}
	return &SourceUnit{
		Path:       path,
		Source:     src,
		SourceKind: sourceKind,
		ModuleKind: kind,
		Program:    prog,
		HasTLA:     ast.HasTopLevelAwait(prog),
		Stage:      stage,
	}, nil
}

// ParseFileUnit 读取并解析一个源码文件（P3-1：runtime loader 与 bundler
// 共用的统一前端入口）。path 为文件系统路径，key 为模块标识（虚拟路径）。
func ParseFileUnit(path, key string) (*SourceUnit, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("module: cannot resolve path %q: %w", path, err)
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("module: cannot read %q: %w", absPath, err)
	}
	return ParseFileSource(src, key, absPath)
}

// ParseFileSource 解析已读取的源码并完成扩展名优先分类 + 隐式 .js/.ts 提升。
func ParseFileSource(src []byte, key, fsPath string) (*SourceUnit, error) {
	resolver := NewResolver()
	moduleKind := resolver.SourceModuleKind(fsPath)
	unit, err := ParseSourceUnit(src, key, moduleKind)
	if err != nil {
		return nil, err
	}
	// 隐式 .js/.ts 延续 package-type 兼容：仅在这里做一次语法提升。
	// 顶层 await（TLA）即使无 import/export 也必须是 ESM（Node 语义）。
	ext := strings.ToLower(filepath.Ext(fsPath))
	if (ext == ".ts" || ext == ".js" || ext == ".tsx" || ext == ".jsx") && moduleKind == ModuleCommonJS &&
		(HasESMDecls(unit.Program) || ast.HasTopLevelAwait(unit.Program)) {
		unit.ModuleKind = ModuleESM
	}
	return unit, nil
}

func stripSourceBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}
