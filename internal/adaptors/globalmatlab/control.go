// Extended MATLAB/Simulink/Simscape toolset (community fork).
// Based on matlab/matlab-mcp-server, Copyright 2025-2026 The MathWorks, Inc. See LICENSE.md.

package globalmatlab

import (
	"context"

	"github.com/matlab/matlab-mcp-server/internal/adaptors/globalmatlab/sessionmanager"
	"github.com/matlab/matlab-mcp-server/internal/entities"
)

// startupOverrider is implemented by the session manager. It is checked via a
// type assertion so that existing mocks of MATLABManagerAdaptor stay valid.
type startupOverrider interface {
	SetStartupOverrides(overrides sessionmanager.StartupOverrides)
	ClearStartupOverrides()
}

// IsRunning reports whether the global MATLAB session has been started and not stopped.
func (g *GlobalMATLAB) IsRunning() bool {
	g.lock.Lock()
	defer g.lock.Unlock()

	var zero entities.SessionID
	return g.sessionID != zero
}

// Start explicitly starts (or attaches to) the global MATLAB session.
// When overrides is non-nil they are applied to this and later launches.
// Any cached unrecoverable start error is cleared, so Start can be used to
// recover after a failed launch. Returns alreadyRunning=true when a session
// already existed (overrides are then ignored).
func (g *GlobalMATLAB) Start(ctx context.Context, logger entities.Logger, overrides *sessionmanager.StartupOverrides) (client entities.MATLABSessionClient, alreadyRunning bool, err error) {
	g.lock.Lock()
	defer g.lock.Unlock()

	var zero entities.SessionID
	if g.sessionID != zero {
		client, err := g.getOrCreateClient(ctx, logger)
		return client, true, err
	}

	g.startSessionError = nil

	if overrider, ok := g.matlabManagerAdaptor.(startupOverrider); ok {
		if overrides != nil {
			overrider.SetStartupOverrides(*overrides)
		}
	}

	client, err = g.getOrCreateClient(ctx, logger)
	return client, false, err
}

// Stop stops the global MATLAB session (quits a MATLAB launched by the server,
// detaches from an existing shared session). Returns wasRunning=false when no
// session was active. After Stop, the next tool call starts MATLAB again.
func (g *GlobalMATLAB) Stop(ctx context.Context, logger entities.Logger) (wasRunning bool, err error) {
	g.lock.Lock()
	defer g.lock.Unlock()

	var zero entities.SessionID
	g.startSessionError = nil

	if g.sessionID == zero {
		return false, nil
	}

	sessionID := g.sessionID
	g.sessionID = zero

	if err := g.matlabManagerAdaptor.StopMATLABSession(ctx, logger, sessionID); err != nil {
		return true, err
	}

	return true, nil
}

// ResetStartupOverrides restores the startup configuration given on the command line.
func (g *GlobalMATLAB) ResetStartupOverrides() {
	g.lock.Lock()
	defer g.lock.Unlock()

	if overrider, ok := g.matlabManagerAdaptor.(startupOverrider); ok {
		overrider.ClearStartupOverrides()
	}
}
