package analyze

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	hotspotMinBytes = int64(64 * 1024)
	hotspotRatio    = 0.20
	cjsRatio        = 0.10
	thirdPartyRatio = 0.60
	lowGainRatio    = 0.03
)

func BuildReport(input Input) (*Report, error) {
	report := &Report{
		Entry:          input.Entry,
		Output:         displayOutput(input.Output),
		Options:        input.Options,
		Modules:        make([]ModuleReport, 0),
		RemovedModules: make([]RemovedModuleReport, 0),
		Assets:         make([]AssetReport, 0),
		Findings:       make([]Finding, 0),
		Stages: Stages{
			Raw:               input.Raw,
			Shaken:            input.Shaken,
			Minified:          input.Minified,
			BytecodeOptimized: input.BytecodeOptimized,
		},
		Bytecode: BytecodeStats{
			InstructionsBefore:  input.BytecodeStats.InstructionsBefore,
			InstructionsAfter:   input.BytecodeStats.InstructionsAfter,
			RemovedInstructions: input.BytecodeStats.RemovedInstructions,
			FusedInstructions:   input.BytecodeStats.FusedInstructions,
			ThreadedJumps:       input.BytecodeStats.ThreadedJumps,
		},
	}

	assetBytes := int64(0)
	for _, data := range input.Assets {
		assetBytes += int64(len(data))
	}
	report.Sizes = Sizes{
		BaseBytes:            input.BaseBytes,
		PayloadBytes:         input.PayloadBytes,
		ArtifactBytes:        input.ArtifactBytes,
		AssetBytes:           assetBytes,
		PayloadOverheadBytes: input.PayloadBytes - input.BytecodeOptimized.ModuleBytes - assetBytes,
	}

	finalSet := make(map[string]bool, len(input.BytecodeOptimized.Modules))
	for path := range input.BytecodeOptimized.Modules {
		finalSet[path] = true
	}
	dependencies := make(map[string]int)
	dependents := make(map[string]int)
	for parent, table := range input.Resolutions {
		if !finalSet[parent] {
			continue
		}
		seen := make(map[string]bool)
		for _, target := range table {
			if seen[target] {
				continue
			}
			seen[target] = true
			if finalSet[target] {
				dependencies[parent]++
				dependents[target]++
			}
		}
	}

	for _, final := range sortedModuleMeasurements(input.BytecodeOptimized) {
		raw := input.Raw.Modules[final.Path]
		sourceBytes, err := sourceSize(input.RootDir, final.Path)
		if err != nil {
			return nil, err
		}
		report.Modules = append(report.Modules, ModuleReport{
			Path:               final.Path,
			ModuleType:         final.ModuleType,
			SourceBytes:        sourceBytes,
			RawBytecodeBytes:   raw.Bytes,
			FinalBytecodeBytes: final.Bytes,
			SavedBytes:         raw.Bytes - final.Bytes,
			PayloadShare:       ratio(final.Bytes, input.PayloadBytes),
			Dependencies:       dependencies[final.Path],
			Dependents:         dependents[final.Path],
			Entry:              final.Path == input.Entry,
			ThirdParty:         isThirdParty(final.Path),
		})
	}
	sort.Slice(report.Modules, func(i, j int) bool {
		if report.Modules[i].FinalBytecodeBytes == report.Modules[j].FinalBytecodeBytes {
			return report.Modules[i].Path < report.Modules[j].Path
		}
		return report.Modules[i].FinalBytecodeBytes > report.Modules[j].FinalBytecodeBytes
	})

	for _, raw := range sortedModuleMeasurements(input.Raw) {
		if finalSet[raw.Path] {
			continue
		}
		sourceBytes, err := sourceSize(input.RootDir, raw.Path)
		if err != nil {
			return nil, err
		}
		report.RemovedModules = append(report.RemovedModules, RemovedModuleReport{
			Path:             raw.Path,
			ModuleType:       raw.ModuleType,
			SourceBytes:      sourceBytes,
			RawBytecodeBytes: raw.Bytes,
		})
	}
	sort.Slice(report.RemovedModules, func(i, j int) bool {
		if report.RemovedModules[i].RawBytecodeBytes == report.RemovedModules[j].RawBytecodeBytes {
			return report.RemovedModules[i].Path < report.RemovedModules[j].Path
		}
		return report.RemovedModules[i].RawBytecodeBytes > report.RemovedModules[j].RawBytecodeBytes
	})

	for path, data := range input.Assets {
		refs := 0
		for parent, table := range input.Resolutions {
			if !finalSet[parent] {
				continue
			}
			seen := false
			for _, target := range table {
				if target == path {
					seen = true
					break
				}
			}
			if seen {
				refs++
			}
		}
		size := int64(len(data))
		report.Assets = append(report.Assets, AssetReport{
			Path:         path,
			Bytes:        size,
			PayloadShare: ratio(size, input.PayloadBytes),
			Dependents:   refs,
		})
	}
	sort.Slice(report.Assets, func(i, j int) bool {
		if report.Assets[i].Bytes == report.Assets[j].Bytes {
			return report.Assets[i].Path < report.Assets[j].Path
		}
		return report.Assets[i].Bytes > report.Assets[j].Bytes
	})

	report.Findings = buildFindings(report, input.UnresolvedDynamic)
	return report, nil
}

