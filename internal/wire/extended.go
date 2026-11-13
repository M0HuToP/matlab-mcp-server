// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package wire

import (
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/resources/codingguidelines"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/resources/plaintextlivecodegeneration"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/server/configurator"
	evalmatlabcodemultisessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/multisession/evalmatlabcode"
	listavailablematlabstool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/multisession/listavailablematlabs"
	startmatlabsessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/multisession/startmatlabsession"
	stopmatlabsessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/multisession/stopmatlabsession"
	checkmatlabcodesinglesessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/singlesession/checkmatlabcode"
	detectmatlabtoolboxessinglesessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/singlesession/detectmatlabtoolboxes"
	evalmatlabcodesinglesessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/singlesession/evalmatlabcode"
	extendedtools "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/singlesession/extended"
	runmatlabfilesinglesessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/singlesession/runmatlabfile"
	runmatlabtestfilesinglesessiontool "github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/singlesession/runmatlabtestfile"
)

// provideConfigurator builds the MCP configurator including the extended
// MATLAB lifecycle / command line / Simulink / Simscape toolset.
func provideConfigurator(
	configFactory configurator.ConfigFactory,
	featuresProvider configurator.ApplicationDefinition,
	listAvailableMATLABsTool *listavailablematlabstool.Tool,
	startMATLABSessionTool *startmatlabsessiontool.Tool,
	stopMATLABSessionTool *stopmatlabsessiontool.Tool,
	evalInMATLABSessionTool *evalmatlabcodemultisessiontool.Tool,
	evalInGlobalMATLABSessionTool *evalmatlabcodesinglesessiontool.Tool,
	checkMATLABCodeTool *checkmatlabcodesinglesessiontool.Tool,
	detectMATLABToolboxesTool *detectmatlabtoolboxessinglesessiontool.Tool,
	runMATLABFileTool *runmatlabfilesinglesessiontool.Tool,
	runMATLABTestFileTool *runmatlabtestfilesinglesessiontool.Tool,
	codingGuidelinesResource *codingguidelines.Resource,
	plaintextlivecodegenerationResource *plaintextlivecodegeneration.Resource,
	customToolFactory configurator.CustomToolFactory,
	extendedToolset *extendedtools.Toolset,
) *configurator.Configurator {
	return configurator.New(
		configFactory,
		featuresProvider,
		listAvailableMATLABsTool,
		startMATLABSessionTool,
		stopMATLABSessionTool,
		evalInMATLABSessionTool,
		evalInGlobalMATLABSessionTool,
		checkMATLABCodeTool,
		detectMATLABToolboxesTool,
		runMATLABFileTool,
		runMATLABTestFileTool,
		codingGuidelinesResource,
		plaintextlivecodegenerationResource,
		customToolFactory,
	).WithExtraSingleSessionTools(extendedToolset.Tools()...)
}
