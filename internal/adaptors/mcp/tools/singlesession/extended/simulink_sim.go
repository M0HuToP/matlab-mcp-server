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

type BlockParamOverride struct {
	Block string `json:"block" jsonschema:"Block path relative to the model, e.g. 'Gain'."`
	Param string `json:"param" jsonschema:"Parameter name, e.g. 'Gain'."`
	Value string `json:"value" jsonschema:"Value string, e.g. '3'."`
}

type RunSimArgs struct {
	Model           string               `json:"model"                       jsonschema:"Model name or .slx file path."`
	StopTime        string               `json:"stop_time,omitempty"         jsonschema:"(Optional) Stop time override, e.g. '10'."`
	ModelParams     map[string]string    `json:"model_params,omitempty"      jsonschema:"(Optional) Configuration overrides for this run only, e.g. {\"Solver\":\"ode45\",\"MaxStep\":\"1e-3\"}."`
	Variables       map[string]string    `json:"variables,omitempty"         jsonschema:"(Optional) Variables for this run only: name -> MATLAB expression (evaluated in the base workspace), e.g. {\"K\":\"2.5\"}."`
	BlockParams     []BlockParamOverride `json:"block_params,omitempty"      jsonschema:"(Optional) Block parameter overrides for this run only."`
	SimscapeLog     bool                 `json:"simscape_log,omitempty"      jsonschema:"(Optional) Enable Simscape data logging (SimscapeLogType='all') for this run."`
	SimscapeSignals []string             `json:"simscape_signals,omitempty"  jsonschema:"(Optional) Simscape log nodes to extract, relative to the simlog, e.g. ['Resistor.i', 'Capacitor.vc', 'Motor.w']. Implies simscape_log."`
	OutputVariable  string               `json:"output_variable,omitempty"   jsonschema:"(Optional) Base-workspace variable receiving the Simulink.SimulationOutput. Default 'out'."`
	SaveMat         string               `json:"save_mat,omitempty"          jsonschema:"(Optional) Save the simulation output to this .mat file."`
	ReturnData      bool                 `json:"return_data,omitempty"       jsonschema:"(Optional) Return the (down-sampled) time series values as JSON."`
	MaxPoints       int                  `json:"max_points,omitempty"        jsonschema:"(Optional) Max samples per signal when return_data=true. Default 200."`
	Plot            bool                 `json:"plot,omitempty"              jsonschema:"(Optional) Return a PNG plot of all logged signals."`
	OpenScopes      bool                 `json:"open_scopes,omitempty"       jsonschema:"(Optional) Capture images of the model's Scope blocks after the run (model is opened)."`
}

func newRunSimTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_run_simulation",
		"Run Simulink Simulation",
		"Run a simulation of a Simulink / Simscape model (sim with Simulink.SimulationInput) and report: status, errors and warnings, elapsed time, and a summary (samples, min, max, final value) of every logged signal "+
			"(signal logging, To Workspace, Outports, Simscape log nodes). Optional run-specific overrides, data return, plots and .mat export. "+
			"To see signals, log them (mark signals for logging, use Outport / To Workspace blocks, or Simscape logging).",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a RunSimArgs) (tools.RichContent, error) {
			if strings.TrimSpace(a.Model) == "" {
				return tools.RichContent{}, errors.New("model must not be empty")
			}
			return d.runMATLAB(ctx, logger, runSimCode(a))
		})
}

