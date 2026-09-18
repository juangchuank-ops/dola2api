package config

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The config package is pure value manipulation with no external deps, so every
// case here is a straight assertion. Normalize is the interesting one: it is the
// only thing standing between a hand-edited settings file and the rest of the
// process, so it gets the boundary treatment.

func TestDefaultSettingsAreSane(t *testing.T) {
	s := DefaultSettings("data")

	if s.Server.Addr != "127.0.0.1:8080" {
		t.Errorf("Server.Addr = %q", s.Server.Addr)
	}
	if s.Server.MaxConcurrentRequests != 64 {
		t.Errorf("Server.MaxConcurrentRequests = %d", s.Server.MaxConcurrentRequests)
	}
	if s.Upstream.BaseURL != "https://www.dola.com" {
		t.Errorf("Upstream.BaseURL = %q", s.Upstream.BaseURL)
	}
	if s.Upstream.UserAgent != defaultUserAgent {
		t.Errorf("Upstream.UserAgent = %q, want the shared default", s.Upstream.UserAgent)
	}
	if s.Routing.Strategy != "least_inflight" {
		t.Errorf("Routing.Strategy = %q", s.Routing.Strategy)
	}
	if s.Media.GeneratedDir != filepath.Join("data", "generated") {
		t.Errorf("Media.GeneratedDir = %q, want it rooted at dataDir", s.Media.GeneratedDir)
	}
}

// A zero Settings is what a fresh install starts from. Normalize must fill every
// field that cannot sensibly stay zero — but only those: see the companion test
// below for the fields whose zero is a meaningful value.
func TestNormalizeFillsZeroValueSettings(t *testing.T) {
	var s Settings
	s.Normalize("data")
	def := DefaultSettings("data")

	filled := []struct {
		name string
		got  any
		want any
	}{
		{"Server.Addr", s.Server.Addr, def.Server.Addr},
		{"Server.MaxConcurrentRequests", s.Server.MaxConcurrentRequests, def.Server.MaxConcurrentRequests},
		{"Server.AdminUsername", s.Server.AdminUsername, def.Server.AdminUsername},
		{"Upstream.BaseURL", s.Upstream.BaseURL, def.Upstream.BaseURL},
		{"Upstream.BotID", s.Upstream.BotID, def.Upstream.BotID},
		{"Upstream.Region", s.Upstream.Region, def.Upstream.Region},
		{"Upstream.Language", s.Upstream.Language, def.Upstream.Language},
		{"Upstream.RequestTimeoutSec", s.Upstream.RequestTimeoutSec, def.Upstream.RequestTimeoutSec},
		{"Upstream.StreamIdleTimeoutSec", s.Upstream.StreamIdleTimeoutSec, def.Upstream.StreamIdleTimeoutSec},
		{"Upstream.UserAgent", s.Upstream.UserAgent, def.Upstream.UserAgent},
		{"Routing.Strategy", s.Routing.Strategy, def.Routing.Strategy},
		{"Routing.CooldownBaseSec", s.Routing.CooldownBaseSec, def.Routing.CooldownBaseSec},
		{"Routing.CooldownMaxSec", s.Routing.CooldownMaxSec, def.Routing.CooldownMaxSec},
		{"Routing.MaxAttempts", s.Routing.MaxAttempts, def.Routing.MaxAttempts},
		{"Audit.RetentionDays", s.Audit.RetentionDays, def.Audit.RetentionDays},
		{"Audit.MaxRecords", s.Audit.MaxRecords, def.Audit.MaxRecords},
		{"Audit.BodyLimitBytes", s.Audit.BodyLimitBytes, def.Audit.BodyLimitBytes},
		{"Media.GeneratedDir", s.Media.GeneratedDir, def.Media.GeneratedDir},
		{"Media.MaxTotalSizeMB", s.Media.MaxTotalSizeMB, def.Media.MaxTotalSizeMB},
	}

	for _, f := range filled {
		if f.got != f.want {
			t.Errorf("%s = %v after Normalize, want the default %v", f.name, f.got, f.want)
		}
	}
}

