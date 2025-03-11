package servermanager

import "time"

// Status represents the connection status of the ServerManager
type Status string

// Define constants for status values
const (
	Disconnected    Status = "Disconnected"
	Connecting      Status = "Connecting"
	Disconnecting   Status = "Disconnecting"
	Connected       Status = "Connected"
	ConnectionError Status = "ConnectionError"
	Reconnecting    Status = "Reconnecting"

	// Default timeout duration
	DefaultTimeout        = 60 * time.Second
	DefaultReconnectDelay = 10 * time.Second
)

// BackoffConfig defines the configuration for exponential backoff
type BackoffConfig struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
	MaxRetries   int
	JitterFactor float64
}

// Default backoff configuration
var DefaultBackoffConfig = BackoffConfig{
	InitialDelay: 5 * time.Second,
	MaxDelay:     5 * time.Minute,
	Multiplier:   1.5,
	MaxRetries:   10,
	JitterFactor: 0.2,
}

// ServerManagerConfig contains all configuration parameters for ServerManager
type ServerManagerConfig struct {
	// Connection parameters
	Gateway    string
	Enterprise string
	Server     string
	User       string
	Password   string

	// Path to the srvrmgr executable
	SrvrmgrPath string

	// Reconnection settings
	AutoReconnect  bool
	ReconnectDelay time.Duration

	// Backoff configuration for reconnection attempts
	BackoffConfig BackoffConfig
}

// NewConfig creates a new ServerManagerConfig with default values
func NewConfig() ServerManagerConfig {
	return ServerManagerConfig{
		AutoReconnect:  false,
		ReconnectDelay: DefaultReconnectDelay,
		BackoffConfig:  DefaultBackoffConfig,
	}
}
