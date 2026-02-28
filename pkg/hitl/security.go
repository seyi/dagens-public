package hitl

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// SecurityManager handles security-related operations
type SecurityManager struct {
	callbackSecret []byte
}

// NewSecurityManager creates a new security manager
func NewSecurityManager(secret []byte) *SecurityManager {
	if len(secret) == 0 {
		// Generate a random secret if none provided (for development)
		secret = make([]byte, 32) // 256 bits
		if _, err := rand.Read(secret); err != nil {
			panic("failed to generate random secret: " + err.Error())
		}
	}
	return &SecurityManager{
		callbackSecret: secret,
	}
}

// GenerateCallbackURL creates a secure, signed callback URL
func (s *SecurityManager) GenerateCallbackURL(baseURL, requestID string) string {
	timestamp := time.Now().Unix()
	payload := fmt.Sprintf("%s:%d", requestID, timestamp)
	signature := s.signHMAC(payload)

	return fmt.Sprintf("%s/api/human-callback?req=%s&ts=%d&sig=%s",
		baseURL, requestID, timestamp, signature)
}

// VerifyCallbackSignature verifies the signature of a callback request
func (s *SecurityManager) VerifyCallbackSignature(requestID string, timestamp int64, signature string) bool {
	// Check timestamp freshness (prevent replay attacks)
	if time.Now().Unix()-timestamp > 300 { // 5 minute window
		return false
	}

	expectedPayload := fmt.Sprintf("%s:%d", requestID, timestamp)
	expectedSignature := s.signHMAC(expectedPayload)

	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(signature), []byte(expectedSignature)) == 1
}

// signHMAC generates an HMAC signature
func (s *SecurityManager) signHMAC(payload string) string {
	h := hmac.New(sha256.New, s.callbackSecret)
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// SignHMAC signs a payload with HMAC
func (s *SecurityManager) SignHMAC(payload string) string {
	return s.signHMAC(payload)
}

// VerifyHMACSignature verifies an HMAC signature
func (s *SecurityManager) VerifyHMACSignature(payload, signature string) bool {
	expectedSignature := s.SignHMAC(payload)
	return subtle.ConstantTimeCompare([]byte(signature), []byte(expectedSignature)) == 1
}

// SecretRotationManager handles secret rotation
type SecretRotationManager struct {
	currentSecret  []byte
	previousSecret []byte // For handling in-flight requests during rotation
	rotationTime   time.Time
}

// NewSecretRotationManager creates a new secret rotation manager
func NewSecretRotationManager(currentSecret []byte) *SecretRotationManager {
	return &SecretRotationManager{
		currentSecret: currentSecret,
		rotationTime:  time.Now(),
	}
}

// RotateSecret rotates to a new secret
func (s *SecretRotationManager) RotateSecret(newSecret []byte) {
	s.previousSecret = s.currentSecret
	s.currentSecret = newSecret
	s.rotationTime = time.Now()
}

// GetCurrentSecret returns the current secret
func (s *SecretRotationManager) GetCurrentSecret() []byte {
	return s.currentSecret
}

// GetPreviousSecret returns the previous secret (for in-flight requests)
func (s *SecretRotationManager) GetPreviousSecret() []byte {
	return s.previousSecret
}

// IsWithinRotationWindow checks if a request is within the rotation window
func (s *SecretRotationManager) IsWithinRotationWindow(requestTime time.Time) bool {
	// Allow requests with old secret for a limited time after rotation
	return requestTime.After(s.rotationTime.Add(-5*time.Minute)) &&
		requestTime.Before(s.rotationTime.Add(30*time.Minute))
}

// StateValidator validates state serialization for security
type StateValidator struct {
	// In a real implementation, this would contain validation rules
	// to ensure state doesn't contain dangerous types like functions, channels, etc.
}

// NewStateValidator creates a new state validator
func NewStateValidator() *StateValidator {
	return &StateValidator{}
}

// ValidateState checks if the state is safe for serialization
func (s *StateValidator) ValidateState(state graph.State) error {
	// In a real implementation, validate that the state doesn't contain
	// dangerous types that can't be properly serialized/deserialized
	// and that could pose security risks

	// For now, just return nil (no validation)
	return nil
}

// ValidateStateSnapshot checks if a state snapshot is safe
func (s *StateValidator) ValidateStateSnapshot(snapshot *graph.StateSnapshot) error {
	// Validate the snapshot before storing or transmitting
	return nil
}