// The counterpart to the test above. Fields where zero carries meaning must
// survive untouched — resetting them would make it impossible to disable sticky
// routing, body capture or media auto-download from the console.
func TestNormalizePreservesMeaningfulZeroValues(t *testing.T) {
	s := DefaultSettings("data")
	s.Routing.CapacityWaitSec = 0
	s.Routing.StickyTTLSec = 0
	s.Routing.PreferIdle = false
	s.Audit.RecordBody = false
	s.Media.AutoDownload = false
	s.Upstream.Proxy = ""

	s.Normalize("data")

	if s.Routing.CapacityWaitSec != 0 {
		t.Errorf("CapacityWaitSec = %d, want 0 (fail fast rather than wait)", s.Routing.CapacityWaitSec)
	}
	if s.Routing.StickyTTLSec != 0 {
		t.Errorf("StickyTTLSec = %d, want 0 (sticky routing disabled)", s.Routing.StickyTTLSec)
	}
	if s.Routing.PreferIdle {
		t.Error("PreferIdle was forced back on; the operator can no longer turn it off")
	}
	if s.Audit.RecordBody {
		t.Error("RecordBody was forced back on; the operator can no longer turn it off")
	}
	if s.Media.AutoDownload {
		t.Error("AutoDownload was forced back on; the operator can no longer turn it off")
	}
	if s.Upstream.Proxy != "" {
		t.Errorf("Proxy = %q, want empty (no proxy configured)", s.Upstream.Proxy)
	}
}

func TestNormalizeKeepsValidValues(t *testing.T) {
	s := DefaultSettings("data")
	s.Server.Addr = "0.0.0.0:9000"
	s.Server.MaxConcurrentRequests = 128
	s.Upstream.BaseURL = "https://example.test"
	s.Upstream.RequestTimeoutSec = 42
	s.Routing.Strategy = "random"
	s.Routing.MaxAttempts = 7
	s.Audit.MaxRecords = 1234
	s.Media.MaxTotalSizeMB = 512

	before := s
	s.Normalize("data")

	if s != before {
		t.Errorf("Normalize modified already-valid settings:\ngot  %+v\nwant %+v", s, before)
	}
}

func TestNormalizeRejectsUnknownStrategy(t *testing.T) {
	for _, strategy := range []string{"", "bogus", "LEAST_INFLIGHT", "least-inflight", "rr"} {
		s := DefaultSettings("data")
		s.Routing.Strategy = strategy
		s.Normalize("data")
		if s.Routing.Strategy != "least_inflight" {
			t.Errorf("strategy %q survived Normalize as %q", strategy, s.Routing.Strategy)
		}
	}
}

func TestNormalizeAcceptsEveryKnownStrategy(t *testing.T) {
	for _, strategy := range []string{"least_inflight", "round_robin", "priority", "random"} {
		s := DefaultSettings("data")
		s.Routing.Strategy = strategy
		s.Normalize("data")
		if s.Routing.Strategy != strategy {
			t.Errorf("valid strategy %q was rewritten to %q", strategy, s.Routing.Strategy)
		}
	}
}

// Normalize must leave the cooldown ceiling at or above the base. Otherwise the
// backoff ramp starts higher than its own cap, and pool's exponential backoff
// computes a sequence that jumps straight past the ceiling on the first failure.
func TestNormalizeCooldownCeilingIsNotBelowBase(t *testing.T) {
	cases := []struct {
		name string
		base int
		max  int
	}{
		{"both zero", 0, 0},
		{"max below base", 600, 300},
		{"max zero with large base", 3600, 0},
		{"base larger than the default ceiling", 100000, 0},
		{"base far above the default ceiling, max slightly under", 50000, 49999},
		{"negative base", -5, -10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultSettings("data")
			s.Routing.CooldownBaseSec = tc.base
			s.Routing.CooldownMaxSec = tc.max
			s.Normalize("data")

			if s.Routing.CooldownMaxSec < s.Routing.CooldownBaseSec {
				t.Errorf("after Normalize(base=%d, max=%d): base=%d but max=%d — the ceiling is below the base",
					tc.base, tc.max, s.Routing.CooldownBaseSec, s.Routing.CooldownMaxSec)
			}
			if s.Routing.CooldownBaseSec <= 0 {
				t.Errorf("base=%d after Normalize, want a positive duration", s.Routing.CooldownBaseSec)
			}
		})
	}
}

// A non-positive retention window makes Retention() return a zero or negative
// duration, which puts the audit cutoff in the future and wipes every record on
// the next prune.
func TestNormalizeRepairsRetentionDays(t *testing.T) {
	for _, days := range []int{-30, -1, 0} {
		s := DefaultSettings("data")
		s.Audit.RetentionDays = days
		s.Normalize("data")

		if s.Audit.RetentionDays <= 0 {
			t.Errorf("RetentionDays=%d survived Normalize; Retention() would be %v",
				s.Audit.RetentionDays, s.Retention())
		}
	}
}

