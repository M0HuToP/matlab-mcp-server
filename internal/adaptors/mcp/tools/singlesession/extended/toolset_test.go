// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package extended

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolset_NamesUniqueAndSchemasValid(t *testing.T) {
	ts := NewWithDependencies(nil, nil, nil, nil, nil)
	seen := map[string]bool{}
	for _, tool := range ts.Tools() {
		require.False(t, seen[tool.Name()], "duplicate tool name %s", tool.Name())
		seen[tool.Name()] = true

		schemaTool, ok := tool.(interface{ GetInputSchema() (any, error) })
		require.True(t, ok, tool.Name())
		_, err := schemaTool.GetInputSchema()
		require.NoError(t, err, tool.Name())
	}
	assert.True(t, seen["simulink_build_model"])
	assert.True(t, seen["start_matlab"])
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		in     string
		source bool
		field  string
		index  int
	}{
		{"1", true, "Outport", 1},
		{"2", false, "Inport", 2},
		{"out3", false, "Outport", 3},
		{"in1", true, "Inport", 1},
		{"lconn1", true, "LConn", 1},
		{"RConn2", false, "RConn", 2},
		{"enable", false, "Enable", 1},
		{"trigger", false, "Trigger", 1},
		{"ifaction", false, "Ifaction", 1},
		{"reset", false, "Reset", 1},
		{"state", true, "State", 1},
	}
	for _, c := range cases {
		p, err := parsePort(c.in, c.source)
		require.NoError(t, err, c.in)
		assert.Equal(t, c.field, p.Field, c.in)
		assert.Equal(t, c.index, p.Index, c.in)
	}
	_, err := parsePort("xyz", true)
	assert.Error(t, err)
}

func TestParseEndpoint(t *testing.T) {
	e, err := parseEndpoint("Sub/Gain/1", true)
	require.NoError(t, err)
	assert.Equal(t, "Sub/Gain", e.Block)
	assert.Equal(t, "Outport", e.Port.Field)

	_, err = parseEndpoint("Gain", true)
	assert.Error(t, err)
}

func TestQ(t *testing.T) {
	assert.Equal(t, "'it''s'", q("it's"))
	assert.Equal(t, "['a' newline 'b']", q("a\nb"))
}

func TestBuildModelCode(t *testing.T) {
	code, err := buildModelCode(BuildModelArgs{
		Model: "rc",
		Blocks: []BuildBlock{
			{Name: "R", Source: "fl_lib/Electrical/Electrical Elements/Resistor", Params: map[string]string{"R": "10"}},
			{Name: "C", Source: "fl_lib/Electrical/Electrical Elements/Capacitor"},
		},
		Connections: []Connection{{From: "R/rconn1", To: []string{"C/lconn1"}}},
	})
	require.NoError(t, err)
	assert.Contains(t, code, "new_system(mcp__mdl)")
	assert.Contains(t, code, "mcp__srcPH.RConn(1), mcp__dstPH.LConn(1)")
	assert.Contains(t, code, "save_system(mcp__mdl, mcp__file)")
	assert.True(t, strings.Contains(code, "arrangeSystem"))
}
