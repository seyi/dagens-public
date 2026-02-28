package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const jwtAuthenticatorName = "jwt"

// JWTAuthenticator implements the Authenticator interface using JWT.
type JWTAuthenticator struct {
	secretKey []byte
	issuer    string
	audience  string
}

// NewJWTAuthenticator creates a new JWT authenticator.
func NewJWTAuthenticator(secretKey string, issuer string, audience string) *JWTAuthenticator {
	return &JWTAuthenticator{
		secretKey: []byte(secretKey),
		issuer:    issuer,
		audience:  audience,
	}
}

// Authenticate validates a JWT token and returns the claims.
func (a *JWTAuthenticator) Authenticate(ctx context.Context, tokenStr string) (*Claims, error) {
	// Remove "Bearer " prefix if present
	tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")
	tokenStr = strings.TrimSpace(tokenStr)

	if tokenStr == "" {
		return nil, ErrUnauthenticated
	}

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.secretKey, nil
	})

	if err != nil {
		if strings.Contains(err.Error(), "expired") {
			return nil, ErrExpiredToken
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	if !token.Valid {
		return nil, ErrInvalidToken
	}

	// Extract claims
	jwtClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalidToken
	}

	claims := &Claims{}
	if sub, ok := jwtClaims["sub"].(string); ok {
		claims.Subject = sub
	}
	if iss, ok := jwtClaims["iss"].(string); ok {
		claims.Issuer = iss
	}

	// Handle audience which can be string or []interface{}
	if aud, ok := jwtClaims["aud"]; ok {
		switch v := aud.(type) {
		case string:
			claims.Audience = []string{v}
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					claims.Audience = append(claims.Audience, s)
				}
			}
		}
	}

	// Handle scopes and groups
	if scopes, ok := jwtClaims["scopes"].([]interface{}); ok {
		for _, s := range scopes {
			if str, ok := s.(string); ok {
				claims.Scopes = append(claims.Scopes, str)
			}
		}
	}
	if groups, ok := jwtClaims["groups"].([]interface{}); ok {
		for _, g := range groups {
			if str, ok := g.(string); ok {
				claims.Groups = append(claims.Groups, str)
			}
		}
	}

	// Validate issuer and audience if configured
	if a.issuer != "" && claims.Issuer != a.issuer {
		return nil, fmt.Errorf("%w: invalid issuer", ErrInvalidToken)
	}
	if a.audience != "" {
		found := false
		for _, aud := range claims.Audience {
			if aud == a.audience {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: invalid audience", ErrInvalidToken)
		}
	}

	return claims, nil
}

// Name returns the name of the authenticator.
func (a *JWTAuthenticator) Name() string {
	return jwtAuthenticatorName
}
