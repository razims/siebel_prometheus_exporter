package servermanager

import (
	"bufio"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
	"go.uber.org/zap"
)

// detectConnectionError analyzes error output to determine if it indicates a connection issue
func detectConnectionError(errorLines []string) (bool, string) {
	// Common error patterns that indicate connection failures
	errorPatterns := []string{
		"error",
		"failed",
		"cannot connect",
		"connection refused",
		"unknown host",
		"timeout",
		"authentication failed",
		"access denied",
		"invalid credentials",
		"server not found",
	}

	logger.Debug("Analyzing error lines for connection issues",
		zap.Int("lineCount", len(errorLines)),
		zap.Strings("patterns", errorPatterns))

	for i, line := range errorLines {
		lowercaseLine := strings.ToLower(line)
		for _, pattern := range errorPatterns {
			if strings.Contains(lowercaseLine, pattern) {
				logger.Debug("Connection error pattern match found",
					zap.Int("lineIndex", i),
					zap.String("pattern", pattern),
					zap.String("line", line))
				return true, line
			}
		}
	}

	logger.Debug("No connection error patterns found in error lines")
	return false, ""
}

// connect is the internal connection method that uses stored config
func (sm *ServerManager) connect() error {
	sm.mu.Lock()
	if sm.status == Connecting || sm.status == Connected {
		status := sm.status
		sm.mu.Unlock()
		logger.Warn("Connection attempt while already connecting/connected", zap.String("currentStatus", string(status)))
		return errors.New("already connected or connecting")
	}

	sm.status = Connecting
	config := sm.config // Make a local copy to use after unlocking
	sm.mu.Unlock()

	logger.Info("Connecting to Siebel Server Manager",
		zap.String("gateway", config.Gateway),
		zap.String("enterprise", config.Enterprise),
		zap.String("server", config.Server),
		zap.String("user", config.User),
		zap.String("srvrmgrPath", config.SrvrmgrPath))

	args := []string{
		"-g", config.Gateway,
		"-e", config.Enterprise,
		"-s", config.Server,
		"-u", config.User,
		"-p", config.Password,
	}

	sm.mu.Lock()
	sm.cmd = exec.Command(config.SrvrmgrPath, args...)
	sm.mu.Unlock()

	logger.Debug("Creating stdin pipe")
	stdinPipe, err := sm.cmd.StdinPipe()
	if err != nil {
		logger.Error("Failed to create stdin pipe", zap.Error(err))
		sm.setStatus(ConnectionError)
		return fmt.Errorf("stdin error: %v", err)
	}

	logger.Debug("Creating stdout pipe")
	stdoutPipe, err := sm.cmd.StdoutPipe()
	if err != nil {
		logger.Error("Failed to create stdout pipe", zap.Error(err))
		sm.setStatus(ConnectionError)
		return fmt.Errorf("stdout error: %v", err)
	}

	logger.Debug("Creating stderr pipe")
	stderrPipe, err := sm.cmd.StderrPipe()
	if err != nil {
		logger.Error("Failed to create stderr pipe", zap.Error(err))
		sm.setStatus(ConnectionError)
		return fmt.Errorf("stderr error: %v", err)
	}

	sm.mu.Lock()
	sm.stdin = bufio.NewWriter(stdinPipe)
	sm.stdout = bufio.NewScanner(stdoutPipe)
	sm.stderr = bufio.NewScanner(stderrPipe)
	sm.stdoutOutput = []string{}
	sm.stderrOutput = []string{}
	sm.mu.Unlock()

	logger.Debug("Starting srvrmgr process")
	if err := sm.cmd.Start(); err != nil {
		logger.Error("Failed to start srvrmgr process", zap.Error(err))
		sm.setStatus(ConnectionError)
		return fmt.Errorf("error starting srvrmgr: %v", err)
	}
	logger.Debug("srvrmgr process started successfully", zap.Int("pid", sm.cmd.Process.Pid))

	// Start goroutines to continuously read stdout and stderr
	sm.reconnectWg.Add(2)

	// Reading from stdout
	logger.Debug("Starting stdout reader goroutine")
	go func() {
		defer sm.reconnectWg.Done()
		logger.Debug("Stdout reader started")
		sm.readOutput(sm.stdout, &sm.stdoutOutput)
		logger.Debug("Stdout reader finished")
	}()

	// Reading from stderr
	logger.Debug("Starting stderr reader goroutine")
	go func() {
		defer sm.reconnectWg.Done()
		logger.Debug("Stderr reader started")
		sm.readOutput(sm.stderr, &sm.stderrOutput)
		logger.Debug("Stderr reader finished")
	}()

	// Wait for initial output to confirm connection
	logger.Debug("Waiting for initial output from srvrmgr")
	time.Sleep(2 * time.Second)

	// Check for any error output that indicates connection failure
	sm.mu.Lock()
	stderrLines := len(sm.stderrOutput)
	stdoutLines := len(sm.stdoutOutput)
	logger.Debug("Initial connection output received",
		zap.Int("stderrLines", stderrLines),
		zap.Int("stdoutLines", stdoutLines))

	if stderrLines > 0 {
		// Check stderr for connection errors using the new function
		hasError, errorMsg := detectConnectionError(sm.stderrOutput)
		if hasError {
			sm.status = ConnectionError
			sm.mu.Unlock()
			logger.Error("Connection error detected in stderr output",
				zap.String("error", errorMsg),
				zap.Strings("allErrors", sm.stderrOutput))
			return fmt.Errorf("connection error: %s", errorMsg)
		}
	}

	// Set status to Connected if no errors occurred
	sm.status = Connected
	sm.lastActivity = time.Now()
	sm.mu.Unlock()

	// Start the heartbeat checker if reconnection is enabled
	if config.AutoReconnect {
		logger.Debug("Starting heartbeat checker")
		sm.startHeartbeatChecker()
	}

	logger.Info("Successfully connected to Siebel Server Manager")
	return nil
}

