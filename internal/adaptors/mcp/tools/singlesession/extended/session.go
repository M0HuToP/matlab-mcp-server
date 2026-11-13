// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/matlab/matlab-mcp-server/internal/adaptors/globalmatlab/sessionmanager"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/annotations"
	"github.com/matlab/matlab-mcp-server/internal/entities"
)

// ---------------------------------------------------------------- start_matlab

type StartMATLABArgs struct {
	MATLABRelease   string `json:"matlab_release,omitempty"    jsonschema:"(Optional) MATLAB release to start, e.g. R2024a. The server looks for it among discovered installations and in standard install folders. Ignored if matlab_root is given."`
	MATLABRoot      string `json:"matlab_root,omitempty"       jsonschema:"(Optional) Absolute path of the MATLAB installation folder (without /bin), e.g. C:\\Program Files\\MATLAB\\R2024a."`
	StartingFolder  string `json:"starting_folder,omitempty"   jsonschema:"(Optional) Absolute path of the folder MATLAB starts in."`
	DisplayMode     string `json:"display_mode,omitempty"      jsonschema:"(Optional) 'desktop' to show the MATLAB desktop, 'nodesktop' to run without it. Default: the server configuration."`
	ForceNewSession bool   `json:"force_new_session,omitempty" jsonschema:"(Optional) Launch a new MATLAB process even if the server is configured to attach to an existing shared session. Implied when matlab_release or matlab_root is given."`
	Restart         bool   `json:"restart,omitempty"           jsonschema:"(Optional) If MATLAB is already running, quit it and start again with the given options."`
	StartSimulink   bool   `json:"start_simulink,omitempty"    jsonschema:"(Optional) Preload Simulink after MATLAB starts (start_simulink) so that the first model operation is fast."`
}

func newStartMATLABTool(d *deps) tools.Tool {
	return newTool(d,
		"start_matlab",
		"Start MATLAB",
		"Start (launch) MATLAB on request. Choose a specific release (e.g. matlab_release='R2024a') or installation folder, desktop or nodesktop mode, and starting folder. "+
			"If MATLAB is already running nothing happens unless restart=true. Returns version information. Other tools also start MATLAB automatically on first use with the default settings.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a StartMATLABArgs) (tools.RichContent, error) {
			var notes []string

			if a.Restart && d.session.IsRunning() {
				if _, err := d.session.Stop(ctx, logger); err != nil {
					notes = append(notes, "Warning while stopping previous session: "+err.Error())
				} else {
					notes = append(notes, "Previous MATLAB session stopped.")
				}
			}

			var overrides *sessionmanager.StartupOverrides
			if a.MATLABRelease != "" || a.MATLABRoot != "" || a.StartingFolder != "" || a.DisplayMode != "" || a.ForceNewSession {
				o := sessionmanager.StartupOverrides{
					StartingFolder:  a.StartingFolder,
					ForceNewSession: a.ForceNewSession,
				}

				root := a.MATLABRoot
				if root == "" && a.MATLABRelease != "" {
					found, err := findMATLABRelease(ctx, logger, d.environments, a.MATLABRelease)
					if err != nil {
						return tools.RichContent{}, err
					}
					root = found
				}
				if root != "" {
					if _, err := os.Stat(root); err != nil {
						return tools.RichContent{}, fmt.Errorf("MATLAB root %q does not exist: %w", root, err)
					}
					o.MATLABRoot = root
					o.ForceNewSession = true
					notes = append(notes, "Using MATLAB root: "+root)
				}

				switch strings.ToLower(a.DisplayMode) {
				case "":
				case "desktop":
					v := true
					o.ShowDesktop = &v
				case "nodesktop":
					v := false
					o.ShowDesktop = &v
				default:
					return tools.RichContent{}, fmt.Errorf("display_mode must be 'desktop' or 'nodesktop', got %q", a.DisplayMode)
				}
				overrides = &o
			}

			client, already, err := d.session.Start(ctx, logger, overrides)
			if err != nil {
				return tools.RichContent{}, err
			}
			if already {
				notes = append(notes, "MATLAB is already running (use restart=true to restart it with new options).")
			} else {
				notes = append(notes, "MATLAB started.")
			}

			body := "mcp__v = ver('MATLAB'); fprintf('MATLAB %s %s\\nmatlabroot: %s\\npwd: %s\\n', mcp__v.Version, mcp__v.Release, matlabroot, pwd);\n"
			if a.StartSimulink {
				body += "mcp__t = tic; start_simulink; fprintf('Simulink loaded in %.1f s.\\n', toc(mcp__t));\n"
			}
			res, err := d.runWithClient(ctx, logger, client, body)
			if err != nil {
				return tools.RichContent{}, err
			}
			res.TextContent = append([]string{strings.Join(notes, "\n")}, res.TextContent...)
			return res, nil
		})
}