func TestNormalizeEnforcesLowerBounds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Settings)
		check  func(Settings) bool
		detail string
	}{
		{
			name:   "request timeout below floor",
			mutate: func(s *Settings) { s.Upstream.RequestTimeoutSec = 4 },
			check:  func(s Settings) bool { return s.Upstream.RequestTimeoutSec >= 5 },
			detail: "RequestTimeoutSec must be raised to at least 5s",
		},
		{
			name:   "stream idle timeout below floor",
			mutate: func(s *Settings) { s.Upstream.StreamIdleTimeoutSec = -1 },
			check:  func(s Settings) bool { return s.Upstream.StreamIdleTimeoutSec >= 5 },
			detail: "StreamIdleTimeoutSec must be raised to at least 5s",
		},
		{
			name:   "max concurrent requests not positive",
			mutate: func(s *Settings) { s.Server.MaxConcurrentRequests = 0 },
			check:  func(s Settings) bool { return s.Server.MaxConcurrentRequests > 0 },
			detail: "MaxConcurrentRequests must be positive",
		},
		{
			name:   "max attempts out of range",
			mutate: func(s *Settings) { s.Routing.MaxAttempts = 999 },
			check:  func(s Settings) bool { return s.Routing.MaxAttempts >= 1 && s.Routing.MaxAttempts <= 20 },
			detail: "MaxAttempts must fall within 1..20",
		},
		{
			name:   "max attempts zero",
			mutate: func(s *Settings) { s.Routing.MaxAttempts = 0 },
			check:  func(s Settings) bool { return s.Routing.MaxAttempts >= 1 },
			detail: "MaxAttempts must be at least 1",
		},
		{
			name:   "negative capacity wait",
			mutate: func(s *Settings) { s.Routing.CapacityWaitSec = -3 },
			check:  func(s Settings) bool { return s.Routing.CapacityWaitSec >= 0 },
			detail: "CapacityWaitSec must not be negative",
		},
		{
			name:   "negative sticky ttl",
			mutate: func(s *Settings) { s.Routing.StickyTTLSec = -3 },
			check:  func(s Settings) bool { return s.Routing.StickyTTLSec >= 0 },
			detail: "StickyTTLSec must not be negative",
		},
		{
			name:   "audit max records below floor",
			mutate: func(s *Settings) { s.Audit.MaxRecords = 10 },
			check:  func(s Settings) bool { return s.Audit.MaxRecords >= 100 },
			detail: "MaxRecords must be at least 100",
		},
		{
			name:   "audit body limit below floor",
			mutate: func(s *Settings) { s.Audit.BodyLimitBytes = 8 },
			check:  func(s Settings) bool { return s.Audit.BodyLimitBytes >= 256 },
			detail: "BodyLimitBytes must be at least 256",
		},
		{
			name:   "media quota below floor",
			mutate: func(s *Settings) { s.Media.MaxTotalSizeMB = 1 },
			check:  func(s Settings) bool { return s.Media.MaxTotalSizeMB >= 64 },
			detail: "MaxTotalSizeMB must be at least 64",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultSettings("data")
			tc.mutate(&s)
			s.Normalize("data")
			if !tc.check(s) {
				t.Errorf("%s; got %+v", tc.detail, s)
			}
		})
	}
}

func TestNormalizeFillsEmptyStrings(t *testing.T) {
	s := Settings{}
	s.Normalize("/srv/data")
	def := DefaultSettings("/srv/data")

	if s.Server.AdminUsername != def.Server.AdminUsername {
		t.Errorf("AdminUsername = %q", s.Server.AdminUsername)
	}
	if s.Upstream.BotID != def.Upstream.BotID {
		t.Errorf("BotID = %q", s.Upstream.BotID)
	}
	if s.Upstream.Region != def.Upstream.Region {
		t.Errorf("Region = %q", s.Upstream.Region)
	}
	if s.Upstream.Language != def.Upstream.Language {
		t.Errorf("Language = %q", s.Upstream.Language)
	}
	if s.Upstream.UserAgent != def.Upstream.UserAgent {
		t.Errorf("UserAgent = %q", s.Upstream.UserAgent)
	}
	// The generated-media directory is derived from the data dir the caller
	// passes in, not from whatever was stored.
	if s.Media.GeneratedDir != filepath.Join("/srv/data", "generated") {
		t.Errorf("GeneratedDir = %q, want it derived from the supplied dataDir", s.Media.GeneratedDir)
	}
}

