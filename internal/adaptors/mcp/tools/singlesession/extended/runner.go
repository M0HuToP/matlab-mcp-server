// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

// Package extended contains additional single-session tools: explicit MATLAB
// lifecycle control, command-line access, and Simulink / Simscape model
// construction, simulation and project management.
package extended

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/matlab/matlab-mcp-server/internal/adaptors/application/config"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/globalmatlab/sessionmanager"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/mcp/tools/basetool"
	"github.com/matlab/matlab-mcp-server/internal/entities"
	"github.com/matlab/matlab-mcp-server/internal/messages"
)

type ConfigFactory interface {
	Config() (config.Config, messages.Error)
}

// SessionController is the subset of *globalmatlab.GlobalMATLAB used by these tools.
type SessionController interface {
	Client(ctx context.Context, logger entities.Logger) (entities.MATLABSessionClient, error)
	IsRunning() bool
	Start(ctx context.Context, logger entities.Logger, overrides *sessionmanager.StartupOverrides) (entities.MATLABSessionClient, bool, error)
	Stop(ctx context.Context, logger entities.Logger) (bool, error)
	ResetStartupOverrides()
}

// EnvironmentLister lists MATLAB installations discovered by the server.
type EnvironmentLister interface {
	ListEnvironments(ctx context.Context, sessionLogger entities.Logger) []entities.EnvironmentInfo
}

type deps struct {
	loggerFactory    basetool.LoggerFactory
	telemetryFactory basetool.TelemetryFactory
	configFactory    ConfigFactory
	session          SessionController
	environments     EnvironmentLister
}

const (
	errorMarker = "<<MCP_ERROR>>"
	imagePrefix = "<<MCP_IMAGE:"
	imageSuffix = ">>"
)

var imageMarkerRegexp = regexp.MustCompile(`<<MCP_IMAGE:([^>\r\n]+)>>\r?\n?`)

func newTool[A any](d *deps, name, title, description string, ann basetool.AnnotationProvider, h basetool.HandlerWithUnstructuredContentOutput[A]) tools.Tool {
	return basetool.NewToolWithUnstructuredContent(name, title, description, ann, d.loggerFactory, d.telemetryFactory, h)
}

// wrapCode wraps generated MATLAB code so that errors are reported with a full
// report, and temporary variables (prefixed mcp__) are removed from the base workspace.
func wrapCode(body string) string {
	var b strings.Builder
	b.WriteString("try\n")
	b.WriteString(body)
	b.WriteString("\ncatch mcp__err\n")
	b.WriteString("fprintf('%s\\n%s\\n', '" + errorMarker + "', " + reportExpr("mcp__err") + ");\n")
	b.WriteString("end\n")
	b.WriteString("clear('-regexp', '^mcp__');\n")
	return b.String()
}

// runMATLAB evaluates generated MATLAB code in the global session and converts
// the output to rich content (text + images emitted via image markers).
func (d *deps) runMATLAB(ctx context.Context, logger entities.Logger, body string) (tools.RichContent, error) {
	client, err := d.session.Client(ctx, logger)
	if err != nil {
		return tools.RichContent{}, err
	}
	return d.runWithClient(ctx, logger, client, body)
}

func (d *deps) runWithClient(ctx context.Context, logger entities.Logger, client entities.MATLABSessionClient, body string) (tools.RichContent, error) {
	capture := d.captureMode()

	request := entities.EvalRequest{Code: wrapCode(body)}

	if capture {
		response, err := client.EvalWithCapture(ctx, logger, request)
		if err != nil {
			return tools.RichContent{}, err
		}
		return convertOutput(response)
	}

	response, err := client.Eval(ctx, logger, request)
	if err != nil {
		return tools.RichContent{}, err
	}
	return convertOutput(response)
}

// captureMode reports whether output is evaluated with capture. In that mode
// the server already returns figures created by the code as images.
func (d *deps) captureMode() bool {
	if d.configFactory == nil {
		return false
	}
	cfg, cfgErr := d.configFactory.Config()
	return cfgErr == nil && !cfg.ShouldShowMATLABDesktop()
}

func convertOutput(response entities.EvalResponse) (tools.RichContent, error) {
	text := response.ConsoleOutput

	var images []tools.PNGImageData
	for _, img := range response.Images {
		images = append(images, img)
	}

	for _, match := range imageMarkerRegexp.FindAllStringSubmatch(text, -1) {
		path := strings.TrimSpace(match[1])
		data, readErr := os.ReadFile(path) //nolint:gosec // path is produced by our own generated MATLAB code (tempname)
		if readErr == nil {
			images = append(images, data)
			_ = os.Remove(path)
		}
	}
	text = imageMarkerRegexp.ReplaceAllString(text, "")

	if idx := strings.Index(text, errorMarker); idx >= 0 {
		out := strings.TrimSpace(text[:idx])
		report := strings.TrimSpace(text[idx+len(errorMarker):])
		msg := "MATLAB error:\n" + report
		if out != "" {
			msg = "Output before error:\n" + out + "\n\n" + msg
		}
		return tools.RichContent{}, errors.New(msg)
	}

	return tools.RichContent{
		TextContent:  []string{strings.TrimRight(text, "\r\n ")},
		ImageContent: images,
	}, nil
}

func textResult(s string) tools.RichContent {
	return tools.RichContent{TextContent: []string{s}}
}
