// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/annotations"
	"github.com/matlab/matlab-mcp-server/internal/entities"
)

// ---------------------------------------------------------------- run_matlab_command

type RunCommandArgs struct {
	Commands        []string `json:"commands"                    jsonschema:"MATLAB Command Window commands, executed in order in the base workspace exactly as if typed at the >> prompt (any command or command syntax is allowed, e.g. 'cd C:\\work', 'x = 1:10', 'format long', 'which sim', 'pkg.fun(1)'). Each element may also contain several lines."`
	WorkingFolder   string   `json:"working_folder,omitempty"    jsonschema:"(Optional) Absolute path to cd into before executing the commands."`
	ContinueOnError bool     `json:"continue_on_error,omitempty" jsonschema:"(Optional) Keep executing the remaining commands after one fails. Default false (stop at first error)."`
	CaptureFigures  bool     `json:"capture_figures,omitempty"   jsonschema:"(Optional) Return all figures that are open after the commands as PNG images."`
}

func newRunCommandTool(d *deps) tools.Tool {
	return newTool(d,
		"run_matlab_command",
		"Run MATLAB Commands",
		"Execute one or more MATLAB Command Window commands (full command-line support: statements, command syntax, scripts, functions, toolboxes, system commands via '!' or system()). "+
			"The output of every command is captured separately with evalc and returned in order, together with any error report. "+
			"Do not use restoredefaultpath (it disconnects the server).",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a RunCommandArgs) (tools.RichContent, error) {
			if len(a.Commands) == 0 {
				return tools.RichContent{}, errors.New("commands must contain at least one command")
			}
			var b strings.Builder
			if a.WorkingFolder != "" {
				fmt.Fprintf(&b, "cd(%s);\n", q(a.WorkingFolder))
			}
			// State lives in root appdata so that user commands such as 'clear'
			// or 'clearvars' cannot break the loop; commands run in the base workspace.
			fmt.Fprintf(&b, "setappdata(0, 'mcp_cmds', %s);\n", cellStr(a.Commands))
			fmt.Fprintf(&b, "setappdata(0, 'mcp_state', struct('k', 0, 'failed', 0, 'cont', %s));\n", boolStr(a.ContinueOnError))
			b.WriteString(`while true
  mcp__st = getappdata(0, 'mcp_state'); mcp__c = getappdata(0, 'mcp_cmds');
  mcp__st.k = mcp__st.k + 1;
  if mcp__st.k > numel(mcp__c), break; end
  setappdata(0, 'mcp_state', mcp__st); setappdata(0, 'mcp_cmd', mcp__c{mcp__st.k});
  fprintf('>> %s\n', mcp__c{mcp__st.k});
  try
    mcp__o = evalc('evalin(''base'', getappdata(0, ''mcp_cmd''))');
    if ~isempty(mcp__o), fprintf('%s', mcp__o); if mcp__o(end) ~= newline, fprintf('\n'); end, end
  catch mcp__e
    mcp__st = getappdata(0, 'mcp_state'); mcp__st.failed = mcp__st.failed + 1; setappdata(0, 'mcp_state', mcp__st);
    fprintf('ERROR: %s\n', ` + reportExpr("mcp__e") + `);
    if ~mcp__st.cont, break; end
  end
end
mcp__st = getappdata(0, 'mcp_state');
if mcp__st.failed > 0, fprintf('\n%d command(s) failed.\n', mcp__st.failed); end
rmappdata(0, 'mcp_cmds'); rmappdata(0, 'mcp_state'); if isappdata(0, 'mcp_cmd'), rmappdata(0, 'mcp_cmd'); end
`)
			if a.CaptureFigures && !d.captureMode() {
				// In capture (nodesktop) mode new figures are already returned by the server.
				b.WriteString(figureCaptureCode())
			}
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// figureCaptureCode emits code saving all open figures to temp PNGs and
// printing image markers.
func figureCaptureCode() string {
	return `mcp__figs = findall(groot, 'Type', 'figure');
for mcp__f = 1:numel(mcp__figs)
  mcp__png = [tempname '.png'];
  try
    exportgraphics(mcp__figs(mcp__f), mcp__png, 'Resolution', 110);
  catch
    print(mcp__figs(mcp__f), mcp__png, '-dpng', '-r110');
  end
  fprintf('` + imagePrefix + `%s` + imageSuffix + `\n', mcp__png);
end
`
}

// ---------------------------------------------------------------- run_shell_command

type ShellArgs struct {
	Command        string `json:"command"                   jsonschema:"Operating system command line to execute (Windows: cmd.exe or PowerShell; Linux/macOS: sh). Does not need MATLAB."`
	Shell          string `json:"shell,omitempty"           jsonschema:"(Optional) 'cmd' (Windows default), 'powershell', or 'sh' (Linux/macOS default)."`
	WorkingFolder  string `json:"working_folder,omitempty"  jsonschema:"(Optional) Absolute path of the working folder."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"(Optional) Kill the command after this many seconds. Default 120."`
}

func newShellTool(d *deps) tools.Tool {
	return newTool(d,
		"run_shell_command",
		"Run OS Shell Command",
		"Execute an operating-system command line (cmd / PowerShell / sh) directly from the MCP server, e.g. to launch matlab.exe with custom flags (-batch, -r), call mex compilers, git, file operations. Returns stdout, stderr and exit code.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, _ entities.Logger, a ShellArgs) (tools.RichContent, error) {
			if strings.TrimSpace(a.Command) == "" {
				return tools.RichContent{}, errors.New("command must not be empty")
			}
			timeout := time.Duration(a.TimeoutSeconds) * time.Second
			if timeout <= 0 {
				timeout = 120 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			shell := strings.ToLower(a.Shell)
			if shell == "" {
				if runtime.GOOS == "windows" {
					shell = "cmd"
				} else {
					shell = "sh"
				}
			}
			var cmd *exec.Cmd
			switch shell {
			case "cmd":
				cmd = exec.CommandContext(cctx, "cmd.exe", "/C", a.Command) //nolint:gosec // executing user commands is the purpose of this tool
			case "powershell", "pwsh":
				exe := "powershell.exe"
				if shell == "pwsh" {
					exe = "pwsh"
				}
				cmd = exec.CommandContext(cctx, exe, "-NoProfile", "-NonInteractive", "-Command", a.Command) //nolint:gosec
			case "sh", "bash":
				cmd = exec.CommandContext(cctx, shell, "-c", a.Command) //nolint:gosec
			default:
				return tools.RichContent{}, fmt.Errorf("unsupported shell %q", a.Shell)
			}
			if a.WorkingFolder != "" {
				cmd.Dir = a.WorkingFolder
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()

			exitCode := 0
			var exitErr *exec.ExitError
			switch {
			case err == nil:
			case errors.As(err, &exitErr):
				exitCode = exitErr.ExitCode()
			case errors.Is(cctx.Err(), context.DeadlineExceeded):
				exitCode = -1
			default:
				return tools.RichContent{}, err
			}

			var b strings.Builder
			fmt.Fprintf(&b, "exit code: %d\n", exitCode)
			if errors.Is(cctx.Err(), context.DeadlineExceeded) {
				fmt.Fprintf(&b, "(killed after %s timeout)\n", timeout)
			}
			if stdout.Len() > 0 {
				b.WriteString("--- stdout ---\n")
				b.WriteString(stdout.String())
				b.WriteString("\n")
			}
			if stderr.Len() > 0 {
				b.WriteString("--- stderr ---\n")
				b.WriteString(stderr.String())
			}
			return textResult(strings.TrimSpace(b.String())), nil
		})
}

// ---------------------------------------------------------------- get_matlab_workspace

type WorkspaceArgs struct {
	Variables []string `json:"variables,omitempty" jsonschema:"(Optional) Names of variables whose values should be displayed. If omitted, only the list of variables (whos) is returned."`
	MaxChars  int      `json:"max_chars,omitempty" jsonschema:"(Optional) Maximum characters of displayed value per variable. Default 4000."`
}

func newWorkspaceTool(d *deps) tools.Tool {
	return newTool(d,
		"get_matlab_workspace",
		"Get MATLAB Workspace",
		"List the variables in the MATLAB base workspace (name, size, class, bytes) and optionally display the values of selected variables.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a WorkspaceArgs) (tools.RichContent, error) {
			maxChars := a.MaxChars
			if maxChars <= 0 {
				maxChars = 4000
			}
			var b strings.Builder
			b.WriteString("mcp__w = evalin('base', 'whos');\n")
			b.WriteString("if isempty(mcp__w), disp('Workspace is empty.'); else\n")
			b.WriteString("fprintf('%-30s %-15s %-12s %s\\n', 'Name', 'Size', 'Class', 'Bytes');\n")
			b.WriteString("for mcp__i = 1:numel(mcp__w), if startsWith(mcp__w(mcp__i).name, 'mcp__'), continue; end, fprintf('%-30s %-15s %-12s %d\\n', mcp__w(mcp__i).name, strjoin(string(mcp__w(mcp__i).size), 'x'), mcp__w(mcp__i).class, mcp__w(mcp__i).bytes); end\nend\n")
			if len(a.Variables) > 0 {
				fmt.Fprintf(&b, "mcp__vars = %s;\n", cellStr(a.Variables))
				fmt.Fprintf(&b, `for mcp__i = 1:numel(mcp__vars)
  fprintf('\n=== %%s ===\n', mcp__vars{mcp__i});
  try
    mcp__txt = evalin('base', ['evalc(''disp(' mcp__vars{mcp__i} ')'')']);
    if numel(mcp__txt) > %d, mcp__txt = [mcp__txt(1:%d) newline '... (truncated)']; end
    fprintf('%%s\n', mcp__txt);
  catch mcp__e
    fprintf('Cannot display: %%s\n', mcp__e.message);
  end
end
`, maxChars, maxChars)
			}
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- get_matlab_help

type HelpArgs struct {
	Topic   string `json:"topic"              jsonschema:"Function, class, block or toolbox name, e.g. 'sim', 'add_block', 'Simulink.SimulationInput', 'simscape'."`
	OpenDoc bool   `json:"open_doc,omitempty" jsonschema:"(Optional) Also open the documentation page in the MATLAB Help browser."`
}

func newHelpTool(d *deps) tools.Tool {
	return newTool(d,
		"get_matlab_help",
		"Get MATLAB Help",
		"Return the MATLAB help text for a function, class, or toolbox, plus where it is defined (which). Useful to check exact syntax of MATLAB, Simulink and Simscape functions before calling them.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a HelpArgs) (tools.RichContent, error) {
			if strings.TrimSpace(a.Topic) == "" {
				return tools.RichContent{}, errors.New("topic must not be empty")
			}
			var b strings.Builder
			fmt.Fprintf(&b, "mcp__t = %s;\n", q(a.Topic))
			b.WriteString("fprintf('which: %s\\n\\n', strjoin(cellstr(which(mcp__t, '-all')), newline));\n")
			b.WriteString("mcp__h = help(mcp__t); if isempty(strtrim(mcp__h)), mcp__h = evalc('help(mcp__t)'); end; disp(mcp__h);\n")
			if a.OpenDoc {
				b.WriteString("doc(mcp__t);\n")
			}
			return d.runMATLAB(ctx, logger, b.String())
		})
}
