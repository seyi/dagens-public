# Dagens Auth Module Design

This document outlines the architectural design for the authentication and authorization module (`pkg/auth`) in the Dagens project.

## 1. Goals

-   Provide a unified authentication mechanism for both gRPC and HTTP endpoints.
-   Support JWT/OIDC-based authentication.
-   Enable easy extraction of claims for authorization and context.
-   Be extensible to support other authentication methods in the future.

## 2. Core Components

### 2.1. Authenticator Interface

The core of the auth module will be the `Authenticator` interface. This interface will provide a standard way to parse and validate tokens.

```go
// pkg/auth/auth.go

package auth

import "context"

// Claims represents the claims extracted from a token.
type Claims struct {
    Subject   string
    Issuer    string
    Audience  []string
    Scopes    []string
    // Add other relevant claims here
}

// Authenticator is the interface for parsing and validating tokens.
type Authenticator interface {
    // Authenticate validates a token and returns the claims.
    Authenticate(ctx context.Context, token string) (*Claims, error)
}
```

### 2.2. OIDC/JWT Authenticator

The primary implementation of the `Authenticator` interface will be for OIDC/JWT. It will use a library like `coreos/go-oidc` to handle the complexities of OIDC discovery and JWT validation.

```go
// pkg/auth/jwt.go

package auth

import "context"

// NewOIDCAuthenticator creates a new Authenticator for OIDC/JWT.
func NewOIDCAuthenticator(ctx context.Context, issuerURL string, clientID string) (Authenticator, error) {
    // ... implementation using coreos/go-oidc
}
```

## 3. Middleware

The authentication logic will be integrated into the server via middleware for both gRPC and HTTP.

### 3.1. gRPC Middleware

A gRPC interceptor will extract the token from the incoming request metadata, use the `Authenticator` to validate it, and then inject the claims into the request context.

```go
// pkg/grpc/auth_interceptor.go

package grpc

import (
    "context"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    "github.com/seyi/dagens/pkg/auth"
)

func AuthInterceptor(authenticator auth.Authenticator) grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
        // ... extract token from metadata
        // ... call authenticator.Authenticate(ctx, token)
        // ... inject claims into new context
        // ... call handler(newCtx, req)
    }
}
```

### 3.2. HTTP Middleware

Similarly, an HTTP middleware will extract the token from the `Authorization` header, validate it, and inject the claims into the request context.

```go
// pkg/grpc/auth_middleware.go

package grpc

import (
    "net/http"
    "github.com/seyi/dagens/pkg/auth"
)

func AuthMiddleware(authenticator auth.Authenticator, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // ... extract token from header
        // ... call authenticator.Authenticate(r.Context(), token)
        // ... inject claims into new context
        // ... call next.ServeHTTP(w, r.WithContext(newCtx))
    })
}
```

## 4. Context Integration

A helper function will be provided to extract claims from the context.

```go
// pkg/auth/context.go

package auth

import "context"

type contextKey string

const claimsKey = contextKey("claims")

// ContextWithClaims returns a new context with the given claims.
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
    return context.WithValue(ctx, claimsKey, claims)
}

// ClaimsFromContext extracts claims from a context.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
    claims, ok := ctx.Value(claimsKey).(*Claims)
    return claims, ok
}
```