// Disconnect terminates the srvrmgr shell
func (sm *ServerManager) Disconnect() error {
	sm.mu.Lock()
	currentStatus := sm.status

	// Stop any reconnection attempts
	if sm.config.AutoReconnect {
		logger.Debug("Stopping reconnect attempts during disconnect")
		close(sm.stopReconnect)
		sm.stopReconnect = make(chan struct{})
	}

	// Stop heartbeat ticker
	if sm.heartbeatTicker != nil {
		logger.Debug("Stopping heartbeat ticker during disconnect")
		sm.heartbeatTicker.Stop()
		sm.heartbeatTicker = nil
	}

	if currentStatus == Disconnected {
		logger.Debug("Disconnect called but already disconnected")
		sm.mu.Unlock()
		return nil
	}

	// Create local references to avoid holding lock
	cmd := sm.cmd
	sm.status = Disconnecting
	sm.mu.Unlock()

	logger.Info("Disconnecting from Siebel Server Manager", zap.String("previousStatus", string(currentStatus)))

	// First try to send exit command with very short timeout
	exitSuccessful := false
	if cmd != nil && cmd.Process != nil {
		logger.Debug("Attempting graceful exit via exit command")

		// Try to send exit command with short timeout
		_, err := sm.SendCommandWithTimeout("exit", 1*time.Second)
		if err != nil {
			logger.Debug("Exit command failed (continuing with kill)", zap.Error(err))
		} else {
			logger.Debug("Exit command sent successfully, waiting briefly for termination")
			exitSuccessful = true

			// Give the process a brief moment to exit gracefully
			exitWaitChan := make(chan struct{})
			go func() {
				cmd.Wait()
				close(exitWaitChan)
			}()

			// Wait up to 1 second for graceful exit
			select {
			case <-exitWaitChan:
				logger.Debug("Process exited gracefully after exit command")
				sm.setStatus(Disconnected)
				return nil
			case <-time.After(1 * time.Second):
				logger.Debug("Process did not exit after exit command, proceeding to kill")
			}
		}
	}

	// If graceful exit didn't work, kill the process
	if cmd != nil && cmd.Process != nil {
		logger.Debug("Killing srvrmgr process", zap.Int("pid", cmd.Process.Pid))
		killErr := cmd.Process.Kill()
		if killErr != nil {
			logger.Warn("Failed to kill srvrmgr process", zap.Error(killErr))
			// Continue with cleanup despite the error
		} else {
			logger.Debug("Successfully killed srvrmgr process")
		}
	} else {
		logger.Debug("No active srvrmgr process to kill")
	}

	// Wait for output readers to complete with short timeout
	logger.Debug("Waiting for output readers to complete")
	outputWaitChan := make(chan struct{})
	go func() {
		sm.reconnectWg.Wait()
		close(outputWaitChan)
	}()

	// Add short timeout for waiting on output readers
	select {
	case <-outputWaitChan:
		logger.Debug("Output readers completed successfully")
	case <-time.After(1 * time.Second):
		logger.Warn("Timed out waiting for output readers to complete")
	}

	// If we previously tried an exit command and are still here,
	// let's wait for the process to finish
	if exitSuccessful && cmd != nil {
		logger.Debug("Waiting for srvrmgr process to exit after kill signal")
		waitChan := make(chan error, 1)
		go func() {
			waitChan <- cmd.Wait()
		}()

		select {
		case err := <-waitChan:
			if err != nil {
				// Don't fail on expected exit errors (process killed)
				if !strings.Contains(err.Error(), "process already finished") &&
					!strings.Contains(err.Error(), "signal: killed") {
					logger.Warn("Error waiting for srvrmgr process to exit", zap.Error(err))
				}
				logger.Debug("srvrmgr process exited with expected error", zap.Error(err))
			} else {
				logger.Debug("srvrmgr process exited cleanly")
			}
		case <-time.After(1 * time.Second):
			logger.Warn("Timed out waiting for srvrmgr process to exit after kill")
		}
	}

	sm.setStatus(Disconnected)
	logger.Info("Successfully disconnected from Siebel Server Manager")
	return nil
}

