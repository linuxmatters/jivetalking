package processor

import "strings"

// filterGraphOptionValueEscaper backslash-escapes the characters that are
// special when a value is embedded in a filtergraph spec parsed by
// AVFilterGraphParsePtr. The value sits after stats_file= inside a
// comma-separated, optionally bracketed filter description, so ':' (option
// separator), ',' (filter separator), '\' (escape), the single quote, and the
// link-label brackets '[' ']' all require escaping per FFmpeg's first-level
// filtergraph escaping rule. NewReplacer scans left-to-right without
// reprocessing inserted backslashes, so '\' is handled correctly without
// ordering hazards.
var filterGraphOptionValueEscaper = strings.NewReplacer(
	`\`, `\\`,
	`'`, `\'`,
	`:`, `\:`,
	`,`, `\,`,
	`[`, `\[`,
	`]`, `\]`,
)

// escapeFilterGraphOptionValue returns path escaped so it can be concatenated
// after stats_file= inside a comma-separated filtergraph spec without
// corrupting the parse.
func escapeFilterGraphOptionValue(path string) string {
	return filterGraphOptionValueEscaper.Replace(path)
}
