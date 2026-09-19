// Package config loads and validates azkey-roumu-bot process configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/azuki774/azkey-bot/internal/roumu/domain"
)

var (
	errEnvironmentLookup   = errors.New("environment lookup is required")
	errBaseURLRequired     = errors.New("MISSKEY_BASE_URL is required")
	errBaseURLInvalid      = errors.New("MISSKEY_BASE_URL must be an absolute HTTP or HTTPS URL without userinfo, query, or fragment")
	errTokenRequired       = errors.New("MISSKEY_TOKEN is required")
	errRulesFileRequired   = errors.New("RULES_FILE is required")
	errRulesFileUnreadable = errors.New("RULES_FILE could not be read")
	errRulesJSONInvalid    = errors.New("RULES_FILE contains invalid JSON")
	errRulesFieldsMissing  = errors.New("RULES_FILE must contain version and rules")
	errRulesVersion        = errors.New("RULES_FILE version is unsupported")
	errRulesUnsupported    = errors.New("RULES_FILE contains unsupported rules")
)

// Config contains the validated values needed to assemble the application.
// Sensitive values are kept private and are exposed only to the constructor
// that needs them.
type Config struct {
	baseURL *url.URL
	token   string
	rules   domain.Rules
}

// Load reads the process environment and the configured rules file.
func Load() (Config, error) {
	return LoadFromEnv(os.Getenv)
}

// LoadFromEnv is Load with an injectable environment lookup for tests and
// callers that already own configuration input.
func LoadFromEnv(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, errEnvironmentLookup
	}

	baseURL, err := parseBaseURL(getenv("MISSKEY_BASE_URL"))
	if err != nil {
		return Config{}, err
	}

	token := getenv("MISSKEY_TOKEN")
	if strings.TrimSpace(token) == "" {
		return Config{}, errTokenRequired
	}

	rulesPath := getenv("RULES_FILE")
	if strings.TrimSpace(rulesPath) == "" {
		return Config{}, errRulesFileRequired
	}
	rules, err := loadRules(rulesPath)
	if err != nil {
		return Config{}, err
	}

	return Config{baseURL: baseURL, token: token, rules: rules}, nil
}

// BaseURL returns a copy of the validated Misskey base URL.
func (c Config) BaseURL() *url.URL {
	if c.baseURL == nil {
		return nil
	}
	u := *c.baseURL
	return &u
}

// Token returns the validated token for HTTP client construction.
func (c Config) Token() string {
	return c.token
}

// Rules returns the validated rules configuration.
func (c Config) Rules() domain.Rules {
	return c.rules
}

func parseBaseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errBaseURLRequired
	}
	if raw != strings.TrimSpace(raw) || containsURLWhitespace(raw) {
		return nil, errBaseURLInvalid
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, errBaseURLInvalid
	}
	if !u.IsAbs() || u.Opaque != "" || u.Host == "" || u.Hostname() == "" {
		return nil, errBaseURLInvalid
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return nil, errBaseURLInvalid
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return nil, errBaseURLInvalid
	}
	if port := u.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 0 || portNumber > 65535 {
			return nil, errBaseURLInvalid
		}
	}

	u.Scheme = strings.ToLower(u.Scheme)
	return u, nil
}

func containsURLWhitespace(raw string) bool {
	for _, r := range raw {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

type rulesDocument struct {
	Version *int               `json:"version"`
	Rules   *[]json.RawMessage `json:"rules"`
}

func loadRules(path string) (domain.Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Rules{}, errRulesFileUnreadable
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder, true); err != nil {
		return domain.Rules{}, errRulesJSONInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return domain.Rules{}, errRulesJSONInvalid
	}

	var document rulesDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return domain.Rules{}, errRulesJSONInvalid
	}

	if document.Version == nil || document.Rules == nil {
		return domain.Rules{}, errRulesFieldsMissing
	}
	if *document.Version != 1 {
		return domain.Rules{}, errRulesVersion
	}
	if len(*document.Rules) != 0 {
		return domain.Rules{}, errRulesUnsupported
	}

	return domain.Rules{}, nil
}

func scanJSONValue(decoder *json.Decoder, topLevel bool) error {
	token, err := decoder.Token()
	if err != nil {
		return errRulesJSONInvalid
	}

	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		if topLevel {
			return errRulesJSONInvalid
		}
		return nil
	}

	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return errRulesJSONInvalid
			}
			key, ok := keyToken.(string)
			if !ok {
				return errRulesJSONInvalid
			}
			if _, exists := keys[key]; exists {
				return errRulesJSONInvalid
			}
			keys[key] = struct{}{}
			if topLevel && key != "version" && key != "rules" {
				return errRulesJSONInvalid
			}
			if err := scanJSONValue(decoder, false); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errRulesJSONInvalid
		}
		return nil
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, false); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errRulesJSONInvalid
		}
		return nil
	default:
		return errRulesJSONInvalid
	}
}
