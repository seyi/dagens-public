// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package grpc

import (
	"context"
	"fmt"
	"testing"

	"github.com/seyi/dagens/pkg/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type mockAuthenticator struct {
	claims *auth.Claims
	err    error
}

func (m *mockAuthenticator) Authenticate(ctx context.Context, token string) (*auth.Claims, error) {
	if m.err != nil {
		return nil, m.err
	}
	if token == "valid-token" {
		return m.claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

func TestAuthInterceptor(t *testing.T) {
	claims := &auth.Claims{Subject: "test-subject"}
	authenticator := &mockAuthenticator{claims: claims}
	interceptor := AuthInterceptor(authenticator)

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		retrievedClaims, ok := auth.ClaimsFromContext(ctx)
		if !ok {
			t.Fatal("handler called with context without claims")
		}
		if retrievedClaims.Subject != claims.Subject {
			t.Errorf("handler called with incorrect claims")
		}
		return "handler-response", nil
	}

	// Test case 1: Valid token
	md := metadata.New(map[string]string{"authorization": "Bearer valid-token"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	_, err := interceptor(ctx, "test-request", &grpc.UnaryServerInfo{}, handler)
	if err != nil {
		t.Fatalf("interceptor returned an error with a valid token: %v", err)
	}

	// Test case 2: Invalid token
	md = metadata.New(map[string]string{"authorization": "Bearer invalid-token"})
	ctx = metadata.NewIncomingContext(context.Background(), md)
	_, err = interceptor(ctx, "test-request", &grpc.UnaryServerInfo{}, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("interceptor did not return an unauthenticated error with an invalid token")
	}

	// Test case 3: No token
	md = metadata.New(map[string]string{})
	ctx = metadata.NewIncomingContext(context.Background(), md)
	_, err = interceptor(ctx, "test-request", &grpc.UnaryServerInfo{}, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("interceptor did not return an unauthenticated error with no token")
	}

	// Test case 4: Authenticator error
	authenticator.err = fmt.Errorf("authenticator-error")
	md = metadata.New(map[string]string{"authorization": "Bearer valid-token"})
	ctx = metadata.NewIncomingContext(context.Background(), md)
	_, err = interceptor(ctx, "test-request", &grpc.UnaryServerInfo{}, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("interceptor did not return an unauthenticated error when the authenticator fails")
	}
}
