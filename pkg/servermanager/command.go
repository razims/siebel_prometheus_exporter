package servermanager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/razims/siebel_exporter/pkg/logger"
	"go.uber.org/zap"
)

// SendCommand sends a command to srvrmgr and waits for a response with default timeout
func (sm *ServerManager) SendCommand(command string) ([]string, error) {
	return sm.SendCommandWithTimeout(command, DefaultTimeout)
}

// SendCommandWithTimeout sends a command with a specified timeout
func (sm *ServerManager) SendCommandWithTimeout(command string, timeout time.Duration) ([]string, error) {
	// Check connection status before attempting command
	if status := sm.GetStatus(); status != Connected {
		// If we're reconnecting, wait a moment and try again
		if status == Reconnecting {
			// Wait briefly for reconnection to complete
			time.Sleep(500 * time.Millisecond)
			if sm.GetStatus() == Connected {
				// Reconnected successfully, continue with command
				logger.Debug("Connection restored, proceeding with command")
			} else {
				logger.Warn("Cannot send command while reconnecting", zap.String("status", string(status)))
				return nil, fmt.Errorf("cannot send command while reconnecting")
			}
		} else {
			logger.Warn("Cannot send command: not connected", zap.String("status", string(status)))
			return nil, fmt.Errorf("cannot send command: not connected (status: %s)", status)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := sm.sendCommandWithContext(ctx, command)

	// Handle common pipe errors
	if err != nil {
		// Check if the error is related to pipe closure
		if strings.Contains(err.Error(), "pipe") ||
			strings.Contains(err.Error(), "broken pipe") ||
			strings.Contains(err.Error(), "write |1") {

			// Only attempt reconnection if auto-reconnect is enabled
			if sm.config.AutoReconnect {
				sm.mu.Lock()
				currentStatus := sm.status
				sm.mu.Unlock()

				if currentStatus == Connected {
					// Command failed with pipe error but we thought we were connected
					logger.Warn("Pipe error detected, initiating reconnection", zap.Error(err))
					go sm.tryReconnect()
				}
			}

			// Convert the error to a more user-friendly message
			return nil, fmt.Errorf("connection to srvrmgr lost: %v", err)
		}
	}

	return result, err
}

// sendCommandWithContext sends a command to srvrmgr with context for timeout/cancellation
func (sm *ServerManager) sendCommandWithContext(ctx context.Context, command string) ([]string, error) {
	logger.Debug("Sending command", zap.String("command", command))

	sm.mu.Lock()

	// Check if we're connected before sending
	if sm.status != Connected {
		sm.mu.Unlock()
		return nil, fmt.Errorf("cannot send command: not connected (status: %s)", sm.status)
	}

	// Update last activity time
	sm.lastActivity = time.Now()

	// Clear previous output
	sm.stdoutOutput = []string{}
	sm.stderrOutput = []string{}
	sm.mu.Unlock()

	// Write the command to stdin
	sm.mu.Lock()
	_, err := sm.stdin.WriteString(command + "\n")
	if err != nil {
		// Pipe closed or other write error
		sm.mu.Unlock()
		logger.Error("Error writing to stdin", zap.Error(err))
		sm.handlePipeError()
		return nil, fmt.Errorf("stdin write error: %v", err)
	}

	err = sm.stdin.Flush()
	if err != nil {
		// Pipe closed or other flush error
		sm.mu.Unlock()
		logger.Error("Error flushing stdin", zap.Error(err))
		sm.handlePipeError()
		return nil, fmt.Errorf("stdin flush error: %v", err)
	}
	sm.mu.Unlock()

	// Loop to keep reading output until prompt is found or timeout occurs
	var output []string
	skipInitialOutput := true // Flag to skip all output before the first prompt match

	// Poll for output until we get the ending pattern or timeout
	for {
		select {
		case <-ctx.Done():
			logger.Warn("Command timed out waiting for prompt", zap.String("command", command))
			return output, fmt.Errorf("timeout: waiting for prompt from srvrmgr")
		case <-time.After(100 * time.Millisecond):
			sm.mu.Lock()

			// Check stdout for new lines
			if len(sm.stdoutOutput) > 0 {
				line := sm.stdoutOutput[0]
				sm.stdoutOutput = sm.stdoutOutput[1:]

				// Trim whitespace from the line
				line = strings.TrimSpace(line)

				// Skip all output before the first prompt match
				if skipInitialOutput {
					// If we find the prompt, stop skipping
					if sm.promptStartedPattern.MatchString(line) {
						skipInitialOutput = false
						sm.mu.Unlock()
						continue // Skip adding the first prompt
					}
					// If we haven't matched the prompt yet, skip this line
					sm.mu.Unlock()
					continue
				}

				// If we find the prompt or "rows returned." line, stop reading
				if sm.promptStartedPattern.MatchString(line) || sm.promptEndedPattern.MatchString(line) {
					// Update last activity time
					sm.lastActivity = time.Now()
					sm.mu.Unlock()
					logger.Debug("Command completed successfully",
						zap.String("command", command),
						zap.Int("outputLines", len(output)))
					return removeDuplicates(output), nil
				}

				// Add the line to the output
				output = append(output, line)
				sm.mu.Unlock()
				continue
			}

			// Check stderr for any error lines
			if len(sm.stderrOutput) > 0 {
				line := sm.stderrOutput[0]
				sm.stderrOutput = sm.stderrOutput[1:]

				// Trim whitespace before appending to output
				line = strings.TrimSpace(line)

				// Append stderr lines to output
				output = append(output, line)
				logger.Warn("Received stderr output", zap.String("line", line))
				sm.mu.Unlock()
				continue
			}

			sm.mu.Unlock()
		}
	}
}

// removeDuplicates removes duplicate strings from a slice
func removeDuplicates(input []string) []string {
	// Create a map to track unique strings
	seen := make(map[string]struct{})
	var result []string

	// Iterate through the input slice
	for _, str := range input {
		if _, exists := seen[str]; !exists {
			// If the string is not in the map, add it to result and mark it as seen
			result = append(result, str)
			seen[str] = struct{}{}
		}
	}

	return result
}
