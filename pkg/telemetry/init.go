// Package telemetry provides initialization functions for the telemetry system
package telemetry

import (
	"context"
	"os"
	"sync"
)

// InitTelemetry initializes the telemetry system with the specified configuration
// It reads from environment variables if available, otherwise uses defaults
func InitTelemetry(serviceName, serviceVersion string) (*TelemetryCollector, error) {
	config := CollectorConfig{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		// Default to in-memory for backward compatibility
		TracerType: TracerInMemory,
		// Enable metrics and logging by default
		EnableMetrics: true,
		EnableLogging: true,
	}

	// Check environment variables for configuration
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		config.Endpoint = endpoint
		config.TracerType = TracerOTEL
		config.ExporterType = ExporterOTLP
	}

	if exporterType := os.Getenv("OTEL_EXPORTER_TYPE"); exporterType != "" {
		switch exporterType {
		case "console":
			config.ExporterType = ExporterConsole
			config.TracerType = TracerOTEL
		case "otlp":
			config.ExporterType = ExporterOTLP
			config.TracerType = TracerOTEL
		case "noop":
			config.ExporterType = ExporterNoOp
			config.TracerType = TracerOTEL
		}
	}

	return NewTelemetryCollectorWithConfig(config)
}

// InitOTELTelemetry initializes the telemetry system with OTEL configuration
func InitOTELTelemetry(config TracerConfig) (*TelemetryCollector, error) {
	collectorConfig := CollectorConfig{
		TracerType:   TracerOTEL,
		TracerConfig: config,
		EnableMetrics: true,
		EnableLogging: true,
	}

	return NewTelemetryCollectorWithConfig(collectorConfig)
}

// Global telemetry collector instance
var globalCollector *TelemetryCollector
var globalCollectorOnce = &Once{}

// Once is similar to sync.Once but with error handling
type Once struct {
	done uint32
	m    sync.Mutex
}

// Do calls the function f if and only if Do has not been invoked without error before
func (o *Once) Do(f func() error) error {
	o.m.Lock()
	defer o.m.Unlock()
	if o.done == 0 {
		err := f()
		if err != nil {
			return err
		}
		o.done = 1
	}
	return nil
}

// GetGlobalTelemetry returns the global telemetry collector instance
func GetGlobalTelemetry() *TelemetryCollector {
	if globalCollector == nil {
		// Initialize with default in-memory collector for backward compatibility
		globalCollector = NewDefaultTelemetryCollector()
	}
	return globalCollector
}

// InitGlobalTelemetry initializes the global telemetry collector
func InitGlobalTelemetry(serviceName, serviceVersion string) error {
	return globalCollectorOnce.Do(func() error {
		collector, err := InitTelemetry(serviceName, serviceVersion)
		if err != nil {
			return err
		}
		globalCollector = collector
		return nil
	})
}

// ShutdownGlobalTelemetry shuts down the global telemetry collector
func ShutdownGlobalTelemetry(ctx context.Context) error {
	if globalCollector != nil {
		return globalCollector.Shutdown(ctx)
	}
	return nil
}

// ForceFlushGlobalTelemetry flushes all remaining spans in the global collector
func ForceFlushGlobalTelemetry(ctx context.Context) error {
	if globalCollector != nil {
		return globalCollector.ForceFlush(ctx)
	}
	return nil
}