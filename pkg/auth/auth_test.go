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

package auth

import (
	"context"
	"reflect"
	"testing"
)

func TestContextWithClaims(t *testing.T) {
	claims := &Claims{Subject: "test-subject"}
	ctx := ContextWithClaims(context.Background(), claims)

	retrievedClaims, ok := ClaimsFromContext(ctx)
	if !ok {
		t.Fatal("ClaimsFromContext returned not ok")
	}

	if !reflect.DeepEqual(claims, retrievedClaims) {
		t.Errorf("retrieved claims do not match original claims")
	}
}

func TestClaimsFromContext_NoClaims(t *testing.T) {
	_, ok := ClaimsFromContext(context.Background())
	if ok {
		t.Fatal("ClaimsFromContext returned ok on a context with no claims")
	}
}