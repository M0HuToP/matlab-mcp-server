// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import (
	"fmt"
	"strings"
)

// libraryListCode leaves a cell array of all Simulink block libraries
// installed (Simulink, Simscape and every other toolbox / add-on that
// registers a library through slblocks.m) in mcp__libs. Cached in root appdata.
func libraryListCode(refresh bool) string {
	return fmt.Sprintf(`if ~is_simulink_loaded(), start_simulink; end
mcp__libs = getappdata(0, 'mcp_sl_libs'); mcp__libnames = getappdata(0, 'mcp_sl_libnames');
%% The list is rebuilt whenever the set of slblocks.m files on the path changes
%% (toolbox installed/removed, user library folder added with addpath).
mcp__files = which('-all', 'slblocks');
if %s || isempty(mcp__libs) || numel(mcp__libnames) ~= numel(mcp__libs) || ~isequal(getappdata(0, 'mcp_sl_libfiles'), mcp__files)
  setappdata(0, 'mcp_sl_libfiles', mcp__files);
  mcp__libs = {'simulink'}; mcp__libnames = {'Simulink'};
  mcp__old = pwd;
  for mcp__i = 1:numel(mcp__files)
    mcp__d = fileparts(mcp__files{mcp__i});
    if isempty(mcp__d) || ~isfolder(mcp__d), continue; end
    try
      cd(mcp__d); mcp__s = slblocks(); cd(mcp__old);
    catch
      cd(mcp__old); continue;
    end
    if isfield(mcp__s, 'Browser')
      for mcp__j = 1:numel(mcp__s.Browser)
        if isfield(mcp__s.Browser(mcp__j), 'Library')
          mcp__libs{end+1} = char(mcp__s.Browser(mcp__j).Library); %%#ok<AGROW>
          if isfield(mcp__s.Browser(mcp__j), 'Name'), mcp__libnames{end+1} = char(mcp__s.Browser(mcp__j).Name); else, mcp__libnames{end+1} = ''; end %%#ok<AGROW>
        end
      end
    elseif isfield(mcp__s, 'Library')
      mcp__libs{end+1} = char(mcp__s.Library); %%#ok<AGROW>
      if isfield(mcp__s, 'Name'), mcp__libnames{end+1} = char(mcp__s.Name); else, mcp__libnames{end+1} = ''; end %%#ok<AGROW>
    end
  end
  %% Simscape and other libraries that are reachable only through sub-library links.
  mcp__cand = {'simscape', 'fl_lib', 'nesl_utility', 'ee_lib', 'sm_lib', 'sdl_lib', 'batt_lib', 'SimscapeFluids_lib', 'sh_lib', 'elec_lib', 'sps_lib', 'powerlib', ...
    'aerolib', 'dspblks', 'commlib', 'simrflib', 'sflib', 'simevents'};
  mcp__candnames = {'Simscape', 'Simscape Foundation Library', 'Simscape Utilities', 'Simscape Electrical', 'Simscape Multibody', 'Simscape Driveline', 'Simscape Battery', 'Simscape Fluids', 'Simscape Fluids (hydraulics)', 'Simscape Electrical (legacy)', 'Simscape Electrical Specialized Power Systems', 'Simscape Electrical Specialized Power Systems (powerlib)', ...
    'Aerospace Blockset (legacy)', 'DSP System Toolbox (legacy)', 'Communications Toolbox (legacy)', 'RF Blockset', 'Stateflow', 'SimEvents'};
  mcp__libs = [mcp__libs, mcp__cand]; mcp__libnames = [mcp__libnames, mcp__candnames];
  [mcp__libs, mcp__ui] = unique(mcp__libs, 'stable'); mcp__libnames = mcp__libnames(mcp__ui);
  mcp__keep = false(size(mcp__libs));
  for mcp__i = 1:numel(mcp__libs)
    mcp__keep(mcp__i) = ischar(mcp__libs{mcp__i}) && ~isempty(regexp(mcp__libs{mcp__i}, '^\w+$', 'once')) && exist(mcp__libs{mcp__i}, 'file') == 4;
  end
  mcp__libs = mcp__libs(mcp__keep); mcp__libnames = mcp__libnames(mcp__keep);
  setappdata(0, 'mcp_sl_libs', mcp__libs); setappdata(0, 'mcp_sl_libnames', mcp__libnames);
end
`, boolStr(refresh))
}

