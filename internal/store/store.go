package store

import (
	"strings"
	"time"
)

const (
	// local s3 store type
	LocalS3Store = "local_s3"
	// local hosted agents store type
	LocalHostedAgents = "local_hosted_agents"
)

type TransferInfo struct {
	BytesTransferred int64
	TransferSpeed    float64 // in MB/s
	RequestID        string
	Duration         time.Duration
}

func IsValidStore(storeType string) bool {
	switch storeType {
	case LocalS3Store, LocalHostedAgents:
		return true
	default:
		return false
	}
}

// calculateTransferSpeedMBps calculates transfer speed in MB/s (decimal megabytes)
// using the formula: bytes / duration_in_seconds / 1,000,000
func calculateTransferSpeedMBps(bytes int64, duration time.Duration) float64 {
	return float64(bytes) / duration.Seconds() / 1000 / 1000
}

// normalizePrefix ensures the prefix has the correct format
func normalizePrefix(prefix string) string {
	// Remove leading slash if present
	prefix = strings.TrimPrefix(prefix, "/")
	// Add trailing slash if not empty and doesn't have one
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix
}