// EnableAutoReconnect enables automatic reconnection
func (sm *ServerManager) EnableAutoReconnect(delay time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	previouslyEnabled := sm.config.AutoReconnect
	sm.config.AutoReconnect = true

	if delay > 0 {
		sm.config.ReconnectDelay = delay
	} else if sm.config.ReconnectDelay <= 0 {
		sm.config.ReconnectDelay = DefaultReconnectDelay
	}

	logger.Info("Auto-reconnect enabled",
		zap.Duration("delay", sm.config.ReconnectDelay),
		zap.Bool("wasEnabled", previouslyEnabled))

	// Start heartbeat checker if we're connected and auto-reconnect was just enabled
	if !previouslyEnabled && sm.status == Connected {
		go sm.startHeartbeatChecker()
	}
}

// DisableAutoReconnect disables automatic reconnection
func (sm *ServerManager) DisableAutoReconnect() {
	sm.mu.Lock()
	previouslyEnabled := sm.config.AutoReconnect
	sm.config.AutoReconnect = false
	sm.mu.Unlock()

	logger.Info("Auto-reconnect disabled", zap.Bool("wasEnabled", previouslyEnabled))

	// Stop any ongoing reconnection attempts if it was previously enabled
	if previouslyEnabled && sm.stopReconnect != nil {
		logger.Debug("Stopping active reconnection attempts")
		close(sm.stopReconnect)
		sm.stopReconnect = make(chan struct{})
	}
}

// cleanupProcess cleans up the existing process without changing user-facing status
func (sm *ServerManager) cleanupProcess() {
	sm.mu.Lock()
	cmd := sm.cmd
	sm.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		// Try to kill the process
		logger.Debug("Cleaning up srvrmgr process", zap.Int("pid", cmd.Process.Pid))
		err := cmd.Process.Kill()
		if err != nil {
			logger.Warn("Error killing process during cleanup", zap.Error(err))
		} else {
			logger.Debug("Successfully killed process during cleanup")
		}
	} else {
		logger.Debug("No active process to clean up")
	}

	// Wait for output readers to complete
	logger.Debug("Waiting for output readers to complete during cleanup")
	sm.reconnectWg.Wait()
	logger.Debug("Process cleanup completed")
}
