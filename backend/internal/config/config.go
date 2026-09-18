package config

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config holds process-level bootstrap options. Runtime-tunable gateway
// parameters live in the persistent store instead.
type Config struct {
	Addr          string
	DataDir       string
	StaticDir     string
	AdminUser     string
	AdminPassword string
}

func Load() Config {
	var cfg Config
	flag.StringVar(&cfg.Addr, "addr", env("DOLA2API_ADDR", "127.0.0.1:8080"), "HTTP listen address")
	flag.StringVar(&cfg.DataDir, "data", env("DOLA2API_DATA", "data"), "persistent data directory")
	flag.StringVar(&cfg.StaticDir, "static", env("DOLA2API_STATIC", "frontend/dist"), "built frontend directory")
	flag.StringVar(&cfg.AdminUser, "admin-user", env("DOLA2API_ADMIN_USER", "admin"), "initial admin username")
	flag.StringVar(&cfg.AdminPassword, "admin-password", env("DOLA2API_ADMIN_PASSWORD", ""), "initial admin password (random when empty)")
	flag.Parse()

	if cfg.AdminPassword == "" {
		cfg.AdminPassword = env("DOLA2API_ADMIN_PASSWORD", "")
	}
	return cfg
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// Settings mirrors the runtime-tunable section exposed by the admin console.
type Settings struct {
	Server   ServerSettings   `json:"server"`
	Upstream UpstreamSettings `json:"upstream"`
	Routing  RoutingSettings  `json:"routing"`
	Audit    AuditSettings    `json:"audit"`
	Media    MediaSettings    `json:"media"`
}

type ServerSettings struct {
	Addr                  string `json:"addr"`
	MaxConcurrentRequests int    `json:"maxConcurrentRequests"`
	AdminUsername         string `json:"adminUsername"`
}

type UpstreamSettings struct {
	BaseURL              string `json:"baseURL"`
	BotID                string `json:"botID"`
	Region               string `json:"region"`
	Language             string `json:"language"`
	RequestTimeoutSec    int    `json:"requestTimeoutSec"`
	StreamIdleTimeoutSec int    `json:"streamIdleTimeoutSec"`
	Proxy                string `json:"proxy"`
	UserAgent            string `json:"userAgent"`
}

type RoutingSettings struct {
	Strategy        string `json:"strategy"`
	CooldownBaseSec int    `json:"cooldownBaseSec"`
	CooldownMaxSec  int    `json:"cooldownMaxSec"`
	MaxAttempts     int    `json:"maxAttempts"`
	CapacityWaitSec int    `json:"capacityWaitSec"`
	StickyTTLSec    int    `json:"stickyTTLSec"`
	PreferIdle      bool   `json:"preferIdle"`
}

type AuditSettings struct {
	RetentionDays  int  `json:"retentionDays"`
	MaxRecords     int  `json:"maxRecords"`
	RecordBody     bool `json:"recordBody"`
	BodyLimitBytes int  `json:"bodyLimitBytes"`
}

type MediaSettings struct {
	GeneratedDir   string `json:"generatedDir"`
	PublicBaseURL  string `json:"publicBaseURL"`
	MaxTotalSizeMB int    `json:"maxTotalSizeMB"`
	AutoDownload   bool   `json:"autoDownload"`
}

// DefaultSettings returns the built-in runtime configuration.
func DefaultSettings(dataDir string) Settings {
	return Settings{
		Server: ServerSettings{
			Addr:                  "127.0.0.1:8080",
			MaxConcurrentRequests: 64,
			AdminUsername:         "admin",
		},
		Upstream: UpstreamSettings{
			BaseURL:              "https://www.dola.com",
			BotID:                "7339470689562525703",
			Region:               "AU",
			Language:             "en",
			RequestTimeoutSec:    300,
			StreamIdleTimeoutSec: 120,
			Proxy:                "",
			UserAgent:            defaultUserAgent,
		},
		Routing: RoutingSettings{
			Strategy:        "least_inflight",
			CooldownBaseSec: 60,
			CooldownMaxSec:  900,
			MaxAttempts:     3,
			CapacityWaitSec: 20,
			StickyTTLSec:    300,
			PreferIdle:      true,
		},
		Audit: AuditSettings{
			RetentionDays:  7,
			MaxRecords:     5000,
			RecordBody:     true,
			BodyLimitBytes: 8192,
		},
		Media: MediaSettings{
			GeneratedDir:   filepath.Join(dataDir, "generated"),
			PublicBaseURL:  "",
			MaxTotalSizeMB: 2048,
			AutoDownload:   true,
		},
	}
}

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// Normalize repairs out-of-range values coming from a stored config file.
func (s *Settings) Normalize(dataDir string) {
	def := DefaultSettings(dataDir)
	if s.Server.Addr == "" {
		s.Server.Addr = def.Server.Addr
	}
	if s.Server.MaxConcurrentRequests <= 0 {
		s.Server.MaxConcurrentRequests = def.Server.MaxConcurrentRequests
	}
	if s.Server.AdminUsername == "" {
		s.Server.AdminUsername = def.Server.AdminUsername
	}
	if s.Upstream.BaseURL == "" {
		s.Upstream.BaseURL = def.Upstream.BaseURL
	}
	if s.Upstream.BotID == "" {
		s.Upstream.BotID = def.Upstream.BotID
	}
	if s.Upstream.Region == "" {
		s.Upstream.Region = def.Upstream.Region
	}
	if s.Upstream.Language == "" {
		s.Upstream.Language = def.Upstream.Language
	}
	if s.Upstream.RequestTimeoutSec < 5 {
		s.Upstream.RequestTimeoutSec = def.Upstream.RequestTimeoutSec
	}
	if s.Upstream.StreamIdleTimeoutSec < 5 {
		s.Upstream.StreamIdleTimeoutSec = def.Upstream.StreamIdleTimeoutSec
	}
	if s.Upstream.UserAgent == "" {
		s.Upstream.UserAgent = def.Upstream.UserAgent
	}
	switch s.Routing.Strategy {
	case "least_inflight", "round_robin", "priority", "random":
	default:
		s.Routing.Strategy = def.Routing.Strategy
	}
	if s.Routing.CooldownBaseSec <= 0 {
		s.Routing.CooldownBaseSec = def.Routing.CooldownBaseSec
	}
	if s.Routing.CooldownMaxSec <= 0 {
		s.Routing.CooldownMaxSec = def.Routing.CooldownMaxSec
	}
	if s.Routing.CooldownMaxSec < s.Routing.CooldownBaseSec {
		// Raise the ceiling to the base rather than substituting the default.
		// The base may legitimately sit above the default ceiling (an operator
		// can configure a long cooldown), and dropping in the default would
		// then leave max < base all over again. The invariant that matters is
		// that the backoff ramp never starts past its own cap.
		s.Routing.CooldownMaxSec = s.Routing.CooldownBaseSec
	}
	if s.Routing.MaxAttempts < 1 || s.Routing.MaxAttempts > 20 {
		s.Routing.MaxAttempts = def.Routing.MaxAttempts
	}
	if s.Routing.CapacityWaitSec < 0 {
		s.Routing.CapacityWaitSec = def.Routing.CapacityWaitSec
	}
	if s.Routing.StickyTTLSec < 0 {
		s.Routing.StickyTTLSec = def.Routing.StickyTTLSec
	}
	if s.Audit.RetentionDays <= 0 {
		// A non-positive window puts the retention cutoff at or after "now",
		// so the next prune would delete every audit record.
		s.Audit.RetentionDays = def.Audit.RetentionDays
	}
	if s.Audit.MaxRecords < 100 {
		s.Audit.MaxRecords = def.Audit.MaxRecords
	}
	if s.Audit.BodyLimitBytes < 256 {
		s.Audit.BodyLimitBytes = def.Audit.BodyLimitBytes
	}
	if s.Media.GeneratedDir == "" {
		s.Media.GeneratedDir = def.Media.GeneratedDir
	}
	if s.Media.MaxTotalSizeMB < 64 {
		s.Media.MaxTotalSizeMB = def.Media.MaxTotalSizeMB
	}
}

func (s Settings) Clone() Settings {
	raw, err := json.Marshal(s)
	if err != nil {
		return s
	}
	var clone Settings
	if err := json.Unmarshal(raw, &clone); err != nil {
		return s
	}
	return clone
}

func (s Settings) RequestTimeout() time.Duration {
	return time.Duration(s.Upstream.RequestTimeoutSec) * time.Second
}

func (s Settings) StreamIdleTimeout() time.Duration {
	return time.Duration(s.Upstream.StreamIdleTimeoutSec) * time.Second
}

func (s Settings) CooldownBase() time.Duration {
	return time.Duration(s.Routing.CooldownBaseSec) * time.Second
}

func (s Settings) CooldownMax() time.Duration {
	return time.Duration(s.Routing.CooldownMaxSec) * time.Second
}

func (s Settings) CapacityWait() time.Duration {
	return time.Duration(s.Routing.CapacityWaitSec) * time.Second
}

func (s Settings) StickyTTL() time.Duration {
	return time.Duration(s.Routing.StickyTTLSec) * time.Second
}

func (s Settings) Retention() time.Duration {
	return time.Duration(s.Audit.RetentionDays) * 24 * time.Hour
}

func (s Settings) MediaLimitBytes() int64 {
	return int64(s.Media.MaxTotalSizeMB) * 1024 * 1024
}

// ParseIntOrDefault is a small helper for query parameters.
func ParseIntOrDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