// resolveSourceCode leaves the resolved library block path in mcp__src. It
// tolerates spaces instead of line breaks in block names and case differences.
func resolveSourceCode(source string) string {
	return fmt.Sprintf(`mcp__src = %s;
if ~strncmp(mcp__src, 'built-in/', 9)
  mcp__root = strtok(mcp__src, '/');
  if ~bdIsLoaded(mcp__root), load_system(mcp__root); end
  if getSimulinkBlockHandle(mcp__src) == -1
    mcp__cands = find_system(mcp__root, 'LookUnderMasks', 'graphical', 'FollowLinks', 'on', 'Type', 'block');
    mcp__norm = regexprep(mcp__cands, '\s+', ' ');
    mcp__hit = find(strcmpi(mcp__norm, regexprep(mcp__src, '\s+', ' ')), 1);
    if isempty(mcp__hit)
      mcp__leaf = regexprep(mcp__src, '^.*/', '');
      mcp__hit = find(strcmpi(regexprep(mcp__norm, '^.*/', ''), regexprep(mcp__leaf, '\s+', ' ')), 1);
    end
    if isempty(mcp__hit)
      error('mcp:source', 'Library block not found: %%s. Use simulink_search_blocks to find the exact path.', mcp__src);
    end
    mcp__src = mcp__cands{mcp__hit};
  end
end
`, q(source))
}

// addBlockCode emits code adding one block. sysExpr is a MATLAB expression
// for the parent system; name is relative to it. Leaves the handle in mcp__bh.
func addBlockCode(sysExpr, name, source string, position []float64, orientation string, params map[string]string, makeUnique bool, clearContents bool) string {
	var b strings.Builder
	b.WriteString(resolveSourceCode(source))
	fmt.Fprintf(&b, "mcp__dst = [%s '/' %s];\n", sysExpr, q(name))
	extra := ""
	if makeUnique {
		extra += ", 'MakeNameUnique', 'on'"
	}
	if len(position) == 4 {
		extra += ", 'Position', " + numVec(position)
	}
	fmt.Fprintf(&b, "mcp__bh = add_block(mcp__src, mcp__dst%s);\n", extra)
	if len(position) == 2 {
		// Move to the given origin while keeping the block's default size.
		x, y := numVec(position[:1]), numVec(position[1:])
		x, y = x[1:len(x)-1], y[1:len(y)-1]
		fmt.Fprintf(&b, "mcp__p = get_param(mcp__bh, 'Position'); set_param(mcp__bh, 'Position', [%s %s %s+(mcp__p(3)-mcp__p(1)) %s+(mcp__p(4)-mcp__p(2))]);\n", x, y, x, y)
	}
	if clearContents {
		b.WriteString("try, Simulink.SubSystem.deleteContents(mcp__bh); catch, end\n")
	}
	if orientation != "" {
		fmt.Fprintf(&b, "set_param(mcp__bh, 'Orientation', %s);\n", q(orientation))
	}
	for _, k := range sortedKeys(params) {
		fmt.Fprintf(&b, "try, set_param(mcp__bh, %s, %s); catch mcp__pe, error('mcp:param', 'Parameter %%s of block %%s: %%s', %s, getfullname(mcp__bh), mcp__pe.message); end\n", q(k), q(params[k]), q(k))
	}
	return b.String()
}

