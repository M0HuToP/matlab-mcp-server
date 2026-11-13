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

type BuildBlock struct {
	Name          string            `json:"name"                     jsonschema:"Block name relative to the model. Use 'Sub/Gain' to place a block inside subsystem 'Sub' (the subsystem must appear earlier in the list)."`
	Source        string            `json:"source"                   jsonschema:"Library path of the block, from any library: e.g. 'simulink/Sources/Step', 'simulink/Continuous/Transfer Fcn', 'simulink/Sinks/Scope', 'simulink/Ports & Subsystems/Subsystem', 'built-in/Subsystem', 'fl_lib/Electrical/Electrical Elements/Resistor', 'fl_lib/Electrical/Electrical Sources/DC Voltage Source', 'nesl_utility/Solver Configuration', 'nesl_utility/PS-Simulink Converter', 'ee_lib/...', 'sm_lib/...'. Use simulink_search_blocks when unsure."`
	Position      []float64         `json:"position,omitempty"       jsonschema:"(Optional) [left top right bottom]. If omitted the block is placed on a grid; with layout='auto' the diagram is arranged afterwards."`
	Orientation   string            `json:"orientation,omitempty"    jsonschema:"(Optional) right, left, up or down."`
	Params        map[string]string `json:"params,omitempty"         jsonschema:"(Optional) Parameter name -> value strings, e.g. {\"Gain\":\"5\"}, {\"Numerator\":\"[1]\",\"Denominator\":\"[1 2 1]\"}, Simscape {\"R\":\"100\",\"R_unit\":\"Ohm\"}."`
	ClearContents bool              `json:"clear_contents,omitempty" jsonschema:"(Optional) For subsystems from the library: remove default contents (In1->Out1) so you can fill it yourself."`
}

type BuildModelArgs struct {
	Model         string            `json:"model"                     jsonschema:"Model name or full .slx file path."`
	Folder        string            `json:"folder,omitempty"          jsonschema:"(Optional) Folder for the model file. Default: folder from model path or current MATLAB folder."`
	Mode          string            `json:"mode,omitempty"            jsonschema:"(Optional) 'create' (default; fails if the model exists), 'overwrite' (replace an existing model), or 'append' (add blocks/lines to an existing model)."`
	Blocks        []BuildBlock      `json:"blocks,omitempty"          jsonschema:"Blocks to add, in order."`
	Connections   []Connection      `json:"connections,omitempty"     jsonschema:"Lines to draw. Endpoints are 'Block/port' relative to the model, e.g. {from:'Step/1', to:['Sum/1']}, {from:'Sub/Gain/1', to:['Sub/Out1/1']}, Simscape: {from:'Resistor/rconn1', to:['Capacitor/lconn1']}."`
	ModelParams   map[string]string `json:"model_params,omitempty"    jsonschema:"(Optional) Model configuration parameters, e.g. {\"StopTime\":\"10\",\"Solver\":\"ode23t\",\"SimscapeLogType\":\"all\"}."`
	Variables     map[string]string `json:"variables,omitempty"       jsonschema:"(Optional) Variables created in the model workspace (name -> MATLAB expression), usable in block parameters."`
	Layout        string            `json:"layout,omitempty"          jsonschema:"(Optional) 'auto' (default): automatically arrange the diagram; 'keep': keep given/grid positions."`
	UpdateDiagram bool              `json:"update_diagram,omitempty"  jsonschema:"(Optional) Compile the diagram (Ctrl+D) after building to validate connections, parameters and Simscape networks."`
	Save          *bool             `json:"save,omitempty"            jsonschema:"(Optional) Save the model file. Default true."`
	Open          bool              `json:"open,omitempty"            jsonschema:"(Optional) Open the model in the Simulink Editor."`
	Screenshot    bool              `json:"screenshot,omitempty"      jsonschema:"(Optional) Return an image of the resulting diagram."`
}

func newBuildModelTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_build_model",
		"Build Simulink Model",
		"Automatically build a complete Simulink / Simscape model in one call from a declarative description: blocks from ANY library (Simulink, Simscape Foundation/Electrical/Multibody/Driveline/Fluids, Stateflow, toolboxes, user libraries), "+
			"parameters, nested subsystems, signal lines and physical connections, model configuration, auto-layout, compile check and save. "+
			"Rules: every endpoint is 'Block/port'; signal ports are numbers (out of source, in of destination); Simscape physical ports are lconnN / rconnN (left/right side as drawn); "+
			"each Simscape network needs a 'nesl_utility/Solver Configuration' block and a reference block (e.g. 'fl_lib/Electrical/Electrical Elements/Electrical Reference', 'fl_lib/Mechanical/Rotational Elements/Mechanical Rotational Reference'); "+
			"connect Simulink signals to Simscape with 'nesl_utility/Simulink-PS Converter' and back with 'nesl_utility/PS-Simulink Converter'. "+
			"If a step fails the error names the block or connection; fix it and call again with mode='append' or mode='overwrite'.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a BuildModelArgs) (tools.RichContent, error) {
			code, err := buildModelCode(a)
			if err != nil {
				return tools.RichContent{}, err
			}
			return d.runMATLAB(ctx, logger, code)
		})
}