func runSimCode(a RunSimArgs) string {
	outVar := a.OutputVariable
	if outVar == "" {
		outVar = "out"
	}
	maxPoints := a.MaxPoints
	if maxPoints <= 0 {
		maxPoints = 200
	}
	simscape := a.SimscapeLog || len(a.SimscapeSignals) > 0

	var b strings.Builder
	b.WriteString(loadModelCode(a.Model))
	fmt.Fprintf(&b, "mcp__mdl = %s;\n", q(resolveTarget(a.Model)))
	b.WriteString("mcp__in = Simulink.SimulationInput(mcp__mdl);\n")
	if a.StopTime != "" {
		fmt.Fprintf(&b, "mcp__in = setModelParameter(mcp__in, 'StopTime', %s);\n", q(a.StopTime))
	}
	for _, k := range sortedKeys(a.ModelParams) {
		fmt.Fprintf(&b, "mcp__in = setModelParameter(mcp__in, %s, %s);\n", q(k), q(a.ModelParams[k]))
	}
	if simscape {
		b.WriteString("mcp__in = setModelParameter(mcp__in, 'SimscapeLogType', 'all', 'SimscapeLogName', 'simlog');\n")
	}
	for _, k := range sortedKeys(a.Variables) {
		fmt.Fprintf(&b, "mcp__in = setVariable(mcp__in, %s, evalin('base', %s));\n", q(k), q(a.Variables[k]))
	}
	for _, bp := range a.BlockParams {
		fmt.Fprintf(&b, "mcp__in = setBlockParameter(mcp__in, [mcp__mdl '/' %s], %s, %s);\n", q(bp.Block), q(bp.Param), q(bp.Value))
	}

	fmt.Fprintf(&b, `mcp__t0 = tic;
mcp__simerr = [];
try
  mcp__out = sim(mcp__in, 'ShowProgress', 'off');
catch mcp__se
  mcp__simerr = mcp__se; mcp__out = [];
end
mcp__wall = toc(mcp__t0);
if ~isempty(mcp__simerr)
  error('mcp:sim', 'Simulation FAILED after %%.2f s:\n%%s', mcp__wall, `+reportExpr("mcp__simerr")+`);
end
assignin('base', %s, mcp__out);
if ~isempty(mcp__out.ErrorMessage)
  fprintf('Simulation FAILED after %%.2f s:\n%%s\n', mcp__wall, mcp__out.ErrorMessage);
else
  fprintf('Simulation of %%s completed in %%.2f s (wall clock). Output stored in base variable ''%s''.\n', mcp__mdl, mcp__wall);
end
try
  mcp__ei = mcp__out.SimulationMetadata.ExecutionInfo;
  fprintf('Stop event: %%s %%s\n', char(mcp__ei.StopEvent), char(mcp__ei.StopEventDescription));
  mcp__wd = mcp__ei.WarningDiagnostics;
  for mcp__i = 1:numel(mcp__wd), fprintf('WARNING: %%s\n', mcp__wd(mcp__i).Diagnostic.message); end
catch
end
`, q(outVar), strings.ReplaceAll(outVar, "'", ""))

	// Flatten all signals into mcp__sig = {label, time, data}.
	b.WriteString(`mcp__sig = cell(0, 3); mcp__notes = {};
mcp__names = mcp__out.who;
mcp__tout = [];
if any(strcmp(mcp__names, 'tout')), mcp__tout = mcp__out.get('tout'); end
for mcp__i = 1:numel(mcp__names)
  mcp__nm = mcp__names{mcp__i}; mcp__v = mcp__out.get(mcp__nm);
  if strcmp(mcp__nm, 'tout'), continue; end
  if isa(mcp__v, 'Simulink.SimulationData.Dataset')
    for mcp__k = 1:mcp__v.numElements
      mcp__el = mcp__v.getElement(mcp__k); mcp__en = char(mcp__el.Name);
      if isempty(mcp__en), try, mcp__en = regexprep(mcp__el.BlockPath.getBlock(1), '^[^/]*/', ''); catch, mcp__en = sprintf('element%d', mcp__k); end, end
      mcp__lbl = [mcp__nm '.' strrep(mcp__en, newline, ' ')];
      try, mcp__vals = mcp__el.Values; catch, mcp__vals = mcp__el; end
      if isa(mcp__vals, 'timeseries')
        mcp__sig(end+1, :) = {mcp__lbl, mcp__vals.Time, mcp__vals.Data};
      elseif isa(mcp__vals, 'timetable')
        mcp__sig(end+1, :) = {mcp__lbl, seconds(mcp__vals.Properties.RowTimes - mcp__vals.Properties.RowTimes(1)), mcp__vals.Variables};
      elseif isstruct(mcp__vals)
        mcp__fns = fieldnames(mcp__vals);
        for mcp__f = 1:numel(mcp__fns), if isa(mcp__vals.(mcp__fns{mcp__f}), 'timeseries'), mcp__sig(end+1, :) = {[mcp__lbl '.' mcp__fns{mcp__f}], mcp__vals.(mcp__fns{mcp__f}).Time, mcp__vals.(mcp__fns{mcp__f}).Data}; end, end
      else
        mcp__notes{end+1} = sprintf('%s: %s', mcp__lbl, class(mcp__vals));
      end
    end
  elseif isa(mcp__v, 'timeseries')
    mcp__sig(end+1, :) = {mcp__nm, mcp__v.Time, mcp__v.Data};
  elseif isstruct(mcp__v) && isfield(mcp__v, 'signals')
    for mcp__k = 1:numel(mcp__v.signals)
      mcp__t = []; if isfield(mcp__v, 'time'), mcp__t = mcp__v.time; end
      mcp__lbl = sprintf('%s.signals(%d)', mcp__nm, mcp__k);
      if isfield(mcp__v.signals(mcp__k), 'label') && ~isempty(mcp__v.signals(mcp__k).label), mcp__lbl = [mcp__lbl ' ' mcp__v.signals(mcp__k).label]; end
      mcp__sig(end+1, :) = {mcp__lbl, mcp__t, mcp__v.signals(mcp__k).values};
    end
  elseif isnumeric(mcp__v) && ~isempty(mcp__v)
    mcp__sig(end+1, :) = {mcp__nm, mcp__tout, mcp__v};
  elseif isa(mcp__v, 'simscape.logging.Node')
    mcp__notes{end+1} = sprintf('%s: Simscape log (query nodes with simscape_signals, e.g. ''Block.variable'')', mcp__nm);
  else
    mcp__notes{end+1} = sprintf('%s: %s', mcp__nm, class(mcp__v));
  end
end
`)
	if len(a.SimscapeSignals) > 0 {
		fmt.Fprintf(&b, `mcp__ssc = %s;
mcp__log = []; if any(strcmp(mcp__names, 'simlog')), mcp__log = mcp__out.get('simlog'); end
for mcp__i = 1:numel(mcp__ssc)
  try
    mcp__node = mcp__log;
    mcp__parts = strsplit(mcp__ssc{mcp__i}, '.');
    for mcp__k = 1:numel(mcp__parts), mcp__node = mcp__node.child(mcp__parts{mcp__k}); end
    mcp__ser = mcp__node.series;
    mcp__sig(end+1, :) = {['simlog.' mcp__ssc{mcp__i} ' [' char(mcp__ser.unit) ']'], mcp__ser.time, mcp__ser.values};
  catch mcp__le
    mcp__notes{end+1} = sprintf('simlog.%%s: %%s', mcp__ssc{mcp__i}, mcp__le.message);
  end
end
`, cellStr(a.SimscapeSignals))
	}

	b.WriteString(`if isempty(mcp__sig)
  disp('No logged signals found. Log signals (signal logging, Outport, To Workspace) or enable simscape_log.');
else
  fprintf('\n%-45s %8s %12s %12s %12s %12s\n', 'Signal', 'Samples', 't_end', 'Min', 'Max', 'Final');
  for mcp__i = 1:size(mcp__sig, 1)
    mcp__y = double(squeeze(mcp__sig{mcp__i, 3})); mcp__t = mcp__sig{mcp__i, 2};
    if isempty(mcp__y), continue; end
    if isvector(mcp__y), mcp__y = mcp__y(:); end
    if ~isempty(mcp__t) && size(mcp__y, 1) ~= numel(mcp__t) && size(mcp__y, 2) == numel(mcp__t), mcp__y = mcp__y.'; end
    mcp__y2 = reshape(mcp__y, size(mcp__y, 1), []);
    if isempty(mcp__t), mcp__te = NaN; else, mcp__te = mcp__t(end); end
    for mcp__c = 1:min(size(mcp__y2, 2), 6)
      mcp__lbl = mcp__sig{mcp__i, 1}; if size(mcp__y2, 2) > 1, mcp__lbl = sprintf('%s(:,%d)', mcp__lbl, mcp__c); end
      fprintf('%-45s %8d %12.5g %12.5g %12.5g %12.5g\n', mcp__lbl, size(mcp__y2, 1), mcp__te, min(mcp__y2(:, mcp__c)), max(mcp__y2(:, mcp__c)), mcp__y2(end, mcp__c));
    end
    if size(mcp__y2, 2) > 6, fprintf('  ... %d more column(s)\n', size(mcp__y2, 2) - 6); end
  end
end
if ~isempty(mcp__notes), fprintf('\nOther outputs:\n  %s\n', strjoin(mcp__notes, [newline '  '])); end
`)

	if a.ReturnData {
		fmt.Fprintf(&b, `mcp__data = {};
for mcp__i = 1:size(mcp__sig, 1)
  mcp__y = double(squeeze(mcp__sig{mcp__i, 3})); mcp__t = double(mcp__sig{mcp__i, 2}(:));
  if isvector(mcp__y), mcp__y = mcp__y(:); end
  if ~isempty(mcp__t) && size(mcp__y, 1) ~= numel(mcp__t) && size(mcp__y, 2) == numel(mcp__t), mcp__y = mcp__y.'; end
  mcp__y = reshape(mcp__y, size(mcp__y, 1), []);
  mcp__idx = unique(round(linspace(1, size(mcp__y, 1), min(%d, size(mcp__y, 1)))));
  mcp__e = struct('signal', mcp__sig{mcp__i, 1}, 'y', mcp__y(mcp__idx, :));
  if ~isempty(mcp__t), mcp__e.t = mcp__t(mcp__idx); end
  mcp__data{end+1} = mcp__e; %%#ok<AGROW>
end
disp(jsonencode(mcp__data));
`, maxPoints)
	}

	if a.Plot {
		b.WriteString(`if ~isempty(mcp__sig)
  mcp__n = min(size(mcp__sig, 1), 12);
  mcp__fig = figure('Visible', 'off', 'Position', [100 100 900 max(250, 200 * mcp__n)]);
  mcp__tl = tiledlayout(mcp__fig, mcp__n, 1, 'TileSpacing', 'compact');
  for mcp__i = 1:mcp__n
    mcp__ax = nexttile(mcp__tl);
    mcp__y = double(squeeze(mcp__sig{mcp__i, 3})); mcp__t = mcp__sig{mcp__i, 2};
    if isvector(mcp__y), mcp__y = mcp__y(:); end
    if ~isempty(mcp__t) && size(mcp__y, 1) ~= numel(mcp__t) && size(mcp__y, 2) == numel(mcp__t), mcp__y = mcp__y.'; end
    mcp__y = reshape(mcp__y, size(mcp__y, 1), []);
    if isempty(mcp__t) || numel(mcp__t) ~= size(mcp__y, 1), plot(mcp__ax, mcp__y); else, plot(mcp__ax, mcp__t, mcp__y); end
    title(mcp__ax, mcp__sig{mcp__i, 1}, 'Interpreter', 'none'); grid(mcp__ax, 'on');
  end
  xlabel(mcp__tl, 'Time (s)');
  mcp__png = [tempname '.png'];
  exportgraphics(mcp__fig, mcp__png, 'Resolution', 100); close(mcp__fig);
  fprintf('` + imagePrefix + `%s` + imageSuffix + `\n', mcp__png);
end
`)
	}

	if a.OpenScopes {
		b.WriteString(`mcp__sc = find_system(mcp__mdl, 'LookUnderMasks', 'all', 'BlockType', 'Scope');
for mcp__i = 1:numel(mcp__sc)
  try
    open_system(mcp__sc{mcp__i}); drawnow;
    mcp__cfg = get_param(mcp__sc{mcp__i}, 'ScopeConfiguration'); mcp__cfg.Visible = true; drawnow;
    mcp__sf = findall(groot, 'Type', 'figure', 'Name', get_param(mcp__sc{mcp__i}, 'Name'));
    if ~isempty(mcp__sf)
      mcp__png = [tempname '.png'];
      try, exportgraphics(mcp__sf(1), mcp__png); catch, print(mcp__sf(1), mcp__png, '-dpng'); end
      fprintf('` + imagePrefix + `%s` + imageSuffix + `\n', mcp__png);
    end
  catch mcp__oe
    fprintf('Could not capture scope %s: %s\n', mcp__sc{mcp__i}, mcp__oe.message);
  end
end
`)
	}

	if a.SaveMat != "" {
		fmt.Fprintf(&b, "simOut = mcp__out; save(%s, 'simOut'); clear simOut; fprintf('Saved simulation output to %%s\\n', %s);\n", q(a.SaveMat), q(a.SaveMat))
	}
	return b.String()
}

