// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import "fmt"

// blockIndexFormat is stored in the persisted index; bump it whenever the
// layout of the index changes so that old files are rebuilt, not misread.
const blockIndexFormat = 2

// blockIndexCode leaves a flattened block index of all libraries in mcp__libs
// in mcp__idx (fields path, lib, name, mask, btype, islib).
//
// When persist is true the index is kept per root library in root appdata and
// in prefdir/mcp_sl_block_index.mat, together with the file name and
// modification date of the root library and of every sub-library reached from
// it. Invalidation:
//   - index format or MATLAB version (incl. update/build number) differ -> full rebuild;
//   - a root library is no longer installed                              -> its entry is dropped;
//   - a new root library appears (toolbox/add-on installed)              -> only it is indexed;
//   - any file of an entry changed or disappeared (add-on updated, user
//     library edited)                                                    -> only that entry is rebuilt.
//
// File dates are checked on every search (a few hundred dir calls, well under
// a second), so saved edits of user libraries are picked up; refresh=true forces a full rebuild.
func blockIndexCode(refresh, persist bool) string {
	return fmt.Sprintf(`mcp__fmt = %[3]d; mcp__ver = version; mcp__file = fullfile(prefdir, 'mcp_sl_block_index.mat');
mcp__db = []; mcp__checked = false; mcp__dirty = false;
if %[2]s && ~%[1]s
  mcp__db = getappdata(0, 'mcp_sl_db');
  if isempty(mcp__db) && isfile(mcp__file)
    try
      mcp__S = load(mcp__file);
      if isfield(mcp__S, 'fmt') && isequal(mcp__S.fmt, mcp__fmt) && strcmp(mcp__S.ver, mcp__ver), mcp__db = mcp__S.db; end
    catch
    end
  end
end
if isempty(mcp__db)
  mcp__db = struct('root', {}, 'libs', {}, 'files', {}, 'dates', {}, 'path', {}, 'lib', {}, 'name', {}, 'mask', {}, 'btype', {}, 'islib', {});
  mcp__dirty = true;
end
mcp__keep = true(1, numel(mcp__db)); mcp__stale = {};
for mcp__k = 1:numel(mcp__db)
  if ~any(strcmp(mcp__libs, mcp__db(mcp__k).root)), mcp__keep(mcp__k) = false; continue; end
  if ~mcp__checked
    for mcp__j = 1:numel(mcp__db(mcp__k).files)
      mcp__d = dir(mcp__db(mcp__k).files{mcp__j});
      if isempty(mcp__d) || abs(mcp__d(1).datenum - mcp__db(mcp__k).dates(mcp__j)) > 1e-6
        mcp__keep(mcp__k) = false; mcp__stale{end+1} = mcp__db(mcp__k).root; break; %%#ok<AGROW>
      end
    end
  end
end
if ~all(mcp__keep), mcp__dirty = true; end
mcp__db = mcp__db(mcp__keep);
if isempty(mcp__db), mcp__have = {}; else, mcp__have = {mcp__db.root}; end
mcp__todo = mcp__libs(~ismember(mcp__libs, mcp__have));
if ~isempty(mcp__todo)
  if isempty(mcp__have)
    fprintf('Building the library block index for %%d libraries (once per MATLAB installation; it is saved in prefdir)...\n', numel(mcp__todo));
  else
    fprintf('Updating the library block index: %%s\n', strjoin(mcp__todo, ', '));
    if ~isempty(mcp__stale), fprintf('  (changed on disk: %%s)\n', strjoin(mcp__stale, ', ')); end
  end
  mcp__ws = warning('off', 'all');
  if isempty(mcp__db), mcp__owned = {}; else, mcp__owned = [mcp__db.libs]; end
  for mcp__r = 1:numel(mcp__todo)
    mcp__e = struct('root', mcp__todo{mcp__r}, 'libs', {{}}, 'files', {{}}, 'dates', [], 'path', {cell(0, 1)}, 'lib', {cell(0, 1)}, ...
      'name', {cell(0, 1)}, 'mask', {cell(0, 1)}, 'btype', {cell(0, 1)}, 'islib', false(0, 1));
    mcp__queue = mcp__todo(mcp__r);
    while ~isempty(mcp__queue)
      mcp__lib = mcp__queue{1}; mcp__queue(1) = [];
      if any(strcmp(mcp__owned, mcp__lib)), continue; end
      mcp__owned{end+1} = mcp__lib; %%#ok<AGROW>
      try, if ~bdIsLoaded(mcp__lib), load_system(mcp__lib); end, catch, continue; end
      mcp__e.libs{end+1} = mcp__lib;
      mcp__f = get_param(mcp__lib, 'FileName'); mcp__d = dir(mcp__f);
      if ~isempty(mcp__f) && ~isempty(mcp__d), mcp__e.files{end+1} = mcp__f; mcp__e.dates(end+1) = mcp__d(1).datenum; end
      try, mcp__bl = find_system(mcp__lib, 'LookUnderMasks', 'graphical', 'FollowLinks', 'on', 'Type', 'block'); catch, continue; end
      if isempty(mcp__bl), continue; end
      mcp__of = strtrim(cellstr(get_param(mcp__bl, 'OpenFcn')));
      mcp__tok = regexp(mcp__of, '^(?:open_system\(\s*''?)?(\w+)''?\s*\)?\s*;?$', 'tokens', 'once');
      for mcp__i = 1:numel(mcp__tok)
        if ~isempty(mcp__tok{mcp__i}) && exist(mcp__tok{mcp__i}{1}, 'file') == 4 && ~any(strcmp(mcp__owned, mcp__tok{mcp__i}{1})), mcp__queue{end+1} = mcp__tok{mcp__i}{1}; end %%#ok<AGROW>
      end
      mcp__bt = cellstr(get_param(mcp__bl, 'BlockType')); mcp__mt = cellstr(get_param(mcp__bl, 'MaskType'));
      mcp__pt = get_param(mcp__bl, 'Ports'); if ~iscell(mcp__pt), mcp__pt = {mcp__pt}; end
      mcp__e.path = [mcp__e.path; mcp__bl];
      mcp__e.lib = [mcp__e.lib; repmat({mcp__lib}, numel(mcp__bl), 1)];
      mcp__e.name = [mcp__e.name; regexprep(cellstr(get_param(mcp__bl, 'Name')), '\s+', ' ')];
      mcp__e.mask = [mcp__e.mask; mcp__mt];
      mcp__e.btype = [mcp__e.btype; mcp__bt];
      mcp__e.islib = [mcp__e.islib; strcmp(mcp__bt, 'SubSystem') & cellfun(@isempty, mcp__mt) & cellfun(@(p) ~any(p), mcp__pt)];
    end
    mcp__db(end+1) = mcp__e; %%#ok<AGROW>
  end
  warning(mcp__ws); mcp__dirty = true;
end
if %[2]s
  setappdata(0, 'mcp_sl_db', mcp__db);
  if mcp__dirty, try, mcp__S = struct('fmt', mcp__fmt, 'ver', mcp__ver, 'db', mcp__db); save(mcp__file, '-struct', 'mcp__S'); catch mcp__se, fprintf('Could not save block index: %%s\n', mcp__se.message); end, end
end
mcp__idx = struct('path', {cat(1, cell(0, 1), mcp__db.path)}, 'lib', {cat(1, cell(0, 1), mcp__db.lib)}, 'name', {cat(1, cell(0, 1), mcp__db.name)}, ...
  'mask', {cat(1, cell(0, 1), mcp__db.mask)}, 'btype', {cat(1, cell(0, 1), mcp__db.btype)}, 'islib', cat(1, false(0, 1), mcp__db.islib));
`, boolStr(refresh), boolStr(persist), blockIndexFormat)
}
