// Copyright 2026 The MathWorks, Inc.

package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/matlab/matlab-mcp-server/internal/adaptors/application/config"
	"github.com/matlab/matlab-mcp-server/internal/adaptors/time/retry"
	"github.com/matlab/matlab-mcp-server/internal/entities"
	"github.com/matlab/matlab-mcp-server/internal/messages"
)

var ErrFailedToAttachToMATLABSession = errors.New("failed to attach to MATLAB session")

const defaultDiscoveryRetryInterval = 1 * time.Second

type ConfigFactory interface {
	Config() (config.Config, messages.Error)
}

type MATLABManager interface {
	StartMATLABSession(ctx context.Context, sessionLogger entities.Logger, startRequest entities.SessionDetails) (entities.SessionID, error)
	StopMATLABSession(ctx context.Context, sessionLogger entities.Logger, sessionID entities.SessionID) error
	GetMATLABSessionClient(ctx context.Context, sessionLogger entities.Logger, sessionID entities.SessionID) (entities.MATLABSessionClient, error)
	GetSessionCorrelationID(sessionID entities.SessionID) string
}

type MATLABRootSelector interface {
	SelectMATLABRoot(ctx context.Context, logger entities.Logger) (string, error)
}

type MATLABStartingDirSelector interface {
	SelectMATLABStartingDir(logger entities.Logger) (string, error)
}

type SessionManager struct {
	matlabManager             MATLABManager
	configFactory             ConfigFactory
	matlabRootSelector        MATLABRootSelector
	matlabStartingDirSelector MATLABStartingDirSelector

	discoveryRetryInterval time.Duration

	initOnce          sync.Once
	initErr           error
	matlabRoot        string
	matlabStartingDir string

	overridesLock sync.Mutex
	overrides     StartupOverrides
}

// StartupOverrides lets tools (e.g. start_matlab) change how the next MATLAB
// session is launched without restarting the MCP server.
type StartupOverrides struct {
	// MATLABRoot, when non-empty, replaces the configured/auto-detected MATLAB root.
	MATLABRoot string
	// StartingFolder, when non-empty, replaces the configured starting folder.
	StartingFolder string
	// ShowDesktop, when non-nil, replaces the configured display mode.
	ShowDesktop *bool
	// ForceNewSession launches a new local MATLAB even in auto/existing session modes.
	ForceNewSession bool
}

// SetStartupOverrides stores overrides used by subsequent StartSession calls.
func (s *SessionManager) SetStartupOverrides(overrides StartupOverrides) {
	s.overridesLock.Lock()
	defer s.overridesLock.Unlock()
	s.overrides = overrides
}

// ClearStartupOverrides restores the configured startup behaviour.
func (s *SessionManager) ClearStartupOverrides() {
	s.SetStartupOverrides(StartupOverrides{})
}

func (s *SessionManager) currentOverrides() StartupOverrides {
	s.overridesLock.Lock()
	defer s.overridesLock.Unlock()
	return s.overrides
}

func New(
	matlabManager MATLABManager,
	configFactory ConfigFactory,
	matlabRootSelector MATLABRootSelector,
	matlabStartingDirSelector MATLABStartingDirSelector,
) *SessionManager {
	return &SessionManager{
		matlabManager:             matlabManager,
		configFactory:             configFactory,
		matlabRootSelector:        matlabRootSelector,
		matlabStartingDirSelector: matlabStartingDirSelector,

		discoveryRetryInterval: defaultDiscoveryRetryInterval,
	}
}

func (s *SessionManager) StartSession(ctx context.Context, logger entities.Logger) (entities.SessionID, error) {
	cfg, messagesErr := s.configFactory.Config()
	if messagesErr != nil {
		return 0, messagesErr
	}

	var sessionID entities.SessionID
	var err error

	sessionMode := cfg.MATLABSessionMode()
	if s.currentOverrides().ForceNewSession {
		sessionMode = entities.MATLABSessionModeNew
	}

	switch sessionMode {
	case entities.MATLABSessionModeExisting:
		sessionID, err = s.getSessionFromAttachingToExistingMATLAB(ctx, logger, cfg)
		if err != nil {
			logger.WithError(err).Debug("failed to attach to MATLAB session")
			err = ErrFailedToAttachToMATLABSession
		}
	case entities.MATLABSessionModeAuto:
		sessionID, err = s.getSessionInAutoMode(ctx, logger, cfg)
	default:
		sessionID, err = s.getSessionFromLocalMATLABInstallation(ctx, logger, cfg)
	}

	if err != nil {
		return 0, err
	}

	return sessionID, nil
}

