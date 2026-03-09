package hitl

import (
	"strings"

	"github.com/seyi/dagens/pkg/telemetry"
)

// denylistedLogKeys are removed from structured logs to avoid PII/payload leakage.
var denylistedLogKeys = map[string]struct{}{
	"payload":        {},
	"response":       {},
	"human_response": {},
	"freeform_text":  {},
	"comment":        {},
	"email":          {},
	"user_id":        {},
}

func hitlLogger() telemetry.Logger {
	return telemetry.GetGlobalTelemetry().GetLogger()
}

func safeLogFields(attrs map[string]interface{}) map[string]interface{} {
	if len(attrs) == 0 {
		return map[string]interface{}{}
	}
	safe := make(map[string]interface{}, len(attrs))
	for k, v := range attrs {
		lk := strings.ToLower(k)
		if _, blocked := denylistedLogKeys[lk]; blocked {
			continue
		}
		safe[k] = v
	}
	return safe
}
