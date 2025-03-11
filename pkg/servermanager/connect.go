package servermanager

import (
	"bufio"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/razims/siebel_exporter/pkg/logger"
	"go.uber.org/zap"
)

// connect is the internal connection method that uses stored config
func (sm *ServerManager) connect() error {
	sm.mu.Lock()
	if sm.status == Connecting || sm.status == Connected {
		sm.mu.Unlock()
		return errors.New("already connected or connecting")
	}

	sm.status = Connecting
	config := sm.config // Make a local copy to use after unlocking
	sm.mu.Unlock()

	logger.Debug("Connecting to Siebel Server Manager",
		zap.String("gateway", config.Gateway),
		zap.String("enterprise", config.Enterprise),
		zap.String("server", config.Server))

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

	stdinPipe, err := sm.cmd.StdinPipe()
	if err != nil {
		sm.setStatus(ConnectionError)
		return fmt.Errorf("stdin error: %v", err)
	}

	stdoutPipe, err := sm.cmd.StdoutPipe()
	if err != nil {
		sm.setStatus(ConnectionError)
		return fmt.Errorf("stdout error: %v", err)
	}

	stderrPipe, err := sm.cmd.StderrPipe()
	if err != nil {
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

	if err := sm.cmd.Start(); err != nil {
		sm.setStatus(ConnectionError)
		return fmt.Errorf("error starting srvrmgr: %v", err)
	}

	// Start goroutines to continuously read stdout and stderr
	sm.reconnectWg.Add(2)

	// Reading from stdout
	go func() {
		defer sm.reconnectWg.Done()
		sm.readOutput(sm.stdout, &sm.stdoutOutput)
	}()

	// Reading from stderr
	go func() {
		defer sm.reconnectWg.Done()
		sm.readOutput(sm.stderr, &sm.stderrOutput)
	}()

	// Wait for initial output to confirm connection
	time.Sleep(2 * time.Second)

	// Check for any error output that indicates connection failure
	sm.mu.Lock()
	if len(sm.stderrOutput) > 0 {
		// Check stderr for connection errors
		for _, line := range sm.stderrOutput {
			if strings.Contains(strings.ToLower(line), "error") ||
				strings.Contains(strings.ToLower(line), "failed") {
				sm.status = ConnectionError
				sm.mu.Unlock()
				logger.Error("Connection error detected in stderr output", zap.String("error", line))
				return fmt.Errorf("connection error: %s", line)
			}
		}
	}

	// Set status to Connected if no errors occurred
	sm.status = Connected
	sm.lastActivity = time.Now()
	sm.mu.Unlock()

	// Start the heartbeat checker if reconnection is enabled
	sm.startHeartbeatChecker()

	logger.Info("Successfully connected to Siebel Server Manager")
	return nil
}

// Disconnect terminates the srvrmgr shell
func (sm *ServerManager) Disconnect() error {
	sm.mu.Lock()
	// Stop any reconnection attempts
	if sm.config.AutoReconnect {
		close(sm.stopReconnect)
		sm.stopReconnect = make(chan struct{})
	}

	// Stop heartbeat ticker
	if sm.heartbeatTicker != nil {
		sm.heartbeatTicker.Stop()
		sm.heartbeatTicker = nil
	}

	if sm.status == Disconnected {
		sm.mu.Unlock()
		return nil
	}

	sm.status = Disconnecting
	sm.mu.Unlock()

	logger.Debug("Disconnecting from Siebel Server Manager")

	// Try to send exit command with short timeout
	// Ignore pipe errors since we're disconnecting anyway
	_, err := sm.SendCommandWithTimeout("exit", 5*time.Second)

	// Always attempt to kill the process whether exit command succeeds or fails
	// This ensures we clean up properly even if the pipe is already closed
	if sm.cmd != nil && sm.cmd.Process != nil {
		// Kill the process if it doesn't exit gracefully or to ensure termination
		killErr := sm.cmd.Process.Kill()
		if killErr != nil && err == nil {
			// Only report kill errors if the exit command succeeded
			sm.setStatus(ConnectionError)
			return fmt.Errorf("failed to kill srvrmgr process: %v", killErr)
		}
	}

	// Wait for output readers to complete with timeout
	outputWaitChan := make(chan struct{})
	go func() {
		sm.reconnectWg.Wait()
		close(outputWaitChan)
	}()

	// Add timeout for waiting on output readers
	select {
	case <-outputWaitChan:
		// Output readers completed successfully
		logger.Debug("Output readers completed")
	case <-time.After(3 * time.Second):
		// Timed out waiting for readers - continue with cleanup
		logger.Warn("Timed out waiting for output readers to complete")
	}

	// Wait for the process to finish with a timeout
	if sm.cmd != nil {
		waitChan := make(chan error, 1)
		go func() {
			waitChan <- sm.cmd.Wait()
		}()

		select {
		case err := <-waitChan:
			if err != nil {
				// Don't fail on expected exit errors (process killed)
				if !strings.Contains(err.Error(), "process already finished") &&
					!strings.Contains(err.Error(), "signal: killed") {
					sm.setStatus(ConnectionError)
					logger.Error("Error waiting for srvrmgr process to exit", zap.Error(err))
					return fmt.Errorf("error while waiting for srvrmgr process to exit: %v", err)
				}
			}
		case <-time.After(3 * time.Second):
			if sm.cmd != nil && sm.cmd.Process != nil {
				// Force kill if wait takes too long
				logger.Warn("Timed out waiting for srvrmgr process to exit, forcing kill")
				sm.cmd.Process.Kill()
			}
		}
	}

	sm.setStatus(Disconnected)
	logger.Info("Disconnected from Siebel Server Manager")
	return nil
}

// EnableAutoReconnect enables automatic reconnection
func (sm *ServerManager) EnableAutoReconnect(delay time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.config.AutoReconnect = true
	if delay > 0 {
		sm.config.ReconnectDelay = delay
	} else if sm.config.ReconnectDelay <= 0 {
		sm.config.ReconnectDelay = DefaultReconnectDelay
	}

	logger.Info("Auto-reconnect enabled", zap.Duration("delay", sm.config.ReconnectDelay))
}

// DisableAutoReconnect disables automatic reconnection
func (sm *ServerManager) DisableAutoReconnect() {
	sm.mu.Lock()
	sm.config.AutoReconnect = false
	sm.mu.Unlock()

	// Stop any ongoing reconnection attempts
	if sm.stopReconnect != nil {
		close(sm.stopReconnect)
		sm.stopReconnect = make(chan struct{})
	}

	logger.Info("Auto-reconnect disabled")
}

// cleanupProcess cleans up the existing process without changing user-facing status
func (sm *ServerManager) cleanupProcess() {
	sm.mu.Lock()
	cmd := sm.cmd
	sm.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		// Try to kill the process
		logger.Debug("Cleaning up process")
		_ = cmd.Process.Kill()
	}

	// Wait for output readers to complete
	sm.reconnectWg.Wait()
	logger.Debug("Process cleanup completed")
}
