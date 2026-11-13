// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/annotations"
	"github.com/matlab/matlab-mcp-server/internal/entities"
)

type ProjectArgs struct {
	Action    string   `json:"action"           jsonschema:"One of: 'create' (new project in folder), 'open' (open .prj file or project folder; runs its startup tasks), 'close' (close the current project; runs shutdown tasks, closes its models), 'info' (current project details), 'add_files' (add files/folders to the project), 'add_path' (add folders to the project path), 'add_startup_file', 'add_shortcut', 'run_checks', 'export' (archive to .mlproj/.zip), 'list_models'."`
	Path      string   `json:"path,omitempty"   jsonschema:"Project folder or .prj file (create/open), or archive file name (export)."`
	Name      string   `json:"name,omitempty"   jsonschema:"(Optional) Project name for 'create'."`
	Files     []string `json:"files,omitempty"  jsonschema:"(Optional) Files or folders for add_files / add_path / add_startup_file / add_shortcut. Relative paths are resolved against the project root."`
	OpenModel string   `json:"open_model,omitempty" jsonschema:"(Optional) After 'open', also open this model of the project in the Simulink Editor."`
}

func newProjectTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_project",
		"Manage MATLAB/Simulink Project",
		"Create, open, close and manage MATLAB/Simulink projects (.prj): start the project (runs startup files, sets the project path), shut it down, inspect it, add files/paths/startup files/shortcuts, run project checks, export an archive, list its models.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a ProjectArgs) (tools.RichContent, error) {
			code, err := projectCode(a)
			if err != nil {
				return tools.RichContent{}, err
			}
			return d.runMATLAB(ctx, logger, code)
		})
}

const projectInfoCode = `mcp__p = matlab.project.rootProject;
if isempty(mcp__p)
  disp('No project is open.');
else
  mcp__s = struct('name', mcp__p.Name, 'root', char(mcp__p.RootFolder), 'description', mcp__p.Description);
  mcp__s.file_count = numel(mcp__p.Files);
  try, mcp__s.project_path = cellfun(@char, {mcp__p.ProjectPath.File}, 'UniformOutput', false); catch, end
  try, mcp__s.startup_files = cellstr(string(mcp__p.StartupFiles)); catch, end
  try, mcp__s.shutdown_files = cellstr(string(mcp__p.ShutdownFiles)); catch, end
  try, mcp__s.shortcuts = cellstr(string({mcp__p.Shortcuts.File})); catch, end
  try, mcp__s.references = cellstr(string({mcp__p.ProjectReferences.File})); catch, end
  disp(jsonencode(mcp__s, 'PrettyPrint', true));
end
`

func projectCode(a ProjectArgs) (string, error) {
	var b strings.Builder
	filesCell := cellStr(a.Files)
	needProject := "mcp__p = matlab.project.rootProject; if isempty(mcp__p), error('mcp:project', 'No project is open. Open one with action=open.'); end\n"
	resolveFiles := fmt.Sprintf("mcp__f = %s; for mcp__i = 1:numel(mcp__f), if ~isfile(mcp__f{mcp__i}) && ~isfolder(mcp__f{mcp__i}), mcp__f{mcp__i} = fullfile(char(mcp__p.RootFolder), mcp__f{mcp__i}); end, end\n", filesCell)

	switch strings.ToLower(a.Action) {
	case "create":
		if a.Path == "" {
			return "", errors.New("path is required for create")
		}
		fmt.Fprintf(&b, "mcp__dir = %s; if ~isfolder(mcp__dir), mkdir(mcp__dir); end\n", q(a.Path))
		if a.Name != "" {
			fmt.Fprintf(&b, "mcp__p = matlab.project.createProject('Folder', mcp__dir, 'Name', %s);\n", q(a.Name))
		} else {
			b.WriteString("mcp__p = matlab.project.createProject(mcp__dir);\n")
		}
		b.WriteString("fprintf('Created project %s in %s\\n', mcp__p.Name, char(mcp__p.RootFolder));\n")
	case "open":
		if a.Path == "" {
			return "", errors.New("path is required for open")
		}
		fmt.Fprintf(&b, "mcp__p = openProject(%s); fprintf('Opened project %%s (%%s)\\n', mcp__p.Name, char(mcp__p.RootFolder));\n", q(a.Path))
		if a.OpenModel != "" {
			fmt.Fprintf(&b, "open_system(%s);\n", q(a.OpenModel))
		}
		b.WriteString(projectInfoCode)
	case "close":
		b.WriteString("mcp__p = matlab.project.rootProject; if isempty(mcp__p), disp('No project is open.'); else, mcp__n = mcp__p.Name; close(mcp__p); fprintf('Closed project %s\\n', mcp__n); end\n")
	case "info", "":
		b.WriteString(projectInfoCode)
	case "add_files":
		b.WriteString(needProject + resolveFiles)
		b.WriteString("for mcp__i = 1:numel(mcp__f), if isfolder(mcp__f{mcp__i}), addFolderIncludingChildFiles(mcp__p, mcp__f{mcp__i}); else, addFile(mcp__p, mcp__f{mcp__i}); end, fprintf('Added %s\\n', mcp__f{mcp__i}); end\n")
	case "add_path":
		b.WriteString(needProject + resolveFiles)
		b.WriteString("for mcp__i = 1:numel(mcp__f), addPath(mcp__p, mcp__f{mcp__i}); fprintf('Added to project path: %s\\n', mcp__f{mcp__i}); end\n")
	case "add_startup_file":
		b.WriteString(needProject + resolveFiles)
		b.WriteString("for mcp__i = 1:numel(mcp__f), addStartupFile(mcp__p, mcp__f{mcp__i}); fprintf('Startup file: %s\\n', mcp__f{mcp__i}); end\n")
	case "add_shortcut":
		b.WriteString(needProject + resolveFiles)
		b.WriteString("for mcp__i = 1:numel(mcp__f), addShortcut(mcp__p, mcp__f{mcp__i}); fprintf('Shortcut: %s\\n', mcp__f{mcp__i}); end\n")
	case "run_checks":
		b.WriteString(needProject)
		b.WriteString("mcp__c = runChecks(mcp__p); for mcp__i = 1:numel(mcp__c), fprintf('[%s] %s\\n', string(mcp__c(mcp__i).Passed), mcp__c(mcp__i).Description); end\n")
	case "export":
		if a.Path == "" {
			return "", errors.New("path (archive file) is required for export")
		}
		b.WriteString(needProject)
		fmt.Fprintf(&b, "export(mcp__p, %s); fprintf('Exported to %%s\\n', %s);\n", q(a.Path), q(a.Path))
	case "list_models":
		b.WriteString(needProject)
		b.WriteString("mcp__fl = string({mcp__p.Files.Path}); mcp__fl = mcp__fl(endsWith(mcp__fl, [\".slx\", \".mdl\"])); fprintf('%s\\n', mcp__fl);\n")
	default:
		return "", fmt.Errorf("unsupported action %q", a.Action)
	}
	return b.String(), nil
}
