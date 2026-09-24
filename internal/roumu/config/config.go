// Package config loads and validates azkey-roumu-bot process configuration.
package config

import (
	"errors"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	errEnvironmentLookup = errors.New("environment lookup is required")
	errBaseURLRequired   = errors.New("MISSKEY_BASE_URL is required")
	errBaseURLInvalid    = errors.New("MISSKEY_BASE_URL must be an absolute HTTP or HTTPS URL without userinfo, query, or fragment")
	errTokenRequired     = errors.New("MISSKEY_TOKEN is required")
	errPollingMode       = errors.New("POLLING_MODE must be observe")
	errPollingSetting    = errors.New("polling setting is invalid")
	errLogLevel          = errors.New("LOG_LEVEL must be DEBUG, INFO, WARN, or ERROR")
)

// PollingSettings is the validated process configuration passed to the
// read-only polling lifecycle. The defaults are deliberately conservative
// enough for about 100 mutual targets while keeping ordinary observation latency
// near one or two minutes.
type PollingSettings struct {
	Mode                 string
	PollInterval         time.Duration
	FollowerSyncInterval time.Duration
	Concurrency          int
	RatePerSecond        float64
	RateBurst            int
	PageLimit            int
	MaxPagesPerTurn      int
	DedupLimit           int
	DedupTTL             time.Duration
	StartupSpread        time.Duration
	BackoffBase          time.Duration
	BackoffMax           time.Duration
}

// Config contains the validated values needed to assemble the application.
// Sensitive values are kept private and are exposed only to the constructor
// that needs them.
type Config struct {
	baseURL *url.URL
	token   string
	polling PollingSettings
}

// Load reads the process environment.
func Load() (Config, error) {
	return LoadFromEnv(os.Getenv)
}

// LoadFromEnv is Load with an injectable environment lookup for tests and
// callers that already own configuration input.
func LoadFromEnv(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, errEnvironmentLookup
	}
	if _, err := ParseLogLevel(getenv("LOG_LEVEL")); err != nil {
		return Config{}, err
	}

	baseURL, err := parseBaseURL(getenv("MISSKEY_BASE_URL"))
	if err != nil {
		return Config{}, err
	}

	token := getenv("MISSKEY_TOKEN")
	if strings.TrimSpace(token) == "" {
		return Config{}, errTokenRequired
	}

	polling, err := loadPollingSettings(getenv)
	if err != nil {
		return Config{}, err
	}

	return Config{baseURL: baseURL, token: token, polling: polling}, nil
}

// ParseLogLevel parses LOG_LEVEL without including its value in errors.
// An empty value keeps the default INFO threshold.
func ParseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errLogLevel
	}
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

// Polling returns the validated polling settings.
func (c Config) Polling() PollingSettings {
	return c.polling
}

func loadPollingSettings(getenv func(string) string) (PollingSettings, error) {
	settings := PollingSettings{
		Mode:                 "observe",
		PollInterval:         time.Minute,
		FollowerSyncInterval: 5 * time.Minute,
		Concurrency:          2,
		RatePerSecond:        2,
		RateBurst:            1,
		PageLimit:            100,
		MaxPagesPerTurn:      5,
		DedupLimit:           10_000,
		DedupTTL:             24 * time.Hour,
		StartupSpread:        time.Minute,
		BackoffBase:          time.Second,
		BackoffMax:           5 * time.Minute,
	}

	if value := strings.TrimSpace(getenv("POLLING_MODE")); value != "" {
		settings.Mode = value
	}
	if settings.Mode != "observe" {
		return PollingSettings{}, errPollingMode
	}

	var err error
	if settings.PollInterval, err = parseDurationSetting(getenv, settings.PollInterval, "POLL_INTERVAL", "POLLING_INTERVAL"); err != nil {
		return PollingSettings{}, err
	}
	if settings.FollowerSyncInterval, err = parseDurationSetting(getenv, settings.FollowerSyncInterval, "FOLLOWER_SYNC_INTERVAL"); err != nil {
		return PollingSettings{}, err
	}
	if settings.Concurrency, err = parseIntSetting(getenv, settings.Concurrency, "POLL_CONCURRENCY", "POLLING_CONCURRENCY"); err != nil {
		return PollingSettings{}, err
	}
	if settings.RatePerSecond, err = parseFloatSetting(getenv, settings.RatePerSecond, "POLL_RATE_PER_SECOND"); err != nil {
		return PollingSettings{}, err
	}
	if settings.RateBurst, err = parseIntSetting(getenv, settings.RateBurst, "POLL_RATE_BURST"); err != nil {
		return PollingSettings{}, err
	}
	if settings.PageLimit, err = parseIntSetting(getenv, settings.PageLimit, "POLL_PAGE_LIMIT"); err != nil {
		return PollingSettings{}, err
	}
	if settings.MaxPagesPerTurn, err = parseIntSetting(getenv, settings.MaxPagesPerTurn, "POLL_MAX_PAGES_PER_TURN"); err != nil {
		return PollingSettings{}, err
	}
	if settings.DedupLimit, err = parseIntSetting(getenv, settings.DedupLimit, "POLL_DEDUP_LIMIT"); err != nil {
		return PollingSettings{}, err
	}
	if settings.DedupTTL, err = parseDurationSetting(getenv, settings.DedupTTL, "POLL_DEDUP_TTL"); err != nil {
		return PollingSettings{}, err
	}
	if settings.StartupSpread, err = parseDurationSetting(getenv, settings.StartupSpread, "POLL_STARTUP_SPREAD"); err != nil {
		return PollingSettings{}, err
	}
	if settings.BackoffBase, err = parseDurationSetting(getenv, settings.BackoffBase, "POLL_BACKOFF_BASE"); err != nil {
		return PollingSettings{}, err
	}
	if settings.BackoffMax, err = parseDurationSetting(getenv, settings.BackoffMax, "POLL_BACKOFF_MAX"); err != nil {
		return PollingSettings{}, err
	}

	if settings.PollInterval <= 0 || settings.FollowerSyncInterval <= 0 || settings.Concurrency <= 0 || settings.Concurrency > 1_000 || settings.RatePerSecond <= 0 || settings.RateBurst <= 0 || settings.PageLimit <= 0 || settings.PageLimit > 100 || settings.MaxPagesPerTurn <= 0 || settings.MaxPagesPerTurn > 10_000 || settings.DedupLimit <= 0 || settings.DedupLimit > 1_000_000 || settings.DedupTTL <= 0 || settings.StartupSpread < 0 || settings.BackoffBase <= 0 || settings.BackoffMax < settings.BackoffBase {
		return PollingSettings{}, errPollingSetting
	}
	return settings, nil
}

func parseDurationSetting(getenv func(string) string, fallback time.Duration, names ...string) (time.Duration, error) {
	raw := settingValue(getenv, names...)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, errPollingSetting
	}
	return value, nil
}

func parseIntSetting(getenv func(string) string, fallback int, names ...string) (int, error) {
	raw := settingValue(getenv, names...)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errPollingSetting
	}
	return value, nil
}

func parseFloatSetting(getenv func(string) string, fallback float64, names ...string) (float64, error) {
	raw := settingValue(getenv, names...)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errPollingSetting
	}
	return value, nil
}

func settingValue(getenv func(string) string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			return value
		}
	}
	return ""
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