func displayOutput(path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Base(path))
	}
	return filepath.ToSlash(path)
}

func sourceSize(rootDir, virtualPath string) (int64, error) {
	info, err := os.Stat(filepath.Join(rootDir, filepath.FromSlash(virtualPath)))
	if err != nil {
		return 0, fmt.Errorf("bundle analyze: stat source %q: %w", virtualPath, err)
	}
	return info.Size(), nil
}

func ratio(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func isThirdParty(path string) bool {
	p := "/" + strings.TrimPrefix(filepath.ToSlash(path), "./") + "/"
	return strings.Contains(p, "/node_modules/")
}

func buildFindings(report *Report, unresolved []string) []Finding {
	findings := make([]Finding, 0)
	for _, path := range uniqueSorted(unresolved) {
		findings = append(findings, Finding{
			ID:         "DYNAMIC_IMPORT_UNRESOLVED",
			Severity:   "error",
			Path:       path,
			Message:    "Dynamic import uses a specifier that cannot be resolved at build time.",
			Suggestion: "Use a string literal, a foldable constant, or an explicit specifier map.",
		})
	}
	if !report.Options.TreeShake {
		findings = append(findings, Finding{
			ID:         "TREE_SHAKE_DISABLED",
			Severity:   "warning",
			Message:    "Tree-shaking is disabled for this build.",
			Suggestion: "Enable --tree-shake or use --optimize.",
		})
	}
	for _, mod := range report.Modules {
		if !report.Options.Minify && mod.FinalBytecodeBytes >= hotspotMinBytes {
			findings = append(findings, Finding{
				ID:             "MINIFY_DISABLED",
				Severity:       "info",
				Path:           mod.Path,
				Message:        "A large module was built without AST minification.",
				Suggestion:     "Enable --minify or use --optimize, then compare the stage report.",
				ActualBytes:    mod.FinalBytecodeBytes,
				ThresholdBytes: hotspotMinBytes,
			})
		}
		if !report.Options.BytecodeOptimize && mod.FinalBytecodeBytes >= hotspotMinBytes {
			findings = append(findings, Finding{
				ID:             "BYTECODE_OPT_DISABLED",
				Severity:       "info",
				Path:           mod.Path,
				Message:        "A large module was built without bytecode optimization.",
				Suggestion:     "Enable --bytecode-opt or use --optimize.",
				ActualBytes:    mod.FinalBytecodeBytes,
				ThresholdBytes: hotspotMinBytes,
			})
		}
		if mod.ModuleType == compileModuleTypeCJS && mod.FinalBytecodeBytes >= hotspotMinBytes && mod.PayloadShare >= cjsRatio {
			findings = append(findings, Finding{
				ID:             "CJS_OPTIMIZATION_LIMITED",
				Severity:       "warning",
				Path:           mod.Path,
				Message:        "A large CommonJS module must be retained conservatively.",
				Suggestion:     "Prefer an ESM or subpath entry, or isolate the dependency behind a smaller module.",
				ActualBytes:    mod.FinalBytecodeBytes,
				ThresholdBytes: hotspotMinBytes,
				ActualRatio:    mod.PayloadShare,
				ThresholdRatio: cjsRatio,
			})
		}
		if mod.FinalBytecodeBytes >= hotspotMinBytes && mod.PayloadShare >= hotspotRatio {
			findings = append(findings, Finding{
				ID:             "MODULE_HOTSPOT",
				Severity:       "warning",
				Path:           mod.Path,
				Message:        "A single module is a major payload contributor.",
				Suggestion:     "Inspect large constants, generated data, broad imports, and opportunities to split responsibilities.",
				ActualBytes:    mod.FinalBytecodeBytes,
				ThresholdBytes: hotspotMinBytes,
				ActualRatio:    mod.PayloadShare,
				ThresholdRatio: hotspotRatio,
			})
		}
	}
	for _, asset := range report.Assets {
		if asset.Bytes >= hotspotMinBytes && asset.PayloadShare >= hotspotRatio {
			findings = append(findings, Finding{
				ID:             "ASSET_HOTSPOT",
				Severity:       "warning",
				Path:           asset.Path,
				Message:        "A single embedded asset is a major payload contributor.",
				Suggestion:     "Compress, externalize, or load the asset on demand.",
				ActualBytes:    asset.Bytes,
				ThresholdBytes: hotspotMinBytes,
				ActualRatio:    asset.PayloadShare,
				ThresholdRatio: hotspotRatio,
			})
		}
	}
	thirdPartyBytes := int64(0)
	for _, mod := range report.Modules {
		if mod.ThirdParty {
			thirdPartyBytes += mod.FinalBytecodeBytes
		}
	}
	thirdPartyShare := ratio(thirdPartyBytes, report.Sizes.PayloadBytes)
	if thirdPartyShare >= thirdPartyRatio {
		findings = append(findings, Finding{
			ID:             "THIRD_PARTY_DOMINANT",
			Severity:       "info",
			Message:        "Third-party modules dominate the payload.",
			Suggestion:     "Review heavy dependencies and prefer narrower subpath imports where available.",
			ActualBytes:    thirdPartyBytes,
			ActualRatio:    thirdPartyShare,
			ThresholdRatio: thirdPartyRatio,
		})
	}
	if report.Options.Minify && report.Stages.Shaken.ModuleBytes > 0 {
		gain := ratio(report.Stages.Shaken.ModuleBytes-report.Stages.Minified.ModuleBytes, report.Stages.Shaken.ModuleBytes)
		if gain >= 0 && gain < lowGainRatio {
			findings = append(findings, Finding{
				ID:             "LOW_MINIFY_GAIN",
				Severity:       "info",
				Message:        "AST minification reduced module bytecode by less than 3%.",
				Suggestion:     "Prioritize assets, CommonJS boundaries, and large constant pools instead.",
				ActualRatio:    gain,
				ThresholdRatio: lowGainRatio,
			})
		}
	}
	if report.Options.BytecodeOptimize && report.Stages.Minified.ModuleBytes > 0 {
		gain := ratio(report.Stages.Minified.ModuleBytes-report.Stages.BytecodeOptimized.ModuleBytes, report.Stages.Minified.ModuleBytes)
		if gain >= 0 && gain < lowGainRatio {
			findings = append(findings, Finding{
				ID:             "LOW_BYTECODE_OPT_GAIN",
				Severity:       "info",
				Message:        "Bytecode optimization reduced module bytecode by less than 3%.",
				Suggestion:     "Use runtime profiling before adding more aggressive bytecode transforms.",
				ActualRatio:    gain,
				ThresholdRatio: lowGainRatio,
			})
		}
	}
	if report.Options.MaxPayloadBytes > 0 && report.Sizes.PayloadBytes > report.Options.MaxPayloadBytes {
		findings = append(findings, Finding{
			ID:             "PAYLOAD_BUDGET_EXCEEDED",
			Severity:       "error",
			Message:        "The compiled payload exceeds its configured size budget.",
			Suggestion:     "Reduce the largest reported modules or raise --max-payload deliberately.",
			ActualBytes:    report.Sizes.PayloadBytes,
			ThresholdBytes: report.Options.MaxPayloadBytes,
		})
	}
	sort.SliceStable(findings, func(i, j int) bool {
		si, sj := severityRank(findings[i].Severity), severityRank(findings[j].Severity)
		if si != sj {
			return si < sj
		}
		if findings[i].ID != findings[j].ID {
			return findings[i].ID < findings[j].ID
		}
		return findings[i].Path < findings[j].Path
	})
	return findings
}

const compileModuleTypeCJS = "cjs"

func severityRank(s string) int {
	switch s {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func uniqueSorted(items []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}