// ---------------------------------------------------------------- simulink_simulation_control

type SimControlArgs struct {
	Model   string `json:"model"   jsonschema:"Model name (must be loaded/open)."`
	Command string `json:"command" jsonschema:"'start' (run asynchronously, like the Run button), 'stop', 'pause', 'continue', 'step' (step forward while paused), 'update' (compile / update diagram), 'status' (report SimulationStatus and current time)."`
}

func newSimControlTool(d *deps) tools.Tool {
	return newTool(d,
		"simulink_simulation_control",
		"Control Simulink Simulation",
		"Interactively start, stop, pause, continue or step a simulation (as with the Simulink Editor toolbar), compile the diagram, or query the simulation status. Use 'start' for long or real-time simulations you want to stop later with 'stop'.",
		annotations.NewDestructiveAnnotations(),
		func(ctx context.Context, logger entities.Logger, a SimControlArgs) (tools.RichContent, error) {
			cmd := strings.ToLower(strings.TrimSpace(a.Command))
			var b strings.Builder
			b.WriteString(loadModelCode(a.Model))
			fmt.Fprintf(&b, "mcp__mdl = %s;\n", q(resolveTarget(a.Model)))
			switch cmd {
			case "start", "stop", "pause", "continue", "update":
				fmt.Fprintf(&b, "set_param(mcp__mdl, 'SimulationCommand', %s); pause(0.2);\n", q(cmd))
			case "step":
				b.WriteString("set_param(mcp__mdl, 'SimulationCommand', 'StepForward'); pause(0.2);\n")
			case "status":
			default:
				return tools.RichContent{}, fmt.Errorf("unsupported command %q", a.Command)
			}
			b.WriteString("mcp__st = get_param(mcp__mdl, 'SimulationStatus'); fprintf('Status: %s\\n', mcp__st);\n")
			b.WriteString("if any(strcmp(mcp__st, {'running', 'paused'})), try, fprintf('Simulation time: %g\\n', get_param(mcp__mdl, 'SimulationTime')); catch, end, end\n")
			return d.runMATLAB(ctx, logger, b.String())
		})
}
