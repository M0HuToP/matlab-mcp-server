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

// ---------------------------------------------------------------- simulink_list_libraries

type ListLibrariesArgs struct {
	Refresh bool `json:"refresh,omitempty" jsonschema:"(Optional) Rescan installed toolboxes instead of using the cached list."`
}

func newListLibrariesTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_list_libraries",
		"List Simulink Libraries",
		"List every installed Simulink block library: Simulink, Simscape (fl_lib Foundation, nesl_utility, ee_lib Electrical, sm_lib Multibody, sdl_lib Driveline, Fluids, ...), and every other toolbox or add-on library registered in the Library Browser.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a ListLibrariesArgs) (tools.RichContent, error) {
			body := libraryListCode(a.Refresh) + `mcp__r = cell(1, numel(mcp__libs));
for mcp__i = 1:numel(mcp__libs)
  mcp__r{mcp__i} = struct('library', mcp__libs{mcp__i}, 'name', mcp__libnames{mcp__i}, 'loaded', bdIsLoaded(mcp__libs{mcp__i}));
end
fprintf('%d libraries\n', numel(mcp__r));
` + printJSONCode("mcp__r")
			return d.runMATLAB(ctx, logger, body)
		})
}

// ---------------------------------------------------------------- simulink_search_blocks

type SearchBlocksArgs struct {
	Query      string   `json:"query"                 jsonschema:"Case-insensitive regular expression matched against block names and mask types, e.g. 'Gain', 'PID', 'Resistor', 'DC Motor', 'Solver Configuration', 'Revolute'."`
	Libraries  []string `json:"libraries,omitempty"   jsonschema:"(Optional) Libraries to search (e.g. ['simulink'], ['fl_lib','ee_lib']). Default: all installed libraries."`
	MaxResults int      `json:"max_results,omitempty" jsonschema:"(Optional) Maximum number of results. Default 40."`
	Refresh    bool     `json:"refresh,omitempty"     jsonschema:"(Optional) Force a full rebuild of the block index. Normally not needed: new, removed and changed libraries are detected automatically (file dates) and only they are re-indexed."`
}

func newSearchBlocksTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_search_blocks",
		"Search Simulink Library Blocks",
		"Find blocks in ANY installed Simulink library (Simulink, Simscape and all its add-on libraries, Stateflow, DSP, Communications, Aerospace, user libraries, ...) by name. "+
			"Returns the exact library path to pass as 'source' to simulink_add_block / simulink_build_model. Note: some block names contain line breaks, returned as \\n; keep them (or use spaces, which are also accepted).",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a SearchBlocksArgs) (tools.RichContent, error) {
			if strings.TrimSpace(a.Query) == "" {
				return tools.RichContent{}, errors.New("query must not be empty")
			}
			maxResults := a.MaxResults
			if maxResults <= 0 {
				maxResults = 40
			}
			var b strings.Builder
			custom := len(a.Libraries) > 0
			if custom {
				b.WriteString("if ~is_simulink_loaded(), start_simulink; end\n")
				fmt.Fprintf(&b, "mcp__libs = %s;\n", cellStr(a.Libraries))
			} else {
				b.WriteString(libraryListCode(a.Refresh))
			}
			// Persistent, incrementally updated index (see blockIndexCode); a
			// custom library list is indexed on the fly and not persisted.
			b.WriteString(blockIndexCode(a.Refresh, !custom))
			fmt.Fprintf(&b, "mcp__pat = %s; mcp__max = %d;\n", q(a.Query), maxResults)
			b.WriteString(`mcp__hit = find(~cellfun(@isempty, regexpi(mcp__idx.name, mcp__pat, 'once')) | ~cellfun(@isempty, regexpi(mcp__idx.mask, mcp__pat, 'once')));
fprintf('%d match(es) among %d library blocks', numel(mcp__hit), numel(mcp__idx.path));
if numel(mcp__hit) > mcp__max, fprintf(' (showing first %d)', mcp__max); mcp__hit = mcp__hit(1:mcp__max); end
fprintf('\n');
mcp__res = cell(1, numel(mcp__hit));
for mcp__k = 1:numel(mcp__hit)
  mcp__i = mcp__hit(mcp__k); mcp__b = mcp__idx.path{mcp__i};
  mcp__isLib = mcp__idx.islib(mcp__i);
  mcp__res{mcp__k} = struct('source', mcp__b, 'library', mcp__idx.lib{mcp__i}, 'block_type', mcp__idx.btype{mcp__i}, 'mask_type', mcp__idx.mask{mcp__i}, 'is_sublibrary', mcp__isLib);
end
`)
			b.WriteString(printJSONCode("mcp__res"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_browse_library

type BrowseLibraryArgs struct {
	Path  string `json:"path"            jsonschema:"Library or sub-library path, e.g. 'simulink', 'simulink/Continuous', 'fl_lib/Electrical/Electrical Elements', 'ee_lib/Electromechanical'."`
	Depth int    `json:"depth,omitempty" jsonschema:"(Optional) How many levels to list. Default 1."`
}

func newBrowseLibraryTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_browse_library",
		"Browse Simulink Library",
		"List the sub-libraries and blocks contained in a library or sub-library path (like expanding a node in the Library Browser).",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a BrowseLibraryArgs) (tools.RichContent, error) {
			depth := a.Depth
			if depth <= 0 {
				depth = 1
			}
			var b strings.Builder
			b.WriteString("if ~is_simulink_loaded(), start_simulink; end\n")
			b.WriteString(loadModelCode(a.Path))
			fmt.Fprintf(&b, "mcp__p = %s;\n", q(a.Path))
			fmt.Fprintf(&b, `if getSimulinkBlockHandle(mcp__p) == -1 && ~bdIsLoaded(mcp__p), error('mcp:path', 'Library path not found: %%s', mcp__p); end
mcp__bl = find_system(mcp__p, 'SearchDepth', %d, 'LookUnderMasks', 'graphical', 'FollowLinks', 'on', 'Type', 'block');
mcp__bl = mcp__bl(~strcmp(mcp__bl, mcp__p));
mcp__r = {};
for mcp__i = 1:numel(mcp__bl)
  mcp__b = mcp__bl{mcp__i};
  mcp__r{end+1} = struct('source', mcp__b, 'block_type', get_param(mcp__b, 'BlockType'), 'mask_type', get_param(mcp__b, 'MaskType')); %%#ok<AGROW>
end
fprintf('%%d item(s)\n', numel(mcp__r));
`, depth)
			b.WriteString(printJSONCode("mcp__r"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_get_block_info

type BlockInfoArgs struct {
	Block string `json:"block" jsonschema:"Full path of a library block (e.g. 'simulink/Math Operations/Gain', 'fl_lib/Electrical/Electrical Elements/Resistor') or of a block in a model (e.g. 'mymodel/Gain1')."`
}

func newBlockInfoTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_get_block_info",
		"Get Simulink Block Info",
		"Describe a library or model block: type, mask type, description, number of each port kind (signal Inport/Outport, Simscape LConn/RConn, Enable, Trigger, ...) and all dialog parameters with prompts, current values and allowed options. Call this before setting parameters or wiring a block you have not used yet.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a BlockInfoArgs) (tools.RichContent, error) {
			var b strings.Builder
			b.WriteString("if ~is_simulink_loaded(), start_simulink; end\n")
			b.WriteString(resolveSourceCode(a.Block))
			b.WriteString(blockInfoCode("mcp__src", true))
			b.WriteString(printJSONCode("mcp__info"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_add_block

type AddBlockArgs struct {
	System        string            `json:"system"                 jsonschema:"Model name or subsystem path that receives the block, e.g. 'mymodel' or 'mymodel/Controller'. The model must be loaded (create/open it first)."`
	Source        string            `json:"source"                 jsonschema:"Library path of the block, e.g. 'simulink/Sources/Step', 'simulink/Math Operations/Gain', 'fl_lib/Electrical/Electrical Elements/Resistor', 'nesl_utility/Solver Configuration', or 'built-in/Subsystem'."`
	Name          string            `json:"name"                   jsonschema:"Name of the new block in the system, e.g. 'Gain1'."`
	Position      []float64         `json:"position,omitempty"     jsonschema:"(Optional) [left top right bottom] in pixels."`
	Orientation   string            `json:"orientation,omitempty"  jsonschema:"(Optional) right (default), left, up or down."`
	Params        map[string]string `json:"params,omitempty"       jsonschema:"(Optional) Block parameters as name -> value strings, e.g. {\"Gain\":\"2.5\"} or for Simscape {\"R\":\"10\",\"R_unit\":\"Ohm\"}. Use simulink_get_block_info to see parameter names."`
	MakeUnique    bool              `json:"make_unique,omitempty"  jsonschema:"(Optional) Rename automatically if a block with that name already exists."`
	ClearContents bool              `json:"clear_contents,omitempty" jsonschema:"(Optional) For subsystem blocks: delete the default contents (e.g. In1->Out1) after adding."`
}

func newAddBlockTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_add_block",
		"Add Simulink Block",
		"Add one block from any library (Simulink, Simscape, any toolbox) to a model or subsystem and set its parameters. For building a whole model at once prefer simulink_build_model.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a AddBlockArgs) (tools.RichContent, error) {
			if a.System == "" || a.Source == "" || a.Name == "" {
				return tools.RichContent{}, errors.New("system, source and name are required")
			}
			var b strings.Builder
			b.WriteString(loadModelCode(a.System))
			fmt.Fprintf(&b, "mcp__sys = %s;\n", q(resolveTarget(a.System)))
			b.WriteString(addBlockCode("mcp__sys", a.Name, a.Source, a.Position, a.Orientation, a.Params, a.MakeUnique, a.ClearContents))
			b.WriteString("fprintf('Added %s (from %s)\\n', getfullname(mcp__bh), mcp__src);\n")
			b.WriteString(blockInfoCode("mcp__bh", false))
			b.WriteString("mcp__info = rmfield(mcp__info, 'parameters');\n")
			b.WriteString(printJSONCode("mcp__info"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_delete_blocks

type DeleteBlocksArgs struct {
	Blocks []string `json:"blocks" jsonschema:"Full paths of blocks to delete, e.g. ['mymodel/Gain1']. Connected lines are deleted too."`
}

func newDeleteBlocksTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_delete_blocks",
		"Delete Simulink Blocks",
		"Delete blocks (and the lines connected to them) from a model.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a DeleteBlocksArgs) (tools.RichContent, error) {
			if len(a.Blocks) == 0 {
				return tools.RichContent{}, errors.New("blocks must not be empty")
			}
			var b strings.Builder
			b.WriteString(loadModelCode(a.Blocks[0]))
			fmt.Fprintf(&b, "mcp__bl = %s;\n", cellStr(a.Blocks))
			b.WriteString(`for mcp__i = 1:numel(mcp__bl)
  mcp__h = getSimulinkBlockHandle(mcp__bl{mcp__i});
  if mcp__h == -1, fprintf('Not found: %s\n', mcp__bl{mcp__i}); continue; end
  mcp__lh = get_param(mcp__h, 'LineHandles'); mcp__lf = fieldnames(mcp__lh);
  for mcp__j = 1:numel(mcp__lf), mcp__l = mcp__lh.(mcp__lf{mcp__j}); mcp__l = mcp__l(mcp__l ~= -1); for mcp__k = 1:numel(mcp__l), try, delete_line(mcp__l(mcp__k)); catch, end, end, end
  delete_block(mcp__h); fprintf('Deleted %s\n', mcp__bl{mcp__i});
end
`)
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_connect

type Connection struct {
	From string   `json:"from" jsonschema:"Source endpoint 'Block/port', relative to system. Port: number or outN for signal outputs; lconnN / rconnN for Simscape physical ports (e.g. 'Step/1', 'Resistor/rconn1', 'Sub/Gain/1')."`
	To   []string `json:"to"   jsonschema:"One or more destination endpoints 'Block/port' (several = branched line). Port: number or inN for signal inputs; enable, trigger, ifaction, reset; lconnN / rconnN for physical ports."`
}

type ConnectArgs struct {
	System      string       `json:"system"      jsonschema:"Model name or subsystem path in which the blocks live."`
	Connections []Connection `json:"connections" jsonschema:"Connections to create."`
}

func newConnectTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_connect",
		"Connect Simulink Blocks",
		"Draw lines between block ports: Simulink signal lines (Outport -> Inport, control ports) and Simscape physical connections (LConn/RConn ports, non-directional). Lines are auto-routed.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a ConnectArgs) (tools.RichContent, error) {
			if len(a.Connections) == 0 {
				return tools.RichContent{}, errors.New("connections must not be empty")
			}
			var b strings.Builder
			b.WriteString(loadModelCode(a.System))
			fmt.Fprintf(&b, "mcp__sys = %s; mcp__n = 0;\n", q(resolveTarget(a.System)))
			code, err := connectionsCode("mcp__sys", a.Connections)
			if err != nil {
				return tools.RichContent{}, err
			}
			b.WriteString(code)
			b.WriteString("fprintf('%d line(s) added.\\n', mcp__n);\n")
			return d.runMATLAB(ctx, logger, b.String())
		})
}

func connectionsCode(sysExpr string, conns []Connection) (string, error) {
	var b strings.Builder
	for _, c := range conns {
		src, err := parseEndpoint(c.From, true)
		if err != nil {
			return "", err
		}
		if len(c.To) == 0 {
			return "", fmt.Errorf("connection from %q has no destination", c.From)
		}
		for _, to := range c.To {
			dst, err := parseEndpoint(to, false)
			if err != nil {
				return "", err
			}
			b.WriteString("try\n")
			b.WriteString(connectCode(sysExpr, src, dst))
			b.WriteString("mcp__n = mcp__n + 1;\n")
			fmt.Fprintf(&b, "catch mcp__ce\nerror('mcp:connect', 'Connection %%s -> %%s failed: %%s', %s, %s, mcp__ce.message);\nend\n", q(c.From), q(to))
		}
	}
	return b.String(), nil
}

// ---------------------------------------------------------------- simulink_delete_lines

type DeleteLinesArgs struct {
	System    string   `json:"system"    jsonschema:"Model name or subsystem path."`
	Endpoints []string `json:"endpoints" jsonschema:"Ports whose lines are deleted, as 'Block/port' (e.g. 'Gain/1' = output 1 of Gain, 'Gain/in1' = input 1, 'Resistor/lconn1'). Use 'all' to delete every line in the system."`
}

func newDeleteLinesTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_delete_lines",
		"Delete Simulink Lines",
		"Delete the lines (signal or physical) attached to given block ports, or all lines in a system.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a DeleteLinesArgs) (tools.RichContent, error) {
			var b strings.Builder
			b.WriteString(loadModelCode(a.System))
			fmt.Fprintf(&b, "mcp__sys = %s; mcp__n = 0;\n", q(resolveTarget(a.System)))
			for _, e := range a.Endpoints {
				if strings.EqualFold(e, "all") {
					b.WriteString("mcp__l = find_system(mcp__sys, 'SearchDepth', 1, 'FindAll', 'on', 'Type', 'line'); for mcp__k = 1:numel(mcp__l), try, delete_line(mcp__l(mcp__k)); mcp__n = mcp__n + 1; catch, end, end\n")
					continue
				}
				ep, err := parseEndpoint(e, true)
				if err != nil {
					return tools.RichContent{}, err
				}
				fmt.Fprintf(&b, "mcp__ph = get_param([mcp__sys '/' %s], 'PortHandles'); mcp__l = get_param(mcp__ph.%s(%d), 'Line'); if mcp__l ~= -1, delete_line(mcp__l); mcp__n = mcp__n + 1; end\n", q(ep.Block), ep.Port.Field, ep.Port.Index)
			}
			b.WriteString("fprintf('%d line(s) deleted.\\n', mcp__n);\n")
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_set_params

type SetParamsArgs struct {
	Target string            `json:"target" jsonschema:"Block path (e.g. 'mymodel/Gain1'), subsystem path, or model name (for configuration parameters such as StopTime, Solver, FixedStep, SimscapeLogType)."`
	Params map[string]string `json:"params" jsonschema:"Parameter name -> value (always strings, e.g. {\"Gain\":\"K*2\"}, {\"StopTime\":\"20\",\"Solver\":\"ode23t\"})."`
}

func newSetParamsTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_set_params",
		"Set Simulink Parameters",
		"Set parameters of a block, subsystem mask, or model configuration (set_param). Values are MATLAB expressions as strings and may reference workspace variables.",
		annotations.NewIdempotentWriteAnnotations(),
		func(ctx context.Context, logger entities.Logger, a SetParamsArgs) (tools.RichContent, error) {
			if len(a.Params) == 0 {
				return tools.RichContent{}, errors.New("params must not be empty")
			}
			var b strings.Builder
			b.WriteString(loadModelCode(a.Target))
			fmt.Fprintf(&b, "mcp__t = %s;\n", q(resolveTarget(a.Target)))
			for _, k := range sortedKeys(a.Params) {
				fmt.Fprintf(&b, "set_param(mcp__t, %s, %s); fprintf('%%s = %%s\\n', %s, get_param(mcp__t, %s));\n", q(k), q(a.Params[k]), q(k), q(k))
			}
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_get_params

type GetParamsArgs struct {
	Target string   `json:"target"          jsonschema:"Block path, subsystem path or model name."`
	Names  []string `json:"names,omitempty" jsonschema:"(Optional) Parameter names. Default: all dialog parameters of a block, or the main configuration parameters of a model."`
}

func newGetParamsTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_get_params",
		"Get Simulink Parameters",
		"Read parameters of a block or model (get_param).",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a GetParamsArgs) (tools.RichContent, error) {
			var b strings.Builder
			b.WriteString(loadModelCode(a.Target))
			fmt.Fprintf(&b, "mcp__t = %s;\n", q(resolveTarget(a.Target)))
			if len(a.Names) > 0 {
				fmt.Fprintf(&b, "mcp__n = %s;\n", cellStr(a.Names))
			} else {
				b.WriteString(`if strcmp(get_param(mcp__t, 'Type'), 'block_diagram')
  mcp__n = {'StopTime', 'StartTime', 'Solver', 'SolverType', 'FixedStep', 'MaxStep', 'RelTol', 'AbsTol', 'SimulationMode', 'SaveOutput', 'SaveTime', 'SignalLogging', 'ReturnWorkspaceOutputs'};
  try, get_param(mcp__t, 'SimscapeLogType'); mcp__n{end+1} = 'SimscapeLogType'; catch, end
else
  mcp__dp = get_param(mcp__t, 'DialogParameters'); if isstruct(mcp__dp), mcp__n = fieldnames(mcp__dp)'; else, mcp__n = {}; end
end
`)
			}
			b.WriteString(`mcp__r = struct();
for mcp__i = 1:numel(mcp__n)
  try, mcp__v = get_param(mcp__t, mcp__n{mcp__i}); catch mcp__e, mcp__v = ['<error: ' mcp__e.message '>']; end
  if ~(ischar(mcp__v) || isstring(mcp__v) || isnumeric(mcp__v) || islogical(mcp__v)), mcp__v = ['<' class(mcp__v) '>']; end
  mcp__r.(matlab.lang.makeValidName(mcp__n{mcp__i})) = mcp__v;
end
`)
			b.WriteString(printJSONCode("mcp__r"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_arrange

type ArrangeArgs struct {
	System string `json:"system" jsonschema:"Model or subsystem path to auto-arrange."`
}

func newArrangeTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_arrange_layout",
		"Auto-Arrange Simulink Layout",
		"Automatically arrange blocks and lines of a model or subsystem for readability (Simulink.BlockDiagram.arrangeSystem).",
		annotations.NewIdempotentWriteAnnotations(),
		func(ctx context.Context, logger entities.Logger, a ArrangeArgs) (tools.RichContent, error) {
			var b strings.Builder
			b.WriteString(loadModelCode(a.System))
			fmt.Fprintf(&b, "Simulink.BlockDiagram.arrangeSystem(%s); disp('Layout arranged.');\n", q(resolveTarget(a.System)))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

// ---------------------------------------------------------------- simulink_screenshot

type ScreenshotArgs struct {
	System string `json:"system" jsonschema:"Model or subsystem path to render as an image."`
}

func newScreenshotTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_screenshot",
		"Render Simulink Diagram",
		"Render a model or subsystem diagram to a PNG image and return it, to visually check the constructed model.",
		annotations.NewReadOnlyAnnotations(),
		func(ctx context.Context, logger entities.Logger, a ScreenshotArgs) (tools.RichContent, error) {
			var b strings.Builder
			b.WriteString(loadModelCode(a.System))
			fmt.Fprintf(&b, "mcp__sys = %s;\n", q(resolveTarget(a.System)))
			b.WriteString(screenshotCode("mcp__sys"))
			return d.runMATLAB(ctx, logger, b.String())
		})
}

func screenshotCode(sysExpr string) string {
	return fmt.Sprintf(`mcp__png = [tempname '.png'];
try
  print(['-s' getfullname(%s)], '-dpng', '-r110', mcp__png);
catch
  saveas(get_param(%s, 'Handle'), mcp__png);
end
fprintf('%s%%s%s\n', mcp__png);
`, sysExpr, sysExpr, imagePrefix, imageSuffix)
}