func buildModelCode(a BuildModelArgs) (string, error) {
	folder, name := splitModelFile(a.Model)
	if a.Folder != "" {
		folder = a.Folder
	}
	if name == "" {
		return "", errors.New("model must not be empty")
	}
	mode := strings.ToLower(a.Mode)
	if mode == "" {
		mode = "create"
	}

	var b strings.Builder
	b.WriteString("if ~is_simulink_loaded(), start_simulink; end\n")

	switch mode {
	case "create", "overwrite":
		b.WriteString(createModelCode(name, folder, "model", "", mode == "overwrite"))
		b.WriteString("if isfile(mcp__file), delete(mcp__file); end\n")
	case "append":
		fmt.Fprintf(&b, "mcp__mdl = %s;\n", q(name))
		if folder != "" {
			fmt.Fprintf(&b, "mcp__folder = %s;\n", q(folder))
		} else {
			b.WriteString("mcp__folder = '';\n")
		}
		b.WriteString(`if ~bdIsLoaded(mcp__mdl)
  if ~isempty(mcp__folder) && isfile(fullfile(mcp__folder, [mcp__mdl '.slx'])), load_system(fullfile(mcp__folder, [mcp__mdl '.slx'])); else, load_system(mcp__mdl); end
end
mcp__file = get_param(mcp__mdl, 'FileName');
if isempty(mcp__file), if isempty(mcp__folder), mcp__folder = pwd; end, mcp__file = fullfile(mcp__folder, [mcp__mdl '.slx']); end
`)
	default:
		return "", fmt.Errorf("mode must be create, overwrite or append, got %q", a.Mode)
	}

	b.WriteString("mcp__nb = 0; mcp__n = 0;\n")

	// Variables in model workspace.
	if len(a.Variables) > 0 {
		b.WriteString("mcp__ws = get_param(mcp__mdl, 'ModelWorkspace');\n")
		for _, k := range sortedKeys(a.Variables) {
			fmt.Fprintf(&b, "assignin(mcp__ws, %s, evalin('base', %s));\n", q(k), q(a.Variables[k]))
		}
	}

	// Model parameters first (e.g. solver), so that block parameters may rely on them.
	for _, k := range sortedKeys(a.ModelParams) {
		fmt.Fprintf(&b, "try, set_param(mcp__mdl, %s, %s); catch mcp__pe, error('mcp:param', 'Model parameter %%s: %%s', %s, mcp__pe.message); end\n", q(k), q(a.ModelParams[k]), q(k))
	}

	// Blocks.
	gridIndex := map[string]int{}
	parents := map[string]bool{}
	for _, blk := range a.Blocks {
		if blk.Name == "" || blk.Source == "" {
			return "", errors.New("every block needs name and source")
		}
		parent := ""
		if i := strings.LastIndex(blk.Name, "/"); i >= 0 {
			parent = blk.Name[:i]
		}
		parents[parent] = true

		pos := blk.Position
		if len(pos) != 4 {
			idx := gridIndex[parent]
			gridIndex[parent] = idx + 1
			col, row := float64(idx%5), float64(idx/5)
			left, top := 60+col*170, 60+row*120
			pos = []float64{left, top}
		}

		b.WriteString("try\n")
		b.WriteString(addBlockCode("mcp__mdl", blk.Name, blk.Source, pos, blk.Orientation, blk.Params, false, blk.ClearContents))
		b.WriteString("mcp__nb = mcp__nb + 1;\n")
		fmt.Fprintf(&b, "catch mcp__be\nerror('mcp:block', 'Block %%s (%%s): %%s', %s, %s, mcp__be.message);\nend\n", q(blk.Name), q(blk.Source))
	}

	// Connections.
	code, err := connectionsCode("mcp__mdl", a.Connections)
	if err != nil {
		return "", err
	}
	b.WriteString(code)

	// Layout.
	if !strings.EqualFold(a.Layout, "keep") {
		b.WriteString("try, Simulink.BlockDiagram.arrangeSystem(mcp__mdl); catch mcp__le, fprintf('Auto layout skipped: %s\\n', mcp__le.message); end\n")
		for p := range parents {
			if p == "" {
				continue
			}
			fmt.Fprintf(&b, "try, Simulink.BlockDiagram.arrangeSystem([mcp__mdl '/' %s]); catch, end\n", q(p))
		}
	}

	b.WriteString("fprintf('Model %s: %d block(s) added, %d line(s) added.\\n', mcp__mdl, mcp__nb, mcp__n);\n")

	if a.UpdateDiagram {
		b.WriteString(`try
  set_param(mcp__mdl, 'SimulationCommand', 'update'); disp('Diagram update (compile) succeeded.');
catch mcp__ue
  fprintf('Diagram update FAILED:\n%s\n', ` + reportExpr("mcp__ue") + `);
end
`)
	}

	if a.Save == nil || *a.Save {
		b.WriteString("save_system(mcp__mdl, mcp__file); fprintf('Saved to %s\\n', mcp__file);\n")
	}
	if a.Open {
		b.WriteString("open_system(mcp__mdl);\n")
	}
	b.WriteString(`mcp__un = {};
try
  mcp__up = find_system(mcp__mdl, 'FindAll', 'on', 'LookUnderMasks', 'none', 'FollowLinks', 'off', 'Type', 'port', 'Line', -1);
  for mcp__i = 1:numel(mcp__up), mcp__un{end+1} = sprintf('%s:%s%d', strrep(get_param(mcp__up(mcp__i), 'Parent'), [mcp__mdl '/'], ''), get_param(mcp__up(mcp__i), 'PortType'), get_param(mcp__up(mcp__i), 'PortNumber')); end %#ok<AGROW>
catch
end
if ~isempty(mcp__un), fprintf('Unconnected ports: %s\n', strjoin(mcp__un, ', ')); end
`)
	if a.Screenshot {
		b.WriteString(screenshotCode("mcp__mdl"))
	}
	return b.String(), nil
}
