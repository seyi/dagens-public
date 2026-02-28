// Package config provides configuration for the observability system
package config

import (
	"os"
	"strconv"
)

// ObservabilityLevel defines the level of observability
type ObservabilityLevel int

const (
	// ObservabilityNone disables all observability features
	ObservabilityNone ObservabilityLevel = iota
	
	// ObservabilityBasic enables basic logging and metrics
	ObservabilityBasic
	
	// ObservabilityFull enables full tracing, metrics, and logging
	ObservabilityFull
)

// GetObservabilityLevel reads the observability level from environment variable
func GetObservabilityLevel() ObservabilityLevel {
	levelStr := os.Getenv("DAGENS_OBSERVABILITY_LEVEL")
	
	switch levelStr {
	case "0", "none":
		return ObservabilityNone
	case "1", "basic":
		return ObservabilityBasic
	case "2", "full":
		return ObservabilityFull
	default:
		// Default to basic level if not set or invalid
		return ObservabilityBasic
	}
}

// IsObservabilityEnabled checks if observability is enabled at the given level
func IsObservabilityEnabled(requiredLevel ObservabilityLevel) bool {
	currentLevel := GetObservabilityLevel()
	return currentLevel >= requiredLevel
}

// GetObservabilityConfig returns configuration based on environment
func GetObservabilityConfig() map[string]interface{} {
	return map[string]interface{}{
		"level":           GetObservabilityLevel(),
		"enabled":         IsObservabilityEnabled(ObservabilityBasic),
		"tracing_enabled": IsObservabilityEnabled(ObservabilityFull),
		"metrics_enabled": IsObservabilityEnabled(ObservabilityBasic),
		"sampling_rate":   getSamplingRate(),
	}
}

// getSamplingRate reads the sampling rate from environment variable
func getSamplingRate() float64 {
	samplingStr := os.Getenv("DAGENS_TRACING_SAMPLING_RATE")
	if samplingStr == "" {
		return 1.0 // Default to 100% sampling
	}
	
	sampling, err := strconv.ParseFloat(samplingStr, 64)
	if err != nil {
		return 1.0 // Default to 100% sampling on error
	}
	
	if sampling < 0.0 {
		return 0.0
	}
	if sampling > 1.0 {
		return 1.0
	}
	
	return sampling
}