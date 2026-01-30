package installer

import "time"

// Timeout constants for installer operations
const (
	// Default timeout for waiting for components to be ready
	DefaultReadyTimeout = 5 * time.Minute

	// Shorter timeout for lighter components
	ShortReadyTimeout = 3 * time.Minute

	// Poll intervals for checking component status
	DefaultPollInterval = 10 * time.Second
	ShortPollInterval   = 5 * time.Second
	LongPollInterval    = 15 * time.Second

	// Initial delays before checking status
	CRDInitialDelay       = 20 * time.Second
	MetalLBConfigureDelay = 30 * time.Second
	MonitoringInitDelay   = 15 * time.Second
)