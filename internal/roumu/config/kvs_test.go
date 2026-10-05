package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKVSSecretSourcesAndRedaction(t *testing.T) {
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://example.test", "MISSKEY_TOKEN": "misskey-secret",
		"KVS_BACKEND": "valkey", "KVS_NAMESPACE": "production-bot",
		"KVS_URL": "rediss://valkey.example.test:6379", "KVS_USERNAME": "bot",
		"KVS_PASSWORD": "valkey-secret", "KVS_DB": "2",
	}
	cfg, err := LoadFromEnv(mapLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.KVS()
	if s.Password() != "valkey-secret" || !s.TLS || s.Address != "valkey.example.test:6379" || s.DB != 2 {
		t.Fatal("settings not loaded")
	}
	for _, value := range []any{cfg, s} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, "valkey-secret") || strings.Contains(text, "misskey-secret") {
				t.Fatal("secret leaked in formatted configuration")
			}
		}
	}
	file := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(file, []byte("file-secret\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	delete(env, "KVS_PASSWORD")
	env["KVS_PASSWORD_FILE"] = file
	cfg, err = LoadFromEnv(mapLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KVS().Password() != "file-secret" {
		t.Fatal("mounted password not loaded")
	}
	env["KVS_PASSWORD"] = "valkey-secret"
	if _, err := LoadFromEnv(mapLookup(env)); err == nil {
		t.Fatal("accepted two password sources")
	}
	delete(env, "KVS_PASSWORD")
	env["KVS_PASSWORD_FILE"] = file + "-missing"
	if _, err := LoadFromEnv(mapLookup(env)); err == nil || strings.Contains(err.Error(), file) {
		t.Fatal("file error should be sanitized")
	}
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	env["KVS_PASSWORD_FILE"] = file
	if _, err := LoadFromEnv(mapLookup(env)); err == nil {
		t.Fatal("accepted empty password file")
	}
}

func TestKVSRejectsInvalidConfiguration(t *testing.T) {
	for name, overrides := range map[string]map[string]string{
		"backend":                   {"KVS_BACKEND": "other"},
		"ignored endpoint":          {"KVS_BACKEND": "memory"},
		"namespace":                 {"KVS_NAMESPACE": ""},
		"credentials in URL":        {"KVS_URL": "rediss://default:secret@valkey.test:6379"},
		"missing port":              {"KVS_URL": "rediss://valkey.test"},
		"invalid port":              {"KVS_URL": "rediss://valkey.test:65536"},
		"query":                     {"KVS_URL": "rediss://valkey.test:6379?password=secret"},
		"fragment":                  {"KVS_URL": "rediss://valkey.test:6379#secret"},
		"path":                      {"KVS_URL": "redis://valkey.test:6379/1"},
		"scheme":                    {"KVS_URL": "https://valkey.test:6379"},
		"database":                  {"KVS_DB": "-1"},
		"username without password": {"KVS_USERNAME": "bot"},
	} {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{"KVS_BACKEND": "valkey", "KVS_NAMESPACE": "production", "KVS_URL": "redis://valkey.test:6379"}
			for key, value := range overrides {
				env[key] = value
			}
			if _, err := loadKVSSettings(mapLookup(env)); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid settings not safely rejected")
			}
		})
	}
}
