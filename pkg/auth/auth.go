package auth

import (
	"context"
	"errors"
)

// Errors
var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrInvalidToken    = errors.New("invalid token")
	ErrExpiredToken    = errors.New("token expired")
)

// Claims represents the claims extracted from a token.
type Claims struct {
	Subject  string   `json:"sub"`
	Issuer   string   `json:"iss"`
	Audience []string `json:"aud"`
	Scopes   []string `json:"scopes"`
	Groups   []string `json:"groups"`
}

// Authenticator is the interface for parsing and validating tokens.
type Authenticator interface {
	// Authenticate validates a token and returns the claims.
	Authenticate(ctx context.Context, token string) (*Claims, error)
	// Name returns the name of the authenticator (e.g., "jwt").
	Name() string
}

type contextKey string

const claimsKey = contextKey("claims")

// ContextWithClaims returns a new context with the given claims.
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// FromContext extracts claims from a context.
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	return claims, ok
}
