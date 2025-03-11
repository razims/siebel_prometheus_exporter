package servermanager

import (
	"context"
	"math/rand"
	"time"

	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
	"go.uber.org/zap"
)

// startHeartbeatChecker starts a goroutine that periodically checks if the connection is still alive
func (sm *ServerManager) startHeartbeatChecker() {
	sm.mu.Lock()
	if sm.heartbeatTicker != nil {
		logger.Debug("Stopping existing heartbeat ticker")
		sm.heartbeatTicker.Stop()
	}

	autoReconnect := sm.config.AutoReconnect
	sm.mu.Unlock()

	if !autoReconnect {
		logger.Debug("Auto-reconnect disabled, not starting heartbeat checker")
		return
	}

	logger.Info("Starting heartbeat checker")

	// Start a new heartbeat ticker (every 30 seconds)
	sm.heartbeatTicker = time.NewTicker(30 * time.Second)

	go func() {
		logger.Debug("Heartbeat checker goroutine started")
		heartbeatCount := 0

		for {
			select {
			case <-sm.heartbeatTicker.C:
				heartbeatCount++
				logger.Debug("Performing heartbeat check", zap.Int("count", heartbeatCount))

				// Check if we need to perform a heartbeat
				if !sm.checkConnectionHealth() {
					logger.Warn("Connection health check failed", zap.Int("heartbeatCount", heartbeatCount))
					// Try to reconnect if the connection is unhealthy
					sm.tryReconnect()
				} else {
					logger.Debug("Connection health check passed", zap.Int("heartbeatCount", heartbeatCount))
				}
			case <-sm.stopReconnect:
				// Stop the heartbeat ticker when reconnection is disabled
				logger.Debug("Heartbeat checker received stop signal")
				if sm.heartbeatTicker != nil {
					sm.heartbeatTicker.Stop()
					logger.Debug("Heartbeat ticker stopped")
				}
				logger.Debug("Heartbeat checker goroutine exiting")
				return
			}
		}
	}()
}

// checkConnectionHealth sends a simple command to check if the connection is still alive
func (sm *ServerManager) checkConnectionHealth() bool {
	sm.mu.Lock()
	if sm.status != Connected {
		currentStatus := sm.status
		sm.mu.Unlock()
		logger.Debug("Connection health check skipped - not connected",
			zap.String("status", string(currentStatus)))
		return false
	}

	// Get a snapshot of the current values while under lock
	lastActivity := sm.lastActivity
	sm.mu.Unlock()

	// Check if there's been any activity in the last 5 minutes
	inactivityDuration := time.Since(lastActivity)

	if inactivityDuration > 5*time.Minute {
		logger.Debug("Connection inactive for too long",
			zap.Duration("inactiveDuration", inactivityDuration),
			zap.Time("lastActivity", lastActivity))

		// Try sending a ping command with a short timeout
		logger.Debug("Sending ping command to verify connection")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Use a simple command that doesn't generate much output
		startTime := time.Now()
		_, err := sm.sendCommandWithContext(ctx, "list ent")

		duration := time.Since(startTime)

		// The error could be due to pipe closed or timeout
		if err != nil {
			// Log the health check failure
			logger.Debug("Connection health check failed with error",
				zap.Duration("inactivity", inactivityDuration.Round(time.Second)),
				zap.Duration("pingTime", duration),
				zap.Error(err))
			return false
		}

		// If the command succeeded, the connection is still good
		logger.Debug("Connection health check succeeded after inactivity",
			zap.Duration("pingTime", duration))
		return true
	}

	// Recent activity means the connection is likely still good
	logger.Debug("Recent activity detected, skipping active health check",
		zap.Duration("timeSinceLastActivity", inactivityDuration))
	return true
}

