package analyze

import (
	"encoding/json"
	"fmt"
	"io"
)

func WriteJSON(w io.Writer, reports []*Report) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(Document{SchemaVersion: SchemaVersion, Reports: reports})
}

func WriteText(w io.Writer, report *Report, topN int) error {
	if topN <= 0 {
		topN = 10
	}
	fprintf := func(format string, args ...interface{}) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	}
	if err := fprintf("Aluka bundle analysis\nEntry: %s\n\n", report.Entry); err != nil {
		return err
	}
	if err := fprintf("Artifact\n  base       %10s\n  payload    %10s\n  total      %10s\n\n",
		formatBytes(report.Sizes.BaseBytes), formatBytes(report.Sizes.PayloadBytes), formatBytes(report.Sizes.ArtifactBytes)); err != nil {
		return err
	}
	raw := report.Stages.Raw
	shaken := report.Stages.Shaken
	minified := report.Stages.Minified
	optimized := report.Stages.BytecodeOptimized
	if err := fprintf("Optimization\n"); err != nil {
		return err
	}
	if err := fprintf("  raw bytecode       %10s  %d modules\n", formatBytes(raw.ModuleBytes), raw.ModuleCount); err != nil {
		return err
	}
	if err := fprintf("  tree-shaken        %10s  %s / %d modules\n", formatBytes(shaken.ModuleBytes), formatDelta(raw.ModuleBytes, shaken.ModuleBytes), shaken.ModuleCount); err != nil {
		return err
	}
	if err := fprintf("  AST minified       %10s  %s\n", formatBytes(minified.ModuleBytes), formatDelta(shaken.ModuleBytes, minified.ModuleBytes)); err != nil {
		return err
	}
	if err := fprintf("  bytecode optimized %10s  %s\n", formatBytes(optimized.ModuleBytes), formatDelta(minified.ModuleBytes, optimized.ModuleBytes)); err != nil {
		return err
	}
	if err := fprintf("  manifest/assets    %10s\n", formatBytes(report.Sizes.PayloadOverheadBytes+report.Sizes.AssetBytes)); err != nil {
		return err
	}
	if err := fprintf("  total saved        %10s  %.1f%%\n\n", formatBytes(raw.ModuleBytes-optimized.ModuleBytes), percent(raw.ModuleBytes-optimized.ModuleBytes, raw.ModuleBytes)); err != nil {
		return err
	}

	if err := fprintf("Hot modules\n"); err != nil {
		return err
	}
	limit := min(topN, len(report.Modules))
	if limit == 0 {
		if err := fprintf("  none\n"); err != nil {
			return err
		}
	}
	for i := 0; i < limit; i++ {
		m := report.Modules[i]
		if err := fprintf("  %2d. %-44s %10s  %5.1f%%  %s\n", i+1, m.Path, formatBytes(m.FinalBytecodeBytes), m.PayloadShare*100, m.ModuleType); err != nil {
			return err
		}
	}
	if err := fprintf("\n"); err != nil {
		return err
	}

	if len(report.Assets) > 0 {
		if err := fprintf("Assets\n"); err != nil {
			return err
		}
		assetLimit := min(topN, len(report.Assets))
		for i := 0; i < assetLimit; i++ {
			a := report.Assets[i]
			if err := fprintf("  %2d. %-44s %10s  %5.1f%%\n", i+1, a.Path, formatBytes(a.Bytes), a.PayloadShare*100); err != nil {
				return err
			}
		}
		if err := fprintf("\n"); err != nil {
			return err
		}
	}

	if err := fprintf("Findings\n"); err != nil {
		return err
	}
	if len(report.Findings) == 0 {
		return fprintf("  none\n")
	}
	for _, finding := range report.Findings {
		path := ""
		if finding.Path != "" {
			path = " (" + finding.Path + ")"
		}
		if err := fprintf("  [%s] %s%s\n    %s\n    %s\n", finding.Severity, finding.ID, path, finding.Message, finding.Suggestion); err != nil {
			return err
		}
	}
	return nil
}

func formatBytes(n int64) string {
	negative := n < 0
	if negative {
		n = -n
	}
	var value string
	switch {
	case n >= 1<<30:
		value = fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		value = fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		value = fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		value = fmt.Sprintf("%d B", n)
	}
	if negative {
		return "-" + value
	}
	return value
}

func formatDelta(before, after int64) string {
	delta := before - after
	if delta < 0 {
		return "+" + formatBytes(-delta)
	}
	return "-" + formatBytes(delta)
}

func percent(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}
