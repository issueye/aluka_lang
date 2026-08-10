// Package analyze measures compiled bundle contents and produces deterministic
// human- and machine-readable reports.
package analyze

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

const SchemaVersion = 1

type Options struct {
	TreeShake        bool  `json:"treeShake"`
	Minify           bool  `json:"minify"`
	BytecodeOptimize bool  `json:"bytecodeOptimize"`
	MaxPayloadBytes  int64 `json:"maxPayloadBytes,omitempty"`
}

type ModuleMeasurement struct {
	Path       string
	ModuleType string
	Bytes      int64
}

type StageMeasurement struct {
	ModuleCount int                          `json:"moduleCount"`
	ModuleBytes int64                        `json:"moduleBytes"`
	Modules     map[string]ModuleMeasurement `json:"-"`
}

type Stages struct {
	Raw               StageMeasurement `json:"raw"`
	Shaken            StageMeasurement `json:"shaken"`
	Minified          StageMeasurement `json:"minified"`
	BytecodeOptimized StageMeasurement `json:"bytecodeOptimized"`
}

type Sizes struct {
	BaseBytes            int64 `json:"baseBytes"`
	PayloadBytes         int64 `json:"payloadBytes"`
	ArtifactBytes        int64 `json:"artifactBytes"`
	AssetBytes           int64 `json:"assetBytes"`
	PayloadOverheadBytes int64 `json:"payloadOverheadBytes"`
}

type ModuleReport struct {
	Path               string  `json:"path"`
	ModuleType         string  `json:"moduleType"`
	SourceBytes        int64   `json:"sourceBytes"`
	RawBytecodeBytes   int64   `json:"rawBytecodeBytes"`
	FinalBytecodeBytes int64   `json:"finalBytecodeBytes"`
	SavedBytes         int64   `json:"savedBytes"`
	PayloadShare       float64 `json:"payloadShare"`
	Dependencies       int     `json:"dependencies"`
	Dependents         int     `json:"dependents"`
	Entry              bool    `json:"isEntry"`
	ThirdParty         bool    `json:"thirdParty"`
}

type RemovedModuleReport struct {
	Path             string `json:"path"`
	ModuleType       string `json:"moduleType"`
	SourceBytes      int64  `json:"sourceBytes"`
	RawBytecodeBytes int64  `json:"rawBytecodeBytes"`
}

type AssetReport struct {
	Path         string  `json:"path"`
	Bytes        int64   `json:"bytes"`
	PayloadShare float64 `json:"payloadShare"`
	Dependents   int     `json:"dependents"`
}

type Finding struct {
	ID             string  `json:"id"`
	Severity       string  `json:"severity"`
	Path           string  `json:"path,omitempty"`
	Message        string  `json:"message"`
	Suggestion     string  `json:"suggestion"`
	ActualBytes    int64   `json:"actualBytes,omitempty"`
	ThresholdBytes int64   `json:"thresholdBytes,omitempty"`
	ActualRatio    float64 `json:"actualRatio,omitempty"`
	ThresholdRatio float64 `json:"thresholdRatio,omitempty"`
}

type BytecodeStats struct {
	InstructionsBefore  int `json:"instructionsBefore"`
	InstructionsAfter   int `json:"instructionsAfter"`
	RemovedInstructions int `json:"removedInstructions"`
	FusedInstructions   int `json:"fusedInstructions"`
	ThreadedJumps       int `json:"threadedJumps"`
}

type Report struct {
	Entry          string                `json:"entry"`
	Output         string                `json:"output,omitempty"`
	Options        Options               `json:"options"`
	Sizes          Sizes                 `json:"sizes"`
	Stages         Stages                `json:"stages"`
	Bytecode       BytecodeStats         `json:"bytecode"`
	Modules        []ModuleReport        `json:"modules"`
	RemovedModules []RemovedModuleReport `json:"removedModules"`
	Assets         []AssetReport         `json:"assets"`
	Findings       []Finding             `json:"findings"`
}

type Document struct {
	SchemaVersion int       `json:"schemaVersion"`
	Reports       []*Report `json:"reports"`
}

type Input struct {
	Entry             string
	Output            string
	RootDir           string
	Resolutions       map[string]map[string]string
	UnresolvedDynamic []string
	Assets            map[string][]byte
	Raw               StageMeasurement
	Shaken            StageMeasurement
	Minified          StageMeasurement
	BytecodeOptimized StageMeasurement
	PayloadBytes      int64
	BaseBytes         int64
	ArtifactBytes     int64
	Options           Options
	BytecodeStats     bytecode.OptimizationStats
}

// MeasureStage serializes each module exactly as Pack does and records the
// resulting byte size. The module byte slices are not retained.
func MeasureStage(modules []*compile.EntryData) (StageMeasurement, error) {
	stage := StageMeasurement{
		ModuleCount: len(modules),
		Modules:     make(map[string]ModuleMeasurement, len(modules)),
	}
	var buf bytes.Buffer
	for _, mod := range modules {
		if mod == nil || mod.Module == nil {
			return stage, fmt.Errorf("bundle analyze: nil module")
		}
		buf.Reset()
		if err := bytecode.Serialize(&buf, mod.Module); err != nil {
			return stage, fmt.Errorf("bundle analyze: serialize %q: %w", mod.Path, err)
		}
		size := int64(buf.Len())
		stage.ModuleBytes += size
		stage.Modules[mod.Path] = ModuleMeasurement{
			Path:       mod.Path,
			ModuleType: mod.ModuleType,
			Bytes:      size,
		}
	}
	return stage, nil
}

func sortedModuleMeasurements(stage StageMeasurement) []ModuleMeasurement {
	items := make([]ModuleMeasurement, 0, len(stage.Modules))
	for _, item := range stage.Modules {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items
}