func (s *SessionManager) ShouldRestart() (bool, messages.Error) {
	cfg, err := s.configFactory.Config()
	if err != nil {
		return false, err
	}

	shouldNotRestart := cfg.MATLABSessionMode() == entities.MATLABSessionModeExisting &&
		cfg.MATLABSessionConnectionDetails() != ""

	return !shouldNotRestart, nil
}

func (s *SessionManager) StopMATLABSession(ctx context.Context, sessionLogger entities.Logger, sessionID entities.SessionID) error {
	return s.matlabManager.StopMATLABSession(ctx, sessionLogger, sessionID)
}

func (s *SessionManager) GetMATLABSessionClient(ctx context.Context, sessionLogger entities.Logger, sessionID entities.SessionID) (entities.MATLABSessionClient, error) {
	return s.matlabManager.GetMATLABSessionClient(ctx, sessionLogger, sessionID)
}

func (s *SessionManager) GetSessionCorrelationID(sessionID entities.SessionID) string {
	return s.matlabManager.GetSessionCorrelationID(sessionID)
}

func (s *SessionManager) initializeStartupConfig(ctx context.Context, logger entities.Logger) error {
	matlabRoot, err := s.matlabRootSelector.SelectMATLABRoot(ctx, logger)
	if err != nil {
		return err
	}

	s.matlabRoot = matlabRoot

	matlabStartingDirectory, err := s.matlabStartingDirSelector.SelectMATLABStartingDir(logger)
	if err != nil {
		logger.WithError(err).Warn("failed to determine MATLAB starting directory, proceeding without one")
		return nil
	}

	s.matlabStartingDir = matlabStartingDirectory
	return nil
}

func (s *SessionManager) getSessionFromLocalMATLABInstallation(ctx context.Context, logger entities.Logger, cfg config.Config) (entities.SessionID, error) {
	if s.currentOverrides().MATLABRoot == "" {
		s.initOnce.Do(func() {
			s.initErr = s.initializeStartupConfig(ctx, logger)
		})
	}
	if s.currentOverrides().MATLABRoot == "" && s.initErr != nil {
		return 0, s.initErr
	}

	overrides := s.currentOverrides()

	matlabRoot := s.matlabRoot
	if overrides.MATLABRoot != "" {
		matlabRoot = overrides.MATLABRoot
	}

	startingDir := s.matlabStartingDir
	if startingDir == "" && overrides.MATLABRoot != "" {
		// The root was overridden, so initializeStartupConfig did not run:
		// still honour the configured / MCP-root based starting folder.
		if dir, err := s.matlabStartingDirSelector.SelectMATLABStartingDir(logger); err == nil {
			startingDir = dir
		}
	}
	if overrides.StartingFolder != "" {
		startingDir = overrides.StartingFolder
	}

	showDesktop := cfg.ShouldShowMATLABDesktop()
	if overrides.ShowDesktop != nil {
		showDesktop = *overrides.ShowDesktop
	}

	startRequest := entities.LocalSessionDetails{
		MATLABRoot:             matlabRoot,
		IsStartingDirectorySet: startingDir != "",
		StartingDirectory:      startingDir,
		ShowMATLABDesktop:      showDesktop,
	}

	return s.matlabManager.StartMATLABSession(ctx, logger, startRequest)
}

func (s *SessionManager) getSessionInAutoMode(ctx context.Context, logger entities.Logger, cfg config.Config) (entities.SessionID, error) {
	sessionID, err := s.getSessionFromAttachingToExistingMATLAB(ctx, logger, cfg)
	if err != nil {
		logger.
			WithError(err).
			Debug("auto mode: no existing MATLAB session found, falling back to launching MATLAB")
		return s.getSessionFromLocalMATLABInstallation(ctx, logger, cfg)
	}

	return sessionID, nil
}

func (s *SessionManager) getSessionFromAttachingToExistingMATLAB(ctx context.Context, logger entities.Logger, cfg config.Config) (entities.SessionID, error) {
	startRequest := entities.AttachToExistingSession{}

	discoveryTimeout := cfg.MATLABSessionDiscoveryTimeout()

	// A zero timeout means "try once, no polling". Use half the retry interval
	// so the context stays alive long enough for a single attempt but expires
	// before the retry strategy fires a second one.
	if discoveryTimeout == 0 {
		discoveryTimeout = s.discoveryRetryInterval / 2
	}

	attachCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	var lastErr error

	sessionID, err := retry.Retry(attachCtx, func() (entities.SessionID, bool, error) {
		sessionID, err := s.matlabManager.StartMATLABSession(attachCtx, logger, startRequest)
		if err != nil {
			lastErr = err
			return 0, false, nil
		}

		return sessionID, true, nil
	}, retry.NewLinearRetryStrategy(s.discoveryRetryInterval))

	if err != nil {
		if lastErr != nil {
			// Return the last error rather than timeout error
			return 0, lastErr
		}
		return 0, err
	}

	return sessionID, nil
}
