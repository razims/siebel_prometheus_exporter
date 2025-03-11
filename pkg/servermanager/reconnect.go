package servermanager

import (
	"context"
	"fmt"
	"time"
)

// startHeartbeatChecker starts a goroutine that periodically checks if the connection is still alive
func (sm *ServerManager) startHeartbeatChecker() {
	sm.mu.Lock()
	if sm.heartbeatTicker != nil {
		sm.heartbeatTicker.Stop()
	}

	autoReconnect := sm.config.AutoReconnect
	sm.mu.Unlock()

	if !autoReconnect {
		return
	}

	// Start a new heartbeat ticker
	sm.heartbeatTicker = time.NewTicker(30 * time.Second)

	go func() {
		for {
			select {
			case <-sm.heartbeatTicker.C:
				// Check if we need to perform a heartbeat
				if !sm.checkConnectionHealth() {
					// Try to reconnect if the connection is unhealthy
					sm.tryReconnect()
				}
			case <-sm.stopReconnect:
				// Stop the heartbeat ticker when reconnection is disabled
				if sm.heartbeatTicker != nil {
					sm.heartbeatTicker.Stop()
				}
				return
			}
		}
	}()
}

// checkConnectionHealth sends a simple command to check if the connection is still alive
func (sm *ServerManager) checkConnectionHealth() bool {
	sm.mu.Lock()
	if sm.status != Connected {
		sm.mu.Unlock()
		return false
	}

	// Get a snapshot of the current values while under lock
	lastActivity := sm.lastActivity
	sm.mu.Unlock()

	// Check if there's been any activity in the last 5 minutes
	inactivityDuration := time.Since(lastActivity)
	if inactivityDuration > 5*time.Minute {
		// Try sending a ping command with a short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Use a simple command that doesn't generate much output
		_, err := sm.sendCommandWithContext(ctx, "list ent")

		// The error could be due to pipe closed or timeout
		if err != nil {
			// Log the health check failure
			fmt.Printf("Connection health check failed after %v of inactivity: %v\n",
				inactivityDuration.Round(time.Second), err)
			return false
		}

		// If the command succeeded, the connection is still good
		return true
	}

	// Recent activity means the connection is likely still good
	return true
}

// tryReconnect attempts to reconnect to the server
func (sm *ServerManager) tryReconnect() {
	sm.mu.Lock()
	if sm.isReconnecting || !sm.config.AutoReconnect {
		sm.mu.Unlock()
		return
	}

	// Add a small delay before reconnecting to avoid rapid reconnection attempts
	// This helps in situations where the pipe is being closed but process cleanup isn't complete
	time.Sleep(500 * time.Millisecond)

	sm.isReconnecting = true
	sm.status = Reconnecting
	sm.mu.Unlock()

	fmt.Println("Attempting to reconnect to Siebel Server Manager...")

	// Create a new stopReconnect channel
	sm.mu.Lock()
	if sm.stopReconnect == nil {
		sm.stopReconnect = make(chan struct{})
	}
	stopCh := sm.stopReconnect
	delay := sm.config.ReconnectDelay
	sm.mu.Unlock()

	// Clean up any existing process
	sm.cleanupProcess()

	// Start reconnection loop in a goroutine
	go func() {
		defer func() {
			sm.mu.Lock()
			sm.isReconnecting = false
			sm.mu.Unlock()
		}()

		for {
			select {
			case <-stopCh:
				// Stop reconnection attempt
				return
			default:
				// Try to connect
				err := sm.connect()
				if err == nil {
					fmt.Println("Successfully reconnected to Siebel Server Manager")
					return
				}

				fmt.Printf("Reconnection failed: %v. Retrying in %v...\n", err, delay)

				// Wait before retry
				select {
				case <-stopCh:
					return
				case <-time.After(delay):
					// Continue with next attempt
				}
			}
		}
	}()
}

// ForceReconnect forces a reconnection attempt
func (sm *ServerManager) ForceReconnect() error {
	// Disconnect first
	sm.cleanupProcess()

	// Then attempt to reconnect
	return sm.connect()
}
