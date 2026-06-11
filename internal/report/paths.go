package report

import (
	"path/filepath"
	"strings"
)

// AnalysisReportPath derives the analysis report path for an input file:
// <dir>/<stem>-<ext>-analysis.md, in the same directory as the source. The
// extension is folded into the name so inputs that share a stem but differ by
// extension (e.g. foo.flac and foo.wav in a mixed-format batch directory) get
// distinct reports instead of silently clobbering one another. Examples:
// /x/voice.flac → /x/voice-flac-analysis.md; /tmp/raw → /tmp/raw-analysis.md.
func AnalysisReportPath(inputPath string) string {
	dir := filepath.Dir(inputPath)
	filename := filepath.Base(inputPath)
	ext := filepath.Ext(filename)
	nameWithoutExt := strings.TrimSuffix(filename, ext)
	stem := nameWithoutExt
	if ext != "" {
		stem += "-" + strings.TrimPrefix(ext, ".")
	}
	return filepath.Join(dir, stem+"-analysis.md")
}