// Normalize runs on every load and on every settings write, so running it twice
// must not keep changing the value.
func TestNormalizeIsIdempotent(t *testing.T) {
	s := Settings{}
	s.Normalize("data")
	once := s
	s.Normalize("data")

	if s != once {
		t.Errorf("second Normalize changed the settings:\nfirst  %+v\nsecond %+v", once, s)
	}
}

// Clone exists so the settings snapshot handed to readers cannot be mutated by
// a later writer. Since every field is a value type, a shallow copy already
// suffices — the test pins that property so a future pointer field cannot be
// added without noticing.
func TestCloneIsDetached(t *testing.T) {
	original := DefaultSettings("data")
	clone := original.Clone()

	clone.Server.Addr = "changed"
	clone.Routing.Strategy = "random"
	clone.Media.GeneratedDir = "/elsewhere"

	if original.Server.Addr == "changed" {
		t.Error("mutating the clone changed the original's Server.Addr")
	}
	if original.Routing.Strategy == "random" {
		t.Error("mutating the clone changed the original's Routing.Strategy")
	}
	if original.Media.GeneratedDir == "/elsewhere" {
		t.Error("mutating the clone changed the original's Media.GeneratedDir")
	}
	if clone == original {
		t.Error("clone equals original after mutation, so it is not a copy")
	}
}

func TestCloneRoundTripsEveryField(t *testing.T) {
	original := DefaultSettings("data")
	clone := original.Clone()

	if clone != original {
		t.Errorf("Clone did not round-trip the settings:\ngot  %+v\nwant %+v", clone, original)
	}
}

func TestDurationAccessorsConvertSeconds(t *testing.T) {
	s := DefaultSettings("data")
	s.Upstream.RequestTimeoutSec = 30
	s.Upstream.StreamIdleTimeoutSec = 45
	s.Routing.CooldownBaseSec = 10
	s.Routing.CooldownMaxSec = 600
	s.Routing.CapacityWaitSec = 5
	s.Routing.StickyTTLSec = 90
	s.Audit.RetentionDays = 3

	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"RequestTimeout", s.RequestTimeout(), 30 * time.Second},
		{"StreamIdleTimeout", s.StreamIdleTimeout(), 45 * time.Second},
		{"CooldownBase", s.CooldownBase(), 10 * time.Second},
		{"CooldownMax", s.CooldownMax(), 600 * time.Second},
		{"CapacityWait", s.CapacityWait(), 5 * time.Second},
		{"StickyTTL", s.StickyTTL(), 90 * time.Second},
		{"Retention", s.Retention(), 3 * 24 * time.Hour},
	}

	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s() = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestMediaLimitBytesConvertsMegabytes(t *testing.T) {
	s := DefaultSettings("data")
	s.Media.MaxTotalSizeMB = 128

	if got, want := s.MediaLimitBytes(), int64(128)*1024*1024; got != want {
		t.Errorf("MediaLimitBytes() = %d, want %d", got, want)
	}
}

// MediaLimitBytes multiplies by 1MiB, so an absurd-but-representable value must
// not silently wrap into a negative quota (which would disable downloads).
func TestMediaLimitBytesDoesNotWrapNegative(t *testing.T) {
	s := DefaultSettings("data")
	s.Media.MaxTotalSizeMB = math.MaxInt32

	if got := s.MediaLimitBytes(); got <= 0 {
		t.Errorf("MediaLimitBytes() = %d for MaxTotalSizeMB=%d; the quota wrapped", got, s.Media.MaxTotalSizeMB)
	}
}

func TestParseIntOrDefault(t *testing.T) {
	cases := []struct {
		raw      string
		fallback int
		want     int
	}{
		{"", 7, 7},
		{"42", 7, 42},
		{"0", 7, 0},
		{"-3", 7, -3},
		{"abc", 7, 7},
		{"3.5", 7, 7},
		{" 5", 7, 7},
		{"5 ", 7, 7},
		{"0x10", 7, 7},
		{"99999999999999999999", 7, 7},
		{"1", 0, 1},
	}

	for _, tc := range cases {
		if got := ParseIntOrDefault(tc.raw, tc.fallback); got != tc.want {
			t.Errorf("ParseIntOrDefault(%q, %d) = %d, want %d", tc.raw, tc.fallback, got, tc.want)
		}
	}
}

func TestEnvUsesFallbackWhenUnset(t *testing.T) {
	t.Setenv("DOLA2API_TEST_UNSET", "")

	if got := env("DOLA2API_TEST_UNSET", "fallback"); got != "fallback" {
		t.Errorf("env() = %q, want the fallback for an unset variable", got)
	}
}

