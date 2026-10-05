package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var errKVSSetting = errors.New("invalid KVS configuration; check KVS_BACKEND, KVS_NAMESPACE and KVS connection settings")

// KVSSettings configures business-state storage, independent of polling progress.
type KVSSettings struct {
	Backend   string
	Namespace string
	Address   string
	TLS       bool
	Username  string
	DB        int
	password  string
}

func (s KVSSettings) Password() string { return s.password }
func (s KVSSettings) String() string   { return "KVSSettings{redacted}" }
func (s KVSSettings) GoString() string { return s.String() }
func (c Config) KVS() KVSSettings      { return c.kvs }
func (c Config) String() string        { return "Config{redacted}" }
func (c Config) GoString() string      { return c.String() }

func loadKVSSettings(getenv func(string) string) (KVSSettings, error) {
	s := KVSSettings{Backend: strings.TrimSpace(getenv("KVS_BACKEND"))}
	if s.Backend == "" {
		s.Backend = "memory"
	}
	if s.Backend != "memory" && s.Backend != "valkey" {
		return KVSSettings{}, errKVSSetting
	}
	if s.Backend == "memory" {
		for _, name := range []string{"KVS_NAMESPACE", "KVS_URL", "KVS_USERNAME", "KVS_PASSWORD", "KVS_PASSWORD_FILE", "KVS_DB"} {
			if getenv(name) != "" {
				return KVSSettings{}, errKVSSetting
			}
		}
		return s, nil
	}
	s.Namespace = strings.TrimSpace(getenv("KVS_NAMESPACE"))
	if s.Namespace == "" {
		return KVSSettings{}, errKVSSetting
	}
	raw := getenv("KVS_URL")
	u, err := url.Parse(raw)
	if err != nil || containsURLWhitespace(raw) || u.Opaque != "" || u.Hostname() == "" ||
		(u.Scheme != "redis" && u.Scheme != "rediss") || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return KVSSettings{}, errKVSSetting
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return KVSSettings{}, errKVSSetting
	}
	s.Address, s.TLS = u.Host, u.Scheme == "rediss"
	s.Username, s.password = getenv("KVS_USERNAME"), getenv("KVS_PASSWORD")
	if file := getenv("KVS_PASSWORD_FILE"); file != "" {
		if s.password != "" {
			return KVSSettings{}, errKVSSetting
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return KVSSettings{}, errors.New("could not read KVS_PASSWORD_FILE")
		}
		s.password = strings.TrimRight(string(data), "\r\n")
		if s.password == "" {
			return KVSSettings{}, errors.New("KVS_PASSWORD_FILE is empty")
		}
	}
	if s.Username != "" && s.password == "" {
		return KVSSettings{}, errKVSSetting
	}
	if raw := getenv("KVS_DB"); raw != "" {
		s.DB, err = strconv.Atoi(raw)
		if err != nil || s.DB < 0 {
			return KVSSettings{}, errKVSSetting
		}
	}
	return s, nil
}
