package benchmark

import (
	"strings"
	"testing"
)

func TestDecodeCredentialsResolvesEnvironmentAndRejectsAmbiguity(t *testing.T) {
	input := `{"schemaVersion":1,"accounts":[{"username":"alice","passwordEnv":"BENCH_PASSWORD"}]}`
	credentials, err := DecodeCredentials(strings.NewReader(input), func(name string) (string, bool) {
		if name == "BENCH_PASSWORD" {
			return "secret-value", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].Username != "alice" || credentials[0].Password != "secret-value" {
		t.Fatalf("credentials=%+v", credentials)
	}
	ambiguous := `{"schemaVersion":1,"accounts":[{"username":"alice","password":"x","passwordEnv":"Y"}]}`
	if _, err := DecodeCredentials(strings.NewReader(ambiguous), func(string) (string, bool) { return "y", true }); err == nil {
		t.Fatal("expected ambiguous password source error")
	}
}

func TestParseEnvironmentCredentials(t *testing.T) {
	credentials, err := ParseEnvironmentCredentials([]string{"alice=PASS_A", "bob=PASS_B"}, func(name string) (string, bool) {
		return "value-" + name, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 || credentials[1].Password != "value-PASS_B" {
		t.Fatalf("credentials=%+v", credentials)
	}
	if _, err := ParseEnvironmentCredentials([]string{"invalid"}, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("expected invalid mapping error")
	}
}
