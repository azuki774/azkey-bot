package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromEnvRequiresValues(t *testing.T) {
	rulesPath := writeRulesFile(t, `{"version":1,"rules":[]}`)

	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "missing base URL",
			env: map[string]string{
				"MISSKEY_TOKEN": "test-token",
				"RULES_FILE":    rulesPath,
			},
		},
		{
			name: "blank base URL",
			env: map[string]string{
				"MISSKEY_BASE_URL": " \t",
				"MISSKEY_TOKEN":    "test-token",
				"RULES_FILE":       rulesPath,
			},
		},
		{
			name: "missing token",
			env: map[string]string{
				"MISSKEY_BASE_URL": "https://misskey.example.test",
				"RULES_FILE":       rulesPath,
			},
		},
		{
			name: "blank token",
			env: map[string]string{
				"MISSKEY_BASE_URL": "https://misskey.example.test",
				"MISSKEY_TOKEN":    "\n",
				"RULES_FILE":       rulesPath,
			},
		},
		{
			name: "missing rules file",
			env: map[string]string{
				"MISSKEY_BASE_URL": "https://misskey.example.test",
				"MISSKEY_TOKEN":    "test-token",
			},
		},
		{
			name: "blank rules file",
			env: map[string]string{
				"MISSKEY_BASE_URL": "https://misskey.example.test",
				"MISSKEY_TOKEN":    "test-token",
				"RULES_FILE":       " ",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadFromEnv(mapLookup(test.env))
			if err == nil {
				t.Fatal("LoadFromEnv returned nil error")
			}
		})
	}
}

func TestLoadFromEnvValidatesBaseURL(t *testing.T) {
	rulesPath := writeRulesFile(t, `{"version":1,"rules":[]}`)

	tests := []struct {
		name string
		url  string
	}{
		{name: "relative", url: "misskey.example.test"},
		{name: "wrong scheme", url: "ftp://misskey.example.test"},
		{name: "missing host", url: "https:///path"},
		{name: "userinfo", url: "https://user:password@misskey.example.test"},
		{name: "query", url: "https://misskey.example.test?token=secret"},
		{name: "fragment", url: "https://misskey.example.test#secret"},
		{name: "malformed", url: "https://[::1"},
		{name: "invalid port", url: "https://misskey.example.test:65536"},
		{name: "leading whitespace", url: " https://misskey.example.test"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := map[string]string{
				"MISSKEY_BASE_URL": test.url,
				"MISSKEY_TOKEN":    "test-token",
				"RULES_FILE":       rulesPath,
			}
			_, err := LoadFromEnv(mapLookup(env))
			if err == nil {
				t.Fatal("LoadFromEnv returned nil error")
			}
			if strings.Contains(err.Error(), test.url) {
				t.Fatalf("error contains raw URL: %q", err)
			}
		})
	}
}

func TestLoadFromEnvAcceptsHTTPAndHTTPS(t *testing.T) {
	rulesPath := writeRulesFile(t, "\n{\n  \"version\": 1,\n  \"rules\": []\n}\n")

	for _, baseURL := range []string{
		"http://127.0.0.1:0/base",
		"https://misskey.example.test",
	} {
		t.Run(baseURL, func(t *testing.T) {
			env := map[string]string{
				"MISSKEY_BASE_URL": baseURL,
				"MISSKEY_TOKEN":    "test-token",
				"RULES_FILE":       rulesPath,
			}
			cfg, err := LoadFromEnv(mapLookup(env))
			if err != nil {
				t.Fatalf("LoadFromEnv returned error: %v", err)
			}
			if got := cfg.BaseURL().String(); got != baseURL {
				t.Fatalf("BaseURL() = %q, want %q", got, baseURL)
			}
		})
	}
}

func TestLoadFromEnvRulesSchema(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "valid", content: `{"version":1,"rules":[]}`},
		{name: "valid with whitespace", content: " \n{\"version\":1,\"rules\":[]}\n\t"},
		{name: "invalid JSON", content: `{"version":1,"rules":[}`, wantErr: true},
		{name: "top-level array", content: `[]`, wantErr: true},
		{name: "top-level null", content: `null`, wantErr: true},
		{name: "top-level string", content: `"rules"`, wantErr: true},
		{name: "unknown field", content: `{"version":1,"rules":[],"extra":true}`, wantErr: true},
		{name: "case-variant version", content: `{"Version":1,"rules":[]}`, wantErr: true},
		{name: "case-variant rules", content: `{"version":1,"Rules":[]}`, wantErr: true},
		{name: "duplicate rules", content: `{"version":1,"rules":[{}],"rules":[]}`, wantErr: true},
		{name: "duplicate version", content: `{"version":1,"version":1,"rules":[]}`, wantErr: true},
		{name: "duplicate nested key", content: `{"version":1,"rules":[{"name":1,"name":2}]}`, wantErr: true},
		{name: "trailing JSON", content: `{"version":1,"rules":[]} {}`, wantErr: true},
		{name: "missing version", content: `{"rules":[]}`, wantErr: true},
		{name: "missing rules", content: `{"version":1}`, wantErr: true},
		{name: "null version", content: `{"version":null,"rules":[]}`, wantErr: true},
		{name: "null rules", content: `{"version":1,"rules":null}`, wantErr: true},
		{name: "unsupported version", content: `{"version":2,"rules":[]}`, wantErr: true},
		{name: "unsupported rules", content: `{"version":1,"rules":[{"name":"future"}]}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			secret := "rules-secret-for-" + strings.ReplaceAll(test.name, " ", "-")
			content := strings.ReplaceAll(test.content, "future", secret)
			path := writeRulesFile(t, content)
			env := map[string]string{
				"MISSKEY_BASE_URL": "https://misskey.example.test",
				"MISSKEY_TOKEN":    "token-that-must-not-appear",
				"RULES_FILE":       path,
			}
			_, err := LoadFromEnv(mapLookup(env))
			if test.wantErr && err == nil {
				t.Fatal("LoadFromEnv returned nil error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("LoadFromEnv returned error: %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), env["MISSKEY_TOKEN"])) {
				t.Fatalf("error contains sensitive input: %q", err)
			}
		})
	}
}

func TestLoadFromEnvMissingRulesFileDoesNotExposePath(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "rules-secret-path.json")
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://misskey.example.test",
		"MISSKEY_TOKEN":    "token-that-must-not-appear",
		"RULES_FILE":       secretPath,
	}

	_, err := LoadFromEnv(mapLookup(env))
	if err == nil {
		t.Fatal("LoadFromEnv returned nil error")
	}
	if strings.Contains(err.Error(), secretPath) || strings.Contains(err.Error(), env["MISSKEY_TOKEN"]) {
		t.Fatalf("error contains sensitive input: %q", err)
	}
}

func writeRulesFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write rules file: %v", err)
	}
	return path
}

func mapLookup(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
