package servermanager

import (
	"bufio"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/razims/siebel_exporter/pkg/logger"
	"go.uber.org/zap"
)

// ServerManager handles the Siebel Server Manager (srvrmgr) process
type ServerManager struct {
	cmd                  *exec.Cmd
	stdin                *bufio.Writer
	stdout               *bufio.Scanner
	stderr               *bufio.Scanner
	mu                   sync.Mutex
	stderrOutput         []string
	stdoutOutput         []string
	promptStartedPattern *regexp.Regexp
	promptEndedPattern   *regexp.Regexp
	status               Status

	// Configuration
	config ServerManagerConfig

	// Reconnection related fields
	stopReconnect   chan struct{}
	reconnectWg     sync.WaitGroup
	lastActivity    time.Time
	heartbeatTicker *time.Ticker
	isReconnecting  bool
}

// NewServerManager creates an instance of ServerManager with the provided configuration
func NewServerManager(config ServerManagerConfig) *ServerManager {
	// Set default reconnect delay if auto-reconnect is enabled but no delay specified
	if config.AutoReconnect && config.ReconnectDelay <= 0 {
		config.ReconnectDelay = DefaultReconnectDelay
	}

	// Define patterns for prompt detection
	promptPattern := regexp.MustCompile(`srvrmgr(:.*|>)`)
	promptEndedPattern := regexp.MustCompile(`.*\ row(|s)\ returned\.`)

	logger.Debug("Creating new ServerManager instance",
		zap.String("gateway", config.Gateway),
		zap.String("enterprise", config.Enterprise),
		zap.String("server", config.Server),
		zap.String("path", config.SrvrmgrPath),
		zap.Bool("autoReconnect", config.AutoReconnect))

	return &ServerManager{
		promptStartedPattern: promptPattern,
		promptEndedPattern:   promptEndedPattern,
		status:               Disconnected,
		config:               config,
		stopReconnect:        make(chan struct{}),
	}
}

// Connect starts srvrmgr using the configuration provided at creation
func (sm *ServerManager) Connect() error {
	return sm.connect()
}

// setStatus safely updates the status
func (sm *ServerManager) setStatus(status Status) {
	sm.mu.Lock()
	oldStatus := sm.status
	sm.status = status
	sm.mu.Unlock()

	logger.Debug("ServerManager status changed",
		zap.String("from", string(oldStatus)),
		zap.String("to", string(status)))
}

// readOutput continuously reads a given scanner to prevent blocking
func (sm *ServerManager) readOutput(scanner *bufio.Scanner, output *[]string) {
	for scanner.Scan() {
		line := scanner.Text()
		sm.mu.Lock()
		*output = append(*output, line)
		sm.lastActivity = time.Now() // Update last activity time
		sm.mu.Unlock()

		if logger.Log.Core().Enabled(zap.DebugLevel) {
			logger.Debug("Read output line", zap.String("line", line))
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Warn("Scanner error", zap.Error(err))
	}

	logger.Debug("Scanner finished reading")
}

// GetStatus retrieves the current status of the ServerManager
func (sm *ServerManager) GetStatus() Status {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.status
}

// IsConnected returns true if the ServerManager is in Connected status
func (sm *ServerManager) IsConnected() bool {
	return sm.GetStatus() == Connected
}

// IsReconnecting returns true if the ServerManager is actively trying to reconnect
func (sm *ServerManager) IsReconnecting() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.isReconnecting
}

// handlePipeError handles pipe closed errors by initiating reconnection if enabled
func (sm *ServerManager) handlePipeError() {
	sm.mu.Lock()
	currentStatus := sm.status
	autoReconnect := sm.config.AutoReconnect
	sm.mu.Unlock()

	// Mark as disconnected due to error
	sm.setStatus(ConnectionError)

	logger.Warn("Pipe error detected",
		zap.String("previousStatus", string(currentStatus)),
		zap.Bool("autoReconnect", autoReconnect))

	// If reconnection is enabled and we thought we were connected, try to reconnect
	if autoReconnect && currentStatus == Connected {
		go sm.tryReconnect()
	}
}

// UpdateConfig updates the ServerManager configuration
func (sm *ServerManager) UpdateConfig(config ServerManagerConfig) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Store previous auto-reconnect setting
	previousAutoReconnect := sm.config.AutoReconnect

	logger.Debug("Updating configuration",
		zap.String("gateway", config.Gateway),
		zap.String("enterprise", config.Enterprise),
		zap.String("server", config.Server))

	// Update configuration
	sm.config = config

	// If auto-reconnect is enabled but no delay specified, set default
	if sm.config.AutoReconnect && sm.config.ReconnectDelay <= 0 {
		sm.config.ReconnectDelay = DefaultReconnectDelay
	}

	// If auto-reconnect was disabled and is now enabled, start heartbeat checker
	if !previousAutoReconnect && sm.config.AutoReconnect && sm.status == Connected {
		logger.Debug("Auto-reconnect enabled, starting heartbeat checker")
		sm.mu.Unlock()
		sm.startHeartbeatChecker()
		sm.mu.Lock()
	}

	// If auto-reconnect was enabled and is now disabled, stop reconnection attempts
	if previousAutoReconnect && !sm.config.AutoReconnect {
		logger.Debug("Auto-reconnect disabled, stopping reconnection attempts")
		close(sm.stopReconnect)
		sm.stopReconnect = make(chan struct{})
	}
}

// GetConfig returns a copy of the current configuration
func (sm *ServerManager) GetConfig() ServerManagerConfig {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.config
}
