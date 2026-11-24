package database

import (
	"net/url"
	"testing"
)

func TestBuildAtlasURIUsesAdminAuthenticationAndTLS(t *testing.T) {
	t.Setenv("MONGODB_USERNAME", "monitor-test@example.test")
	t.Setenv("MONGODB_PASSWORD", "password-with-special-characters")
	t.Setenv("MONGODB_HOST", "cluster.example.test")
	t.Setenv("MONGODB_APP_NAME", "ref-ledger-test")
	t.Setenv("MONGODB_NAME", "refLedger_test")

	uri, isAtlas, err := BuildAtlasURI()
	if err != nil {
		t.Fatal(err)
	}
	if !isAtlas {
		t.Fatal("BuildAtlasURI did not identify an Atlas connection")
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("authSource") != "admin" || parsed.Query().Get("tls") != "true" {
		t.Fatalf("Atlas URI missing explicit authentication or TLS settings")
	}
	if parsed.Host != "cluster.example.test" || parsed.Path != "/refLedger_test" {
		t.Fatalf("Atlas URI selected the wrong host or database")
	}
	if parsed.User == nil {
		t.Fatal("Atlas URI did not include credentials")
	}
	if got, _ := parsed.User.Password(); got != "password-with-special-characters" {
		t.Fatal("Atlas URI did not preserve the configured password")
	}
}