// ---------------------------------------------------------------- stop_matlab

type StopMATLABArgs struct {
	SaveModels       bool `json:"save_models,omitempty"     jsonschema:"(Optional) Save all modified Simulink models before quitting."`
	CloseProject     bool `json:"close_project,omitempty"   jsonschema:"(Optional) Close the currently open MATLAB/Simulink project before quitting."`
	QuitSharedMATLAB bool `json:"quit_shared_matlab,omitempty" jsonschema:"(Optional) When the server is attached to an existing shared MATLAB session, also quit that MATLAB (by default the server only detaches)."`
	ResetOptions     bool `json:"reset_options,omitempty"   jsonschema:"(Optional) Forget options passed to start_matlab (release, display mode, ...) so that the next start uses the server configuration."`
}

func newStopMATLABTool(d *deps) tools.Tool {
	return newTool(d,
		"stop_matlab",
		"Stop MATLAB",
		"Quit the MATLAB session used by the server on request. Optionally saves open Simulink models and closes the open project first. "+
			"Unsaved Simulink changes are discarded unless save_models=true. The next MATLAB tool call starts MATLAB again.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a StopMATLABArgs) (tools.RichContent, error) {
			if !d.session.IsRunning() {
				if a.ResetOptions {
					d.session.ResetStartupOverrides()
				}
				return textResult("MATLAB is not running."), nil
			}

			var notes []string
			client, err := d.session.Client(ctx, logger)
			if err == nil {
				var b strings.Builder
				if a.SaveModels {
					b.WriteString("mcp__m = find_system('SearchDepth', 0, 'Type', 'block_diagram', 'BlockDiagramType', 'model');\n")
					b.WriteString("for mcp__i = 1:numel(mcp__m), if strcmp(get_param(mcp__m{mcp__i}, 'Dirty'), 'on'), try, save_system(mcp__m{mcp__i}); fprintf('Saved %s\\n', mcp__m{mcp__i}); catch mcp__e, fprintf('Could not save %s: %s\\n', mcp__m{mcp__i}, mcp__e.message); end, end, end\n")
				}
				if a.CloseProject {
					b.WriteString("try, mcp__p = matlab.project.rootProject; if ~isempty(mcp__p), close(mcp__p); disp('Project closed.'); end, catch, end\n")
				}
				// Discard unsaved changes of models only (so no save dialog can block
				// quitting). Libraries are left to MATLAB's exit: closing hundreds
				// of loaded libraries one by one took minutes.
				b.WriteString("if is_simulink_loaded(), mcp__m = find_system('SearchDepth', 0, 'Type', 'block_diagram', 'BlockDiagramType', 'model'); for mcp__i = 1:numel(mcp__m), try, close_system(mcp__m{mcp__i}, 0); catch, end, end, end\n")
				if res, runErr := d.runWithClient(ctx, logger, client, b.String()); runErr == nil {
					notes = append(notes, res.TextContent...)
				} else {
					notes = append(notes, "Warning during cleanup: "+runErr.Error())
				}

				if a.QuitSharedMATLAB {
					// The connection is lost while MATLAB exits, so the error is expected.
					_, _ = client.Eval(ctx, logger, entities.EvalRequest{Code: "quit('force')"})
				}
			}

			if _, err := d.session.Stop(ctx, logger); err != nil {
				notes = append(notes, "Stop reported: "+err.Error())
			} else {
				notes = append(notes, "MATLAB session stopped.")
			}
			if a.ResetOptions {
				d.session.ResetStartupOverrides()
			}
			return textResult(strings.TrimSpace(strings.Join(notes, "\n"))), nil
		})
}

// ---------------------------------------------------------------- matlab_status

type StatusArgs struct{}

