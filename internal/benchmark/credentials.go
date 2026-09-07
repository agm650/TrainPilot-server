package benchmark

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const CredentialsSchemaVersion = 1

type CredentialsFile struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Accounts      []CredentialAccount `json:"accounts"`
}

type CredentialAccount struct {
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	PasswordEnv string `json:"passwordEnv,omitempty"`
}

type Credential struct {
	Username string
	Password string
}

func LoadCredentials(path string) ([]Credential, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat credentials: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("credentials file permissions are %04o; want 0600 or stricter", info.Mode().Perm())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open credentials: %w", err)
	}
	defer f.Close()
	return DecodeCredentials(f, os.LookupEnv)
}

func DecodeCredentials(r io.Reader, lookupEnv func(string) (string, bool)) ([]Credential, error) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var input CredentialsFile
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode credentials: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if input.SchemaVersion != CredentialsSchemaVersion {
		return nil, fmt.Errorf("unsupported credentials schemaVersion %d (want %d)", input.SchemaVersion, CredentialsSchemaVersion)
	}
	if len(input.Accounts) == 0 {
		return nil, errors.New("credentials must contain at least one account")
	}
	seen := make(map[string]struct{}, len(input.Accounts))
	credentials := make([]Credential, 0, len(input.Accounts))
	for index, account := range input.Accounts {
		username := strings.TrimSpace(account.Username)
		if username == "" {
			return nil, fmt.Errorf("accounts[%d].username is required", index)
		}
		if _, exists := seen[username]; exists {
			return nil, fmt.Errorf("duplicate credentials for username %q", username)
		}
		seen[username] = struct{}{}
		if (account.Password == "") == (account.PasswordEnv == "") {
			return nil, fmt.Errorf("accounts[%d] must set exactly one of password or passwordEnv", index)
		}
		password := account.Password
		if account.PasswordEnv != "" {
			var ok bool
			password, ok = lookupEnv(account.PasswordEnv)
			if !ok || password == "" {
				return nil, fmt.Errorf("environment variable %q is empty or unset", account.PasswordEnv)
			}
		}
		credentials = append(credentials, Credential{Username: username, Password: password})
	}
	return credentials, nil
}

func ParseEnvironmentCredentials(values []string, lookupEnv func(string) (string, bool)) ([]Credential, error) {
	credentials := make([]Credential, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		username, variable, ok := strings.Cut(value, "=")
		username = strings.TrimSpace(username)
		variable = strings.TrimSpace(variable)
		if !ok || username == "" || variable == "" {
			return nil, fmt.Errorf("invalid credential %q; want username=ENV_VAR", value)
		}
		if _, exists := seen[username]; exists {
			return nil, fmt.Errorf("duplicate credentials for username %q", username)
		}
		seen[username] = struct{}{}
		password, found := lookupEnv(variable)
		if !found || password == "" {
			return nil, fmt.Errorf("environment variable %q is empty or unset", variable)
		}
		credentials = append(credentials, Credential{Username: username, Password: password})
	}
	if len(credentials) == 0 {
		return nil, errors.New("at least one credential is required")
	}
	return credentials, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("input must contain one JSON document")
		}
		return err
	}
	return nil
}
