package servermanager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
	"go.uber.org/zap"
)

// SendCommand sends a command to srvrmgr and waits for a response with default timeout
func (sm *ServerManager) SendCommand(command string) ([]string, error) {
	return sm.SendCommandWithTimeout(command, DefaultTimeout)
}

// SendCommandWithTimeout sends a command with a specified timeout
func (sm *ServerManager) SendCommandWithTimeout(command string, timeout time.Duration) ([]string, error) {
	logger.Debug("SendCommandWithTimeout called",
		zap.String("command", command),
		zap.Duration("timeout", timeout))

	// Check connection status before attempting command
	if status := sm.GetStatus(); status != Connected {
		// If we're reconnecting, wait a moment and try again
		if status == Reconnecting {
			logger.Info("Connection is currently reconnecting, waiting briefly",
				zap.Duration("waitTime", 500*time.Millisecond))

			// Wait briefly for reconnection to complete
			time.Sleep(500 * time.Millisecond)

			if sm.GetStatus() == Connected {
				// Reconnected successfully, continue with command
				logger.Info("Connection restored, proceeding with command")
			} else {
				logger.Warn("Cannot send command while reconnecting",
					zap.String("status", string(status)))
				return nil, fmt.Errorf("cannot send command while reconnecting")
			}
		} else {
			logger.Warn("Cannot send command: not connected",
				zap.String("status", string(status)))
			return nil, fmt.Errorf("cannot send command: not connected (status: %s)", status)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	startTime := time.Now()
	logger.Debug("Sending command with context",
		zap.String("command", command),
		zap.Duration("timeout", timeout))

	result, err := sm.sendCommandWithContext(ctx, command)

	duration := time.Since(startTime)
	logger.Debug("Command execution completed",
		zap.Duration("executionTime", duration),
		zap.Int("resultLineCount", len(result)),
		zap.Bool("hasError", err != nil))

	// Handle common pipe errors
	if err != nil {
		// Check if the error is related to pipe closure
		if strings.Contains(err.Error(), "pipe") ||
			strings.Contains(err.Error(), "broken pipe") ||
			strings.Contains(err.Error(), "write |1") {

			logger.Error("Pipe error detected when sending command",
				zap.String("command", command),
				zap.Error(err))

			// Only attempt reconnection if auto-reconnect is enabled
			if sm.config.AutoReconnect {
				sm.mu.Lock()
				currentStatus := sm.status
				sm.mu.Unlock()

				if currentStatus == Connected {
					// Command failed with pipe error but we thought we were connected
					logger.Warn("Pipe error detected while connected, initiating reconnection")
					go sm.tryReconnect()
				}
			}

			// Convert the error to a more user-friendly message
			return nil, fmt.Errorf("connection to srvrmgr lost: %v", err)
		}
	}

	if err == nil && len(result) > 0 {
		// Log the first few lines of the result if debug is enabled
		if logger.Log.Core().Enabled(zap.DebugLevel) {
			maxLinesToLog := 5
			linesToLog := len(result)
			if linesToLog > maxLinesToLog {
				linesToLog = maxLinesToLog
			}

			logger.Debug("Command result sample",
				zap.String("command", command),
				zap.Int("totalLines", len(result)),
				zap.Int("sampleLines", linesToLog),
				zap.Strings("resultSample", result[:linesToLog]))
		}
	}

	return result, err
}

// sendCommandWithContext sends a command to srvrmgr with context for timeout/cancellation
func (sm *ServerManager) sendCommandWithContext(ctx context.Context, command string) ([]string, error) {
	logger.Debug("Sending command with context",
		zap.String("command", command),
		zap.Duration("timeout", getRemainingTimeout(ctx)))

	sm.mu.Lock()

	// Check if we're connected before sending
	if sm.status != Connected {
		status := sm.status
		sm.mu.Unlock()
		logger.Warn("Cannot send command with context: not connected",
			zap.String("status", string(status)))
		return nil, fmt.Errorf("cannot send command: not connected (status: %s)", status)
	}

	// Update last activity time
	sm.lastActivity = time.Now()

	// Clear previous output
	sm.stdoutOutput = []string{}
	sm.stderrOutput = []string{}
	sm.mu.Unlock()

	// Write the command to stdin
	sm.mu.Lock()
	logger.Debug("Writing command to stdin")
	_, err := sm.stdin.WriteString(command + "\n")
	if err != nil {
		// Pipe closed or other write error
		sm.mu.Unlock()
		logger.Error("Error writing to stdin", zap.Error(err))
		sm.handlePipeError()
		return nil, fmt.Errorf("stdin write error: %v", err)
	}

	logger.Debug("Flushing stdin")
	err = sm.stdin.Flush()
	if err != nil {
		// Pipe closed or other flush error
		sm.mu.Unlock()
		logger.Error("Error flushing stdin", zap.Error(err))
		sm.handlePipeError()
		return nil, fmt.Errorf("stdin flush error: %v", err)
	}
	sm.mu.Unlock()
	logger.Debug("Command successfully sent to srvrmgr")

	// Loop to keep reading output until prompt is found or timeout occurs
	var output []string
	skipInitialOutput := true // Flag to skip all output before the first prompt match

	logger.Debug("Starting to poll for command output")
	pollStartTime := time.Now()
	pollCount := 0

	// Poll for output until we get the ending pattern or timeout
	for {
		select {
		case <-ctx.Done():
			duration := time.Since(pollStartTime)
			logger.Warn("Command timed out waiting for prompt",
				zap.String("command", command),
				zap.Duration("pollDuration", duration),
				zap.Int("pollCount", pollCount),
				zap.Int("currentOutputLines", len(output)))
			return output, fmt.Errorf("timeout: waiting for prompt from srvrmgr")
		case <-time.After(100 * time.Millisecond):
			pollCount++
			sm.mu.Lock()

			// Check stdout for new lines
			if len(sm.stdoutOutput) > 0 {
				line := sm.stdoutOutput[0]
				sm.stdoutOutput = sm.stdoutOutput[1:]

				// Trim whitespace from the line
				line = strings.TrimSpace(line)

				if logger.Log.Core().Enabled(zap.DebugLevel) && pollCount%100 == 0 {
					logger.Debug("Still polling for output",
						zap.Int("pollCount", pollCount),
						zap.Duration("elapsed", time.Since(pollStartTime)),
						zap.Int("outputLinesCollected", len(output)))
				}

				// Skip all output before the first prompt match
				if skipInitialOutput {
					// If we find the prompt, stop skipping
					if sm.promptStartedPattern.MatchString(line) {
						logger.Debug("Found initial prompt marker, starting to collect output")
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

					duration := time.Since(pollStartTime)
					logger.Debug("Command completed successfully",
						zap.String("command", command),
						zap.Int("outputLines", len(output)),
						zap.Duration("duration", duration),
						zap.Int("pollCount", pollCount))

					// Remove duplicates and return
					uniqueOutput := removeDuplicates(output)
					if len(uniqueOutput) != len(output) {
						logger.Debug("Removed duplicate lines from output",
							zap.Int("before", len(output)),
							zap.Int("after", len(uniqueOutput)))
					}

					return uniqueOutput, nil
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

// getRemainingTimeout gets the remaining time before the context deadline
func getRemainingTimeout(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
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
