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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seyi/dagens/pkg/auth"
)

func TestAuthMiddleware(t *testing.T) {
	claims := &auth.Claims{Subject: "test-subject"}
	authenticator := &mockAuthenticator{claims: claims}
	middleware := AuthMiddleware(authenticator)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retrievedClaims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			t.Fatal("handler called with context without claims")
		}
		if retrievedClaims.Subject != claims.Subject {
			t.Errorf("handler called with incorrect claims")
		}
		w.WriteHeader(http.StatusOK)
	})

	// Test case 1: Valid token
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()
	middleware(handler).ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Test case 2: Invalid token
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr = httptest.NewRecorder()
	middleware(handler).ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}

	// Test case 3: No token
	req = httptest.NewRequest("GET", "/", nil)
	rr = httptest.NewRecorder()
	middleware(handler).ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}

	// Test case 4: Authenticator error
	authenticator.err = fmt.Errorf("authenticator-error")
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr = httptest.NewRecorder()
	middleware(handler).ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}
