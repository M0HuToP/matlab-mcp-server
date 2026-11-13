// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// q returns a MATLAB char-vector literal for s. Newlines are supported by
// concatenation with newline characters.
func q(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = "'" + strings.ReplaceAll(p, "'", "''") + "'"
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "[" + strings.Join(parts, " newline ") + "]"
}

// cellStr returns a MATLAB cell array of char vectors.
func cellStr(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = q(it)
	}
	return "{" + strings.Join(quoted, ", ") + "}"
}

// sortedKeys returns map keys in deterministic order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// nameValueArgs renders ", 'Name', 'Value', ..." for set_param/add_block.
func nameValueArgs(m map[string]string) string {
	var b strings.Builder
	for _, k := range sortedKeys(m) {
		b.WriteString(", ")
		b.WriteString(q(k))
		b.WriteString(", ")
		b.WriteString(q(m[k]))
	}
	return b.String()
}

// numVec renders a MATLAB row vector.
func numVec(v []float64) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(x, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// modelNameFromPath derives the block diagram name from a path or name
// ("C:\x\my.slx" -> "my", "my/Sub/Gain" -> "my").
func modelNameFromPath(p string) string {
	p = strings.TrimSpace(p)
	base := p
	if i := strings.LastIndexAny(base, `\`); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasSuffix(strings.ToLower(base), ".slx") || strings.HasSuffix(strings.ToLower(base), ".mdl") {
		base = base[:len(base)-4]
		return base
	}
	if i := strings.Index(base, "/"); i >= 0 {
		return base[:i]
	}
	return base
}

// isFilePath reports whether s looks like a model file path rather than a name.
func isFilePath(s string) bool {
	l := strings.ToLower(s)
	return strings.HasSuffix(l, ".slx") || strings.HasSuffix(l, ".mdl") || strings.ContainsAny(s, `\:`)
}

// loadModelCode emits code that makes sure the block diagram that contains
// target is loaded (works for models and libraries).
func loadModelCode(target string) string {
	if isFilePath(target) {
		return fmt.Sprintf("load_system(%s);\n", q(target))
	}
	root := modelNameFromPath(target)
	return fmt.Sprintf("if ~bdIsLoaded(%s), load_system(%s); end\n", q(root), q(root))
}

// resolveTarget converts a user supplied target (model file path, model name
// or block path) to a Simulink path string.
func resolveTarget(target string) string {
	if isFilePath(target) {
		return modelNameFromPath(target)
	}
	return target
}

// portSpec is a parsed port reference.
type portSpec struct {
	Field string // PortHandles field: Inport, Outport, LConn, RConn, Enable, Trigger, State, Ifaction, Reset, Event
	Index int
}

// parsePort parses a port token. isSource selects the default direction for
// plain numbers ("1" -> Outport(1) for sources, Inport(1) for destinations).
func parsePort(token string, isSource bool) (portSpec, error) {
	t := strings.ToLower(strings.TrimSpace(token))
	if t == "" {
		t = "1"
	}

	splitNum := func(prefix string) (int, bool) {
		if !strings.HasPrefix(t, prefix) {
			return 0, false
		}
		rest := strings.TrimSpace(t[len(prefix):])
		if rest == "" {
			return 1, true
		}
		n, err := strconv.Atoi(rest)
		if err != nil || n < 1 {
			return 0, false
		}
		return n, true
	}

	if n, err := strconv.Atoi(t); err == nil && n >= 1 {
		if isSource {
			return portSpec{"Outport", n}, nil
		}
		return portSpec{"Inport", n}, nil
	}

	table := []struct {
		prefixes []string
		field    string
	}{
		{[]string{"lconn", "l"}, "LConn"},
		{[]string{"rconn", "r"}, "RConn"},
		{[]string{"outport", "out", "o"}, "Outport"},
		{[]string{"inport", "in", "i"}, "Inport"},
		{[]string{"enable"}, "Enable"},
		{[]string{"trigger"}, "Trigger"},
		{[]string{"state"}, "State"},
		{[]string{"ifaction", "action"}, "Ifaction"},
		{[]string{"reset"}, "Reset"},
		{[]string{"event"}, "Event"},
	}

	// Check long keywords first to avoid "i" matching "ifaction".
	for _, kw := range []string{"enable", "trigger", "state", "ifaction", "action", "reset", "event"} {
		if n, ok := splitNum(kw); ok {
			for _, row := range table {
				for _, p := range row.prefixes {
					if p == kw {
						return portSpec{row.field, n}, nil
					}
				}
			}
		}
	}
	for _, row := range table {
		for _, p := range row.prefixes {
			if n, ok := splitNum(p); ok {
				return portSpec{row.field, n}, nil
			}
		}
	}

	return portSpec{}, fmt.Errorf("unrecognized port %q (use a number, inN, outN, lconnN, rconnN, enable, trigger, state, ifaction, reset)", token)
}

// endpoint is "<block path relative to system>/<port>".
type endpoint struct {
	Block string
	Port  portSpec
}

// parseEndpoint parses "<block path>/<port>". The port after the last slash
// is mandatory (e.g. "Gain/1", "Sub/Gain/out1", "Resistor/rconn1").
func parseEndpoint(s string, isSource bool) (endpoint, error) {
	s = strings.TrimSpace(s)
	i := strings.LastIndex(s, "/")
	if i <= 0 || i == len(s)-1 {
		return endpoint{}, fmt.Errorf("connection endpoint %q must have the form 'BlockName/port' (e.g. 'Gain/1', 'Resistor/rconn1')", s)
	}
	p, err := parsePort(s[i+1:], isSource)
	if err != nil {
		return endpoint{}, fmt.Errorf("endpoint %q: %w", s, err)
	}
	return endpoint{Block: s[:i], Port: p}, nil
}

// connectCode emits MATLAB code connecting two endpoints inside system sys
// (a MATLAB expression evaluating to the parent system path).
func connectCode(sysExpr string, src, dst endpoint) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mcp__srcBlk = [%s '/' %s];\n", sysExpr, q(src.Block))
	fmt.Fprintf(&b, "mcp__dstBlk = [%s '/' %s];\n", sysExpr, q(dst.Block))
	b.WriteString("mcp__srcPH = get_param(mcp__srcBlk, 'PortHandles');\n")
	b.WriteString("mcp__dstPH = get_param(mcp__dstBlk, 'PortHandles');\n")
	fmt.Fprintf(&b, "if numel(mcp__srcPH.%s) < %d, error('mcp:port', 'Block %%s has only %%d %s port(s).', mcp__srcBlk, numel(mcp__srcPH.%s)); end\n", src.Port.Field, src.Port.Index, src.Port.Field, src.Port.Field)
	fmt.Fprintf(&b, "if numel(mcp__dstPH.%s) < %d, error('mcp:port', 'Block %%s has only %%d %s port(s).', mcp__dstBlk, numel(mcp__dstPH.%s)); end\n", dst.Port.Field, dst.Port.Index, dst.Port.Field, dst.Port.Field)
	fmt.Fprintf(&b, "mcp__lh = add_line(get_param(mcp__srcBlk, 'Parent'), mcp__srcPH.%s(%d), mcp__dstPH.%s(%d), 'autorouting', 'on');\n", src.Port.Field, src.Port.Index, dst.Port.Field, dst.Port.Index)
	return b.String()
}

// printJSONCode prints a MATLAB variable as pretty JSON.
func printJSONCode(varName string) string {
	return fmt.Sprintf("disp(jsonencode(%s, 'PrettyPrint', true));\n", varName)
}

// boolStr returns MATLAB true/false.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// reportExpr returns a MATLAB expression producing the error report of
// exception variable v without the MCP server's own internal stack frames.
func reportExpr(v string) string {
	// Remove only the lines that point into the server's evaluation machinery,
	// keeping the message and every "Caused by" entry.
	return `strtrim(regexprep(getReport(` + v + `, 'extended', 'hyperlinks', 'off'), ` +
		`{'Error using (evalin|LiveEditorEvaluationHelper\w*)( \(line \d+\))?\n', '\n*Error in (LiveEditorEvaluationHelper\w*|matlab_mcp\.\w+|connector\.internal\.\w+)[^\n]*(\n {4}[^\n]*)?', '\n[ \t]*Error in Simulink\.SimulationInput[^\n]*', '\n[ \t]*Error in Simulink\.Simulation\w*[^\n]*', '(\n[ \t]*){3,}'}, {'', '', '', '', sprintf('\n\n')}))`
}