// blockInfoCode builds a struct describing block path expression blkExpr into mcp__info.
func blockInfoCode(blkExpr string, includeValues bool) string {
	return fmt.Sprintf(`mcp__blk = %s;
mcp__info = struct();
mcp__info.path = getfullname(mcp__blk);
mcp__info.name = get_param(mcp__blk, 'Name');
mcp__info.block_type = get_param(mcp__blk, 'BlockType');
mcp__info.mask_type = get_param(mcp__blk, 'MaskType');
try, mcp__info.reference_block = get_param(mcp__blk, 'ReferenceBlock'); catch, mcp__info.reference_block = ''; end
try, mcp__info.link_status = get_param(mcp__blk, 'LinkStatus'); catch, end
mcp__info.description = '';
try, mcp__info.description = get_param(mcp__blk, 'MaskDescription'); catch, end
if ~isempty(regexp(mcp__info.description, '^\w+(:\w+){2,}$', 'once')), try, mcp__info.description = getString(message(mcp__info.description)); catch, end, end
if isempty(mcp__info.description), try, mcp__info.description = get_param(mcp__blk, 'Description'); catch, end, end
try, mcp__info.simscape_component = get_param(mcp__blk, 'ComponentPath'); catch, end
mcp__info.position = get_param(mcp__blk, 'Position');
mcp__ph = get_param(mcp__blk, 'PortHandles');
mcp__pf = fieldnames(mcp__ph);
mcp__info.ports = struct();
for mcp__i = 1:numel(mcp__pf)
  if ~isempty(mcp__ph.(mcp__pf{mcp__i})), mcp__info.ports.(mcp__pf{mcp__i}) = numel(mcp__ph.(mcp__pf{mcp__i})); end
end
try
  mcp__ci = physmod.schema.internal.blockComponentSchema(mcp__blk).info();
  mcp__sp = struct();
  for mcp__kind = {'ConnectionPorts', 'Inputs', 'Outputs'}
    if isfield(mcp__ci.Members, mcp__kind{1}) && ~isempty(mcp__ci.Members.(mcp__kind{1}))
      mcp__m = mcp__ci.Members.(mcp__kind{1}); mcp__l = {};
      for mcp__j = 1:numel(mcp__m)
        mcp__e = struct();
        for mcp__fld = {'ID', 'Label', 'Descriptor', 'Location', 'Side', 'Domain'}
          if isfield(mcp__m(mcp__j), mcp__fld{1}), mcp__v = mcp__m(mcp__j).(mcp__fld{1}); if ischar(mcp__v) || isstring(mcp__v) || isnumeric(mcp__v), mcp__e.(lower(mcp__fld{1})) = mcp__v; end, end
        end
        mcp__l{end+1} = mcp__e; %%#ok<AGROW>
      end
      mcp__sp.(mcp__kind{1}) = mcp__l;
    end
  end
  mcp__info.simscape_ports = mcp__sp;
  %% Expected LConn/RConn mapping: left/top ports -> LConn, right/bottom -> RConn,
  %% in the order inputs, outputs, conserving ports. Only reported when the
  %% counts agree with the block's actual port handles.
  mcp__L = {}; mcp__R = {};
  for mcp__kind = {'Inputs', 'Outputs', 'ConnectionPorts'}
    if ~isfield(mcp__sp, mcp__kind{1}), continue; end
    for mcp__j = 1:numel(mcp__sp.(mcp__kind{1}))
      mcp__e = mcp__sp.(mcp__kind{1}){mcp__j};
      if isfield(mcp__e, 'label') && ~isempty(mcp__e.label), mcp__lab = char(mcp__e.label); elseif isfield(mcp__e, 'id'), mcp__lab = char(mcp__e.id); else, mcp__lab = '?'; end
      if isfield(mcp__e, 'side'), mcp__side = char(mcp__e.side); elseif strcmp(mcp__kind{1}, 'Outputs'), mcp__side = 'right'; else, mcp__side = 'left'; end
      if any(strcmp(mcp__side, {'left', 'top'})), mcp__L{end+1} = mcp__lab; else, mcp__R{end+1} = mcp__lab; end %%#ok<AGROW>
    end
  end
  mcp__ph0 = get_param(mcp__blk, 'PortHandles');
  if numel(mcp__L) == numel(mcp__ph0.LConn) && numel(mcp__R) == numel(mcp__ph0.RConn) && ~isempty([mcp__L mcp__R])
    mcp__map = struct();
    for mcp__j = 1:numel(mcp__L), mcp__map.(sprintf('lconn%%d', mcp__j)) = mcp__L{mcp__j}; end
    for mcp__j = 1:numel(mcp__R), mcp__map.(sprintf('rconn%%d', mcp__j)) = mcp__R{mcp__j}; end
    mcp__info.simscape_port_map = mcp__map;
  end
catch
end
mcp__info.port_hint ='Connect with BlockName/<port>: 1..N or inN/outN for signal ports; lconnN/rconnN for Simscape physical ports; enable, trigger, ifaction, reset, state.';
mcp__info.parameters = {};
mcp__dp = get_param(mcp__blk, 'DialogParameters');
if isstruct(mcp__dp)
  mcp__fn = fieldnames(mcp__dp);
  for mcp__i = 1:numel(mcp__fn)
    mcp__e = struct('name', mcp__fn{mcp__i}, 'prompt', mcp__dp.(mcp__fn{mcp__i}).Prompt, 'type', mcp__dp.(mcp__fn{mcp__i}).Type);
    if %s, try, mcp__val = get_param(mcp__blk, mcp__fn{mcp__i}); if ischar(mcp__val) || isstring(mcp__val), mcp__e.value = char(mcp__val); end, catch, end, end
    if isfield(mcp__dp.(mcp__fn{mcp__i}), 'Enum') && ~isempty(mcp__dp.(mcp__fn{mcp__i}).Enum), mcp__e.options = mcp__dp.(mcp__fn{mcp__i}).Enum; end
    mcp__info.parameters{end+1} = mcp__e; %%#ok<AGROW>
  end
end
`, blkExpr, boolStr(includeValues))
}