func TestEnvPrefersSetValue(t *testing.T) {
	t.Setenv("DOLA2API_TEST_SET", "from-env")

	if got := env("DOLA2API_TEST_SET", "fallback"); got != "from-env" {
		t.Errorf("env() = %q, want the environment value", got)
	}
}

// An empty environment variable is treated as unset, so there is no way to
// override a non-empty default with "". That is deliberate: the defaults are
// the safety net for a half-written config.
func TestEnvTreatsEmptyAsUnset(t *testing.T) {
	t.Setenv("DOLA2API_TEST_EMPTY", "")

	if got := env("DOLA2API_TEST_EMPTY", "fallback"); got != "fallback" {
		t.Errorf("env() = %q, want the fallback when the variable is empty", got)
	}
}

// Load reads process-level options from flags with environment fallbacks. It
// touches the global flag set, so the test swaps both flag.CommandLine and
// os.Args and restores them afterwards.
func TestLoadPrefersFlagsOverEnvironment(t *testing.T) {
	restoreFlags := flag.CommandLine
	restoreArgs := os.Args
	defer func() {
		flag.CommandLine = restoreFlags
		os.Args = restoreArgs
	}()

	t.Setenv("DOLA2API_ADDR", "10.0.0.1:1111")
	t.Setenv("DOLA2API_DATA", "/env/data")
	t.Setenv("DOLA2API_STATIC", "/env/static")
	t.Setenv("DOLA2API_ADMIN_USER", "envadmin")

	flag.CommandLine = flag.NewFlagSet("dola2api", flag.ContinueOnError)
	os.Args = []string{"dola2api", "-addr", "127.0.0.1:2222", "-admin-user", "flagadmin"}

	cfg := Load()

	if cfg.Addr != "127.0.0.1:2222" {
		t.Errorf("Addr = %q, want the flag value to win", cfg.Addr)
	}
	if cfg.AdminUser != "flagadmin" {
		t.Errorf("AdminUser = %q, want the flag value to win", cfg.AdminUser)
	}
	if cfg.DataDir != "/env/data" {
		t.Errorf("DataDir = %q, want the environment value when no flag is given", cfg.DataDir)
	}
	if cfg.StaticDir != "/env/static" {
		t.Errorf("StaticDir = %q, want the environment value when no flag is given", cfg.StaticDir)
	}
}

func TestLoadFallsBackToBuiltInDefaults(t *testing.T) {
	restoreFlags := flag.CommandLine
	restoreArgs := os.Args
	defer func() {
		flag.CommandLine = restoreFlags
		os.Args = restoreArgs
	}()

	for _, key := range []string{"DOLA2API_ADDR", "DOLA2API_DATA", "DOLA2API_STATIC", "DOLA2API_ADMIN_USER", "DOLA2API_ADMIN_PASSWORD"} {
		t.Setenv(key, "")
	}

	flag.CommandLine = flag.NewFlagSet("dola2api", flag.ContinueOnError)
	os.Args = []string{"dola2api"}

	cfg := Load()

	if cfg.Addr != "127.0.0.1:8080" {
		t.Errorf("Addr = %q, want the built-in default", cfg.Addr)
	}
	if cfg.DataDir != "data" {
		t.Errorf("DataDir = %q, want the built-in default", cfg.DataDir)
	}
	if cfg.StaticDir != "frontend/dist" {
		t.Errorf("StaticDir = %q, want the built-in default", cfg.StaticDir)
	}
	if cfg.AdminUser != "admin" {
		t.Errorf("AdminUser = %q, want the built-in default", cfg.AdminUser)
	}
	// An empty password means "generate a random one at first boot".
	if cfg.AdminPassword != "" {
		t.Errorf("AdminPassword = %q, want empty so the caller generates one", cfg.AdminPassword)
	}
}

func TestLoadReadsAdminPasswordFromEnvironment(t *testing.T) {
	restoreFlags := flag.CommandLine
	restoreArgs := os.Args
	defer func() {
		flag.CommandLine = restoreFlags
		os.Args = restoreArgs
	}()

	t.Setenv("DOLA2API_ADMIN_PASSWORD", "s3cret-from-env")

	flag.CommandLine = flag.NewFlagSet("dola2api", flag.ContinueOnError)
	os.Args = []string{"dola2api"}

	if got := Load().AdminPassword; got != "s3cret-from-env" {
		t.Errorf("AdminPassword = %q, want the environment value", got)
	}
}
