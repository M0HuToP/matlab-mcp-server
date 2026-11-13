// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import (
	"github.com/matlab/matlab-mcp-server/internal/adaptors/globalmatlab"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/matlabmanager"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/basetool"
)

// Toolset groups the extended single-session tools.
type Toolset struct {
	tools []tools.Tool
}

// New creates the toolset. Concrete types are used so that wire can inject them.
func New(
	loggerFactory basetool.LoggerFactory,
	telemetryFactory basetool.TelemetryFactory,
	configFactory ConfigFactory,
	globalMATLAB *globalmatlab.GlobalMATLAB,
	matlabManager *matlabmanager.MATLABManager,
) *Toolset {
	var lister EnvironmentLister
	if matlabManager != nil {
		lister = matlabManager
	}
	var session SessionController
	if globalMATLAB != nil {
		session = globalMATLAB
	}
	return NewWithDependencies(loggerFactory, telemetryFactory, configFactory, session, lister)
}

// NewWithDependencies creates the toolset from interfaces (useful for tests).
func NewWithDependencies(
	loggerFactory basetool.LoggerFactory,
	telemetryFactory basetool.TelemetryFactory,
	configFactory ConfigFactory,
	session SessionController,
	environments EnvironmentLister,
) *Toolset {
	d := &deps{
		loggerFactory:    loggerFactory,
		telemetryFactory: telemetryFactory,
		configFactory:    configFactory,
		session:          session,
		environments:     environments,
	}
	return &Toolset{
		tools: []tools.Tool{
			// MATLAB lifecycle
			newStartMATLABTool(d),
			newStopMATLABTool(d),
			newStatusTool(d),
			newListInstallsTool(d),

			// Command line
			newRunCommandTool(d),
			newShellTool(d),
			newWorkspaceTool(d),
			newHelpTool(d),

			// Simulink models
			newNewModelTool(d),
			newOpenModelTool(d),
			newSaveModelTool(d),
			newCloseModelTool(d),
			newStructureTool(d),

			// Libraries and blocks (Simulink, Simscape, any toolbox)
			newListLibrariesTool(d),
			newSearchBlocksTool(d),
			newBrowseLibraryTool(d),
			newBlockInfoTool(d),
			newAddBlockTool(d),
			newDeleteBlocksTool(d),
			newConnectTool(d),
			newDeleteLinesTool(d),
			newSetParamsTool(d),
			newGetParamsTool(d),
			newArrangeTool(d),
			newScreenshotTool(d),

			// Automatic model construction
			newBuildModelTool(d),

			// Simulation
			newRunSimTool(d),
			newSimControlTool(d),

			// Projects
			newProjectTool(d),
		},
	}
}

// Tools returns all tools of the set.
func (t *Toolset) Tools() []tools.Tool {
	if t == nil {
		return nil
	}
	return t.tools
}
