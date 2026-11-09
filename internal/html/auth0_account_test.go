package main

import (
	"ref-ledger-v2/internal/model"
	"testing"
)

func TestAuth0AccountEligibility(t *testing.T) {
	verified, unverified := true, false
	for _, tc := range []struct {
		name    string
		user    model.User
		allowed bool
	}{
		{"verified", model.User{Username: "user@example.com", TenantID: "tenant", Role: "user", EmailVerified: &verified}, true},
		{"migration", model.User{Username: "user@example.com", TenantID: "tenant", Role: "readonly"}, true},
		{"unverified", model.User{Username: "user@example.com", TenantID: "tenant", Role: "admin", EmailVerified: &unverified}, false},
		{"disabled role", model.User{Username: "user@example.com", TenantID: "tenant", Role: "disabled"}, false},
		{"missing tenant", model.User{Username: "user@example.com", Role: "user"}, false},
		{"placeholder tenant", model.User{Username: "user@example.com", TenantID: "na", Role: "user"}, false},
		{"missing username", model.User{TenantID: "tenant", Role: "user"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth0AccountAllowed(tc.user); got != tc.allowed {
				t.Fatalf("allowed=%v, want %v", got, tc.allowed)
			}
		})
	}
}