// tryReconnect attempts to reconnect to the server with exponential backoff
func (sm *ServerManager) tryReconnect() {
	sm.mu.Lock()
	if sm.isReconnecting || !sm.config.AutoReconnect {
		alreadyReconnecting := sm.isReconnecting
		autoReconnectEnabled := sm.config.AutoReconnect
		sm.mu.Unlock()

		if alreadyReconnecting {
			logger.Debug("Reconnection already in progress, skipping new attempt")
		} else if !autoReconnectEnabled {
			logger.Debug("Auto-reconnect disabled, skipping reconnection attempt")
		}
		return
	}

	// Add a small delay before reconnecting
	time.Sleep(500 * time.Millisecond)

	sm.isReconnecting = true
	sm.status = Reconnecting
	backoffConfig := sm.config.BackoffConfig
	sm.mu.Unlock()

	logger.Info("Initiating reconnection with exponential backoff",
		zap.Duration("initialDelay", backoffConfig.InitialDelay),
		zap.Duration("maxDelay", backoffConfig.MaxDelay),
		zap.Float64("multiplier", backoffConfig.Multiplier),
		zap.Int("maxRetries", backoffConfig.MaxRetries))

	// Create a new stopReconnect channel
	sm.mu.Lock()
	if sm.stopReconnect == nil {
		sm.stopReconnect = make(chan struct{})
	}
	stopCh := sm.stopReconnect
	sm.mu.Unlock()

	// Clean up any existing process
	logger.Debug("Cleaning up existing process before reconnection")
	sm.cleanupProcess()

	// Start reconnection loop in a goroutine
	go func() {
		defer func() {
			sm.mu.Lock()
			previousReconnecting := sm.isReconnecting
			sm.isReconnecting = false
			sm.mu.Unlock()
			logger.Debug("Exiting reconnection loop",
				zap.Bool("wasReconnecting", previousReconnecting))
		}()

		currentDelay := backoffConfig.InitialDelay
		retryCount := 0

		// Initialize random number generator for jitter
		rand.Seed(time.Now().UnixNano())

		for {
			if retryCount >= backoffConfig.MaxRetries && backoffConfig.MaxRetries > 0 {
				logger.Error("Maximum reconnection attempts reached",
					zap.Int("maxRetries", backoffConfig.MaxRetries),
					zap.Int("actualAttempts", retryCount))
				sm.setStatus(ConnectionError)
				return
			}

			select {
			case <-stopCh:
				// Stop reconnection attempt
				logger.Debug("Reconnection attempt cancelled")
				return
			default:
				// Try to connect
				logger.Info("Attempting reconnection",
					zap.Int("attempt", retryCount+1),
					zap.Int("maxRetries", backoffConfig.MaxRetries),
					zap.Duration("currentDelay", currentDelay))

				startTime := time.Now()
				err := sm.connect()
				duration := time.Since(startTime)

				if err == nil {
					logger.Info("Successfully reconnected to Siebel Server Manager",
						zap.Int("attemptsTaken", retryCount+1),
						zap.Duration("reconnectTime", duration))
					return
				}

				retryCount++

				// Calculate next delay with jitter
				jitter := 1.0
				if backoffConfig.JitterFactor > 0 {
					// Add random jitter between -JitterFactor and +JitterFactor
					jitter = 1.0 + (rand.Float64()*2.0-1.0)*backoffConfig.JitterFactor
				}

				nextDelay := time.Duration(float64(currentDelay) * backoffConfig.Multiplier * jitter)
				if nextDelay > backoffConfig.MaxDelay {
					nextDelay = backoffConfig.MaxDelay
				}

				logger.Warn("Reconnection failed, will retry with backoff",
					zap.Error(err),
					zap.Int("attempt", retryCount),
					zap.Int("maxRetries", backoffConfig.MaxRetries),
					zap.Duration("connectionAttemptTime", duration),
					zap.Duration("nextDelay", nextDelay),
					zap.Float64("jitterFactor", jitter))

				currentDelay = nextDelay

				// Wait before retry
				logger.Debug("Waiting before next reconnection attempt",
					zap.Duration("delay", currentDelay))

				select {
				case <-stopCh:
					logger.Debug("Reconnection attempt cancelled during delay")
					return
				case <-time.After(currentDelay):
					// Continue with next attempt
					logger.Debug("Delay completed, proceeding with next reconnection attempt")
				}
			}
		}
	}()
}

// ForceReconnect forces a reconnection attempt
func (sm *ServerManager) ForceReconnect() error {
	logger.Info("Force reconnection requested")

	// Disconnect first
	logger.Debug("Cleaning up process before force reconnect")
	sm.cleanupProcess()

	// Then attempt to reconnect
	logger.Debug("Initiating connection after force reconnect")
	err := sm.connect()

	if err != nil {
		logger.Error("Force reconnection failed", zap.Error(err))
	} else {
		logger.Info("Force reconnection successful")
	}

	return err
}
