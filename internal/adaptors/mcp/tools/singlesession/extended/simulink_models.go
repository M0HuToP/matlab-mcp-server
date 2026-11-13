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

// splitModelFile splits "C:\dir\name.slx" into ("C:\dir", "name"). For plain
// names it returns ("", name).
func splitModelFile(p string) (folder, name string) {
	p = strings.TrimSpace(p)
	if !isFilePath(p) {
		return "", p
	}
	i := strings.LastIndexAny(p, `/\`)
	if i >= 0 {
		folder, name = p[:i], p[i+1:]
	} else {
		name = p
	}
	l := strings.ToLower(name)
	if strings.HasSuffix(l, ".slx") || strings.HasSuffix(l, ".mdl") {
		name = name[:len(name)-4]
	}
	return folder, name
}

// ---------------------------------------------------------------- simulink_new_model

type NewModelArgs struct {
	Name      string `json:"name"                jsonschema:"Model name (valid MATLAB identifier) or full file path ending in .slx, e.g. C:\\work\\motor.slx."`
	Folder    string `json:"folder,omitempty"    jsonschema:"(Optional) Folder where the model file is saved. Default: current MATLAB folder."`
	Type      string `json:"type,omitempty"      jsonschema:"(Optional) 'model' (default), 'library' or 'subsystem' (subsystem reference file)."`
	Template  string `json:"template,omitempty"  jsonschema:"(Optional) Simulink template (.sltx) to create the model from."`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"(Optional) Close a loaded model with the same name without saving and overwrite an existing file."`
	Open      bool   `json:"open,omitempty"      jsonschema:"(Optional) Open the model in the Simulink Editor (otherwise it is only loaded in memory)."`
}

func newNewModelTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_new_model",
		"Create Simulink Model",
		"Create a new empty Simulink model, library or subsystem file and save it to disk. Starts Simulink if needed.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a NewModelArgs) (tools.RichContent, error) {
			folder, name := splitModelFile(a.Name)
			if a.Folder != "" {
				folder = a.Folder
			}
			if name == "" {
				return tools.RichContent{}, errors.New("name must not be empty")
			}
			var b strings.Builder
			b.WriteString(createModelCode(name, folder, a.Type, a.Template, a.Overwrite))
			b.WriteString("save_system(mcp__mdl, mcp__file);\n")
			if a.Open {
				b.WriteString("open_system(mcp__mdl);\n")
			}
			b.WriteString("fprintf('Created %s (%s)\\n', mcp__mdl, get_param(mcp__mdl, 'FileName'));\n")
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// createModelCode leaves the model name in mcp__mdl and target file in mcp__file.
func createModelCode(name, folder, kind, template string, overwrite bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mcp__mdl = %s;\n", q(name))
	if folder != "" {
		fmt.Fprintf(&b, "mcp__folder = %s; if ~isfolder(mcp__folder), mkdir(mcp__folder); end\n", q(folder))
	} else {
		b.WriteString("mcp__folder = pwd;\n")
	}
	b.WriteString("mcp__file = fullfile(mcp__folder, [mcp__mdl '.slx']);\n")
	b.WriteString("if ~is_simulink_loaded(), start_simulink; end\n")
	if overwrite {
		b.WriteString("if bdIsLoaded(mcp__mdl), close_system(mcp__mdl, 0); end\n")
	} else {
		b.WriteString("if bdIsLoaded(mcp__mdl), error('mcp:exists', 'A model named %s is already loaded. Use overwrite=true or another name.', mcp__mdl); end\n")
		b.WriteString("if isfile(mcp__file), error('mcp:exists', 'File %s already exists. Use overwrite=true, open it with simulink_open_model, or append to it.', mcp__file); end\n")
	}
	switch {
	case template != "":
		fmt.Fprintf(&b, "Simulink.createFromTemplate(%s, 'Name', mcp__mdl);\n", q(template))
	case strings.EqualFold(kind, "library"):
		b.WriteString("new_system(mcp__mdl, 'Library');\n")
	case strings.EqualFold(kind, "subsystem"):
		b.WriteString("new_system(mcp__mdl, 'Subsystem');\n")
	default:
		b.WriteString("new_system(mcp__mdl);\n")
	}
	return b.String()
}

// ---------------------------------------------------------------- simulink_open_model

type OpenModelArgs struct {
	Model    string `json:"model"               jsonschema:"Model file path (.slx/.mdl) or name of a model on the MATLAB path. Also accepts a library name (e.g. 'simulink', 'fl_lib') or a subsystem path to open."`
	LoadOnly bool   `json:"load_only,omitempty" jsonschema:"(Optional) Load into memory without opening the Simulink Editor window (faster, works without desktop)."`
}

func newOpenModelTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_open_model",
		"Open Simulink Model",
		"Open (or only load) an existing Simulink model, library or subsystem. Starts Simulink if needed. Returns the model name, file and main configuration.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a OpenModelArgs) (tools.RichContent, error) {
			if strings.TrimSpace(a.Model) == "" {
				return tools.RichContent{}, errors.New("model must not be empty")
			}
			var b strings.Builder
			b.WriteString("if ~is_simulink_loaded(), start_simulink; end\n")
			if a.LoadOnly {
				fmt.Fprintf(&b, "mcp__h = load_system(%s);\n", q(a.Model))
			} else {
				fmt.Fprintf(&b, "open_system(%s); mcp__h = get_param(bdroot(gcs), 'Handle');\n", q(a.Model))
				if isFilePath(a.Model) {
					fmt.Fprintf(&b, "mcp__h = get_param(%s, 'Handle');\n", q(modelNameFromPath(a.Model)))
				}
			}
			b.WriteString(modelSummaryCode("get_param(mcp__h, 'Name')"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

func modelSummaryCode(nameExpr string) string {
	return fmt.Sprintf(`mcp__m = %s;
mcp__s = struct('name', mcp__m, 'type', get_param(mcp__m, 'BlockDiagramType'), 'file', get_param(mcp__m, 'FileName'), 'dirty', get_param(mcp__m, 'Dirty'));
if strcmp(mcp__s.type, 'model')
  mcp__s.stop_time = get_param(mcp__m, 'StopTime'); mcp__s.solver = get_param(mcp__m, 'Solver'); mcp__s.solver_type = get_param(mcp__m, 'SolverType');
  mcp__s.simulation_mode = get_param(mcp__m, 'SimulationMode');
end
mcp__s.top_level_blocks = numel(find_system(mcp__m, 'SearchDepth', 1, 'Type', 'block'));
`, nameExpr) + printJSONCode("mcp__s")
}

// ---------------------------------------------------------------- simulink_save_model

type SaveModelArgs struct {
	Model           string `json:"model"               jsonschema:"Name of the loaded model (or 'all' to save every modified model)."`
	FilePath        string `json:"file_path,omitempty" jsonschema:"(Optional) Save as this file (.slx). Note: renames the model to the file name."`
	ExportToVersion string `json:"export_to_version,omitempty" jsonschema:"(Optional) Instead of saving, export a copy for an older release, e.g. R2022b (requires file_path)."`
}

func newSaveModelTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_save_model",
		"Save Simulink Model",
		"Save a loaded Simulink model or library, optionally under a new file name or exported for an older Simulink release.",
		annotations.NewIdempotentWriteAnnotations(),
		func(ctx context.Context, logger entities.Logger, a SaveModelArgs) (tools.RichContent, error) {
			var b strings.Builder
			switch {
			case strings.EqualFold(a.Model, "all"):
				b.WriteString("mcp__m = find_system('SearchDepth', 0, 'Type', 'block_diagram');\n")
				b.WriteString("for mcp__i = 1:numel(mcp__m), if strcmp(get_param(mcp__m{mcp__i}, 'Dirty'), 'on') && ~strcmp(get_param(mcp__m{mcp__i}, 'Lock'), 'on'), save_system(mcp__m{mcp__i}); fprintf('Saved %s\\n', get_param(mcp__m{mcp__i}, 'FileName')); end, end\n")
			case a.ExportToVersion != "":
				if a.FilePath == "" {
					return tools.RichContent{}, errors.New("file_path is required with export_to_version")
				}
				fmt.Fprintf(&b, "mcp__f = Simulink.exportToVersion(%s, %s, %s); fprintf('Exported to %%s\\n', mcp__f);\n", q(resolveTarget(a.Model)), q(a.FilePath), q(a.ExportToVersion))
			case a.FilePath != "":
				fmt.Fprintf(&b, "save_system(%s, %s); fprintf('Saved to %%s\\n', %s);\n", q(resolveTarget(a.Model)), q(a.FilePath), q(a.FilePath))
			default:
				fmt.Fprintf(&b, "mcp__m = %s; save_system(mcp__m); fprintf('Saved %%s\\n', get_param(mcp__m, 'FileName'));\n", q(resolveTarget(a.Model)))
			}
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_close_model

type CloseModelArgs struct {
	Model string `json:"model"          jsonschema:"Name of the model/library to close, or 'all' to close every block diagram."`
	Save  bool   `json:"save,omitempty" jsonschema:"(Optional) Save before closing. Default false: unsaved changes are discarded."`
}

func newCloseModelTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_close_model",
		"Close Simulink Model",
		"Close a Simulink model (or all models), stopping a running simulation first. Set save=true to keep changes; otherwise changes are discarded.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a CloseModelArgs) (tools.RichContent, error) {
			var b strings.Builder
			if strings.EqualFold(a.Model, "all") || a.Model == "" {
				// Only models and the user's own files; MathWorks libraries loaded
				// for block search stay in memory.
				b.WriteString("if is_simulink_loaded()\n mcp__m = find_system('SearchDepth', 0, 'Type', 'block_diagram');\n mcp__m = mcp__m(cellfun(@(x) strcmp(get_param(x, 'BlockDiagramType'), 'model') || ~startsWith(get_param(x, 'FileName'), matlabroot), mcp__m));\nelse, mcp__m = {}; end\n")
			} else {
				fmt.Fprintf(&b, "mcp__m = {%s};\n", q(resolveTarget(a.Model)))
			}
			fmt.Fprintf(&b, `for mcp__i = 1:numel(mcp__m)
  if ~bdIsLoaded(mcp__m{mcp__i}), fprintf('%%s is not loaded\n', mcp__m{mcp__i}); continue; end
  try, if ~any(strcmp(get_param(mcp__m{mcp__i}, 'SimulationStatus'), {'stopped', 'compiled'})), set_param(mcp__m{mcp__i}, 'SimulationCommand', 'stop'); end, catch, end
  if %s && strcmp(get_param(mcp__m{mcp__i}, 'Dirty'), 'on') && ~strcmp(get_param(mcp__m{mcp__i}, 'Lock'), 'on'), save_system(mcp__m{mcp__i}); end
  close_system(mcp__m{mcp__i}, 0); fprintf('Closed %%s\n', mcp__m{mcp__i});
end
if isempty(mcp__m), disp('No models loaded.'); end
`, boolStr(a.Save))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_get_model_structure

type StructureArgs struct {
	System     string `json:"system"                 jsonschema:"Model name, model file path, or subsystem path to inspect."`
	Depth      int    `json:"depth,omitempty"        jsonschema:"(Optional) Search depth into subsystems. Default 1 (top level only); use a large number for everything."`
	WithParams bool   `json:"with_params,omitempty"  jsonschema:"(Optional) Include the dialog parameter values of every block."`
}

func newStructureTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_get_model_structure",
		"Get Simulink Model Structure",
		"Describe a model or subsystem: configuration (solver, stop time), every block (path, type, library source, position, ports) and all connections, including Simscape physical connections. Use it to inspect an existing model before modifying it.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a StructureArgs) (tools.RichContent, error) {
			depth := a.Depth
			if depth <= 0 {
				depth = 1
			}
			var b strings.Builder
			b.WriteString(loadModelCode(a.System))
			fmt.Fprintf(&b, "mcp__sys = %s;\n", q(resolveTarget(a.System)))
			fmt.Fprintf(&b, `mcp__out = struct('system', mcp__sys);
if strcmp(get_param(mcp__sys, 'Type'), 'block_diagram') && strcmp(get_param(mcp__sys, 'BlockDiagramType'), 'model')
  mcp__out.config = struct('stop_time', get_param(mcp__sys, 'StopTime'), 'solver', get_param(mcp__sys, 'Solver'), 'solver_type', get_param(mcp__sys, 'SolverType'), ...
    'fixed_step', get_param(mcp__sys, 'FixedStep'), 'max_step', get_param(mcp__sys, 'MaxStep'), 'rel_tol', get_param(mcp__sys, 'RelTol'), 'file', get_param(mcp__sys, 'FileName'), 'dirty', get_param(mcp__sys, 'Dirty'));
end
mcp__all = find_system(mcp__sys, 'SearchDepth', %d, 'LookUnderMasks', 'none', 'FollowLinks', 'off', 'Type', 'block');
mcp__all = mcp__all(~strcmp(mcp__all, mcp__sys));
mcp__pref = [getfullname(mcp__sys) '/'];
mcp__rel = @(p) strrep(p, mcp__pref, '');
mcp__out.blocks = {};
for mcp__i = 1:numel(mcp__all)
  mcp__b = mcp__all{mcp__i};
  mcp__e = struct('path', mcp__rel(mcp__b), 'block_type', get_param(mcp__b, 'BlockType'), 'mask_type', get_param(mcp__b, 'MaskType'), 'position', get_param(mcp__b, 'Position'));
  try, mcp__e.library = get_param(mcp__b, 'ReferenceBlock'); catch, end
  mcp__ph = get_param(mcp__b, 'PortHandles'); mcp__pf = fieldnames(mcp__ph); mcp__e.ports = struct();
  for mcp__j = 1:numel(mcp__pf), if ~isempty(mcp__ph.(mcp__pf{mcp__j})), mcp__e.ports.(mcp__pf{mcp__j}) = numel(mcp__ph.(mcp__pf{mcp__j})); end, end
  mcp__e.connections = {};
  mcp__pc = get_param(mcp__b, 'PortConnectivity');
  for mcp__j = 1:numel(mcp__pc)
    for mcp__k = 1:numel(mcp__pc(mcp__j).SrcBlock)
      if mcp__pc(mcp__j).SrcBlock(mcp__k) ~= -1
        mcp__e.connections{end+1} = sprintf('%%s <- %%s:%%d', mcp__pc(mcp__j).Type, mcp__rel(getfullname(mcp__pc(mcp__j).SrcBlock(mcp__k))), mcp__pc(mcp__j).SrcPort(min(mcp__k, end)) + 1);
      end
    end
    for mcp__k = 1:numel(mcp__pc(mcp__j).DstBlock)
      if numel(mcp__pc(mcp__j).DstPort) >= mcp__k, mcp__dp = mcp__pc(mcp__j).DstPort(mcp__k) + 1; else, mcp__dp = 0; end
      mcp__e.connections{end+1} = sprintf('%%s -> %%s:%%d', mcp__pc(mcp__j).Type, mcp__rel(getfullname(mcp__pc(mcp__j).DstBlock(mcp__k))), mcp__dp);
    end
  end
  if %s
    mcp__dpar = get_param(mcp__b, 'DialogParameters');
    if isstruct(mcp__dpar)
      mcp__fn = fieldnames(mcp__dpar); mcp__e.params = struct();
      for mcp__j = 1:numel(mcp__fn), try, mcp__v = get_param(mcp__b, mcp__fn{mcp__j}); if ischar(mcp__v), mcp__e.params.(mcp__fn{mcp__j}) = mcp__v; end, catch, end, end
    end
  end
  mcp__out.blocks{end+1} = mcp__e;
end
mcp__ln = find_system(mcp__sys, 'SearchDepth', 1, 'FindAll', 'on', 'Type', 'line');
mcp__out.line_count = numel(mcp__ln);
mcp__out.unconnected_ports = {};
try
  mcp__un = find_system(mcp__sys, 'SearchDepth', %d, 'FindAll', 'on', 'Type', 'port', 'Line', -1);
  for mcp__i = 1:numel(mcp__un)
    mcp__out.unconnected_ports{end+1} = sprintf('%%s:%%s%%d', mcp__rel(get_param(mcp__un(mcp__i), 'Parent')), get_param(mcp__un(mcp__i), 'PortType'), get_param(mcp__un(mcp__i), 'PortNumber'));
  end
catch
end
`, depth, boolStr(a.WithParams), depth)
			b.WriteString(printJSONCode("mcp__out"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}