func newStatusTool(d *deps) tools.Tool {
	return newTool(d,
		"matlab_status",
		"MATLAB Status",
		"Report whether MATLAB is running (without starting it). If running: version, matlabroot, current folder, loaded Simulink models (dirty flag, simulation status) and open project.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, _ StatusArgs) (tools.RichContent, error) {
			if !d.session.IsRunning() {
				return textResult("MATLAB is not running. Use start_matlab to start it."), nil
			}
			body := `mcp__s = struct();
mcp__v = ver('MATLAB');
mcp__s.version = mcp__v.Version; mcp__s.release = mcp__v.Release; mcp__s.matlabroot = matlabroot; mcp__s.pwd = pwd;
mcp__s.simulink_loaded = ~isempty(which('bdroot')) && is_simulink_loaded();
mcp__s.loaded_models = {};
if mcp__s.simulink_loaded
  mcp__bd = find_system('SearchDepth', 0, 'Type', 'block_diagram');
  for mcp__i = 1:numel(mcp__bd)
    mcp__s.loaded_models{end+1} = struct('name', mcp__bd{mcp__i}, 'type', get_param(mcp__bd{mcp__i}, 'BlockDiagramType'), ...
      'dirty', get_param(mcp__bd{mcp__i}, 'Dirty'), 'file', get_param(mcp__bd{mcp__i}, 'FileName'), 'sim_status', get_param(mcp__bd{mcp__i}, 'SimulationStatus'));
  end
end
mcp__s.project = '';
try, mcp__p = matlab.project.rootProject; if ~isempty(mcp__p), mcp__s.project = struct('name', mcp__p.Name, 'root', char(mcp__p.RootFolder)); end, catch, end
` + printJSONCode("mcp__s")
			return d.runMATLAB(ctx, logger, body)
		})
}

// ---------------------------------------------------------------- list_installed_matlabs

type ListInstallsArgs struct{}

func newListInstallsTool(d *deps) tools.Tool {
	return newTool(d,
		"list_installed_matlabs",
		"List Installed MATLAB Releases",
		"List MATLAB installations available on this computer (release and root folder). Does not start MATLAB. Use the result with start_matlab.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, _ ListInstallsArgs) (tools.RichContent, error) {
			installs := discoverInstallations(ctx, logger, d.environments)
			if len(installs) == 0 {
				return textResult("No MATLAB installations found (searched PATH and standard install folders)."), nil
			}
			var b strings.Builder
			for _, in := range installs {
				fmt.Fprintf(&b, "%s\t%s\n", in.Version, in.MATLABRoot)
			}
			return textResult(strings.TrimSpace(b.String())), nil
		})
}

func standardInstallFolders() []string {
	switch runtime.GOOS {
	case "windows":
		var dirs []string
		for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
			if v := os.Getenv(env); v != "" {
				dirs = append(dirs, filepath.Join(v, "MATLAB"))
			}
		}
		dirs = append(dirs, `C:\Program Files\MATLAB`)
		return dirs
	case "darwin":
		return []string{"/Applications"}
	default:
		home, _ := os.UserHomeDir()
		return []string{"/usr/local/MATLAB", "/opt/MATLAB", filepath.Join(home, "MATLAB")}
	}
}

func discoverInstallations(ctx context.Context, logger entities.Logger, lister EnvironmentLister) []entities.EnvironmentInfo {
	seen := map[string]bool{}
	var result []entities.EnvironmentInfo
	add := func(root, version string) {
		key := strings.ToLower(filepath.Clean(root))
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, entities.EnvironmentInfo{MATLABRoot: root, Version: version})
	}

	if lister != nil {
		for _, e := range lister.ListEnvironments(ctx, logger) {
			add(e.MATLABRoot, e.Version)
		}
	}

	for _, dir := range standardInstallFolders() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			release := ""
			switch {
			case strings.HasPrefix(name, "MATLAB_R") && strings.HasSuffix(name, ".app"):
				release = strings.TrimSuffix(strings.TrimPrefix(name, "MATLAB_"), ".app")
			case strings.HasPrefix(name, "R20"):
				release = name
			default:
				continue
			}
			root := filepath.Join(dir, name)
			exe := filepath.Join(root, "bin", "matlab")
			if runtime.GOOS == "windows" {
				exe += ".exe"
			}
			if _, err := os.Stat(exe); err == nil {
				add(root, release)
			}
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Version > result[j].Version })
	return result
}

func findMATLABRelease(ctx context.Context, logger entities.Logger, lister EnvironmentLister, release string) (string, error) {
	want := strings.ToLower(strings.TrimSpace(release))
	if !strings.HasPrefix(want, "r") {
		want = "r" + want
	}
	installs := discoverInstallations(ctx, logger, lister)
	for _, in := range installs {
		if strings.EqualFold(in.Version, want) || strings.Contains(strings.ToLower(filepath.Base(in.MATLABRoot)), want) {
			return in.MATLABRoot, nil
		}
	}
	var available []string
	for _, in := range installs {
		available = append(available, in.Version+" ("+in.MATLABRoot+")")
	}
	return "", fmt.Errorf("MATLAB %s not found. Available: %s. Pass matlab_root explicitly if it is installed elsewhere", release, strings.Join(available, "; "))
}
