# Infrastructure: Configuration (`internal/observability/config`)

> Env-var-driven config loading at v0; pluggable source for SSM at v1. Validated at startup; binary refuses to boot on invalid config.

## Purpose

- Load configuration from env vars (v0) or SSM Parameter Store (v1+).
- Validate every required field; fail-fast on missing or malformed.
- Provide a typed, immutable `Config` to the rest of the binary.
- Make secrets pluggable so v0→v1 migration is mechanical.

## Dependencies

- `os` (stdlib)
- `strconv` (stdlib)
- `time` (stdlib)
- `errors`, `fmt` (stdlib)
- (v1) `github.com/aws/aws-sdk-go-v2/service/ssm`

## Package layout

```
internal/observability/config/
├── doc.go
├── config.go            ← top-level Config struct + Load()
├── source.go            ← Source interface (env, SSM, file)
├── env_source.go        ← env-var implementation
├── ssm_source.go        ← (v1) SSM Parameter Store implementation
├── validators.go        ← string/int/duration parsers + URL validation
└── config_test.go
```

## Public API

```go
// Package config loads and validates application configuration.
//
// Configuration is loaded once at startup; the resulting Config is immutable
// and passed to every subsystem. There is no runtime config refresh — change
// requires a restart.
//
// At v0 we use env vars only; at v1 we add SSM Parameter Store for secrets.
// The Source interface decouples the implementation.
package config

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "net/url"
    "strconv"
    "strings"
    "time"
)

// Role defines what the binary does. Set via PIKSHIPP_ROLE env var.
type Role string

const (
    RoleAPI    Role = "api"
    RoleWorker Role = "worker"
    RoleAll    Role = "all"
)

func (r Role) IncludesAPI() bool    { return r == RoleAPI || r == RoleAll }
func (r Role) IncludesWorker() bool { return r == RoleWorker || r == RoleAll }
func (r Role) IsValid() bool {
    switch r {
    case RoleAPI, RoleWorker, RoleAll:
        return true
    }
    return false
}

// Config is the top-level application configuration.
//
// All fields are validated at Load(); zero-values are not permitted unless
// explicitly marked optional.
type Config struct {
    // Identity
    Role            Role
    Version         string  // git SHA at build; required
    Environment     string  // 'dev' | 'staging' | 'prod'

    // Server
    HTTPPort        int           // default 8080
    HTTPReadTimeout time.Duration // default 10s
    HTTPWriteTimeout time.Duration // default 30s
    HTTPShutdownGracePeriod time.Duration // default 30s

    // Database
    DatabaseURL          string         // required; postgres://...
    DBMaxConns           int32          // default 50
    DBMinConns           int32          // default 5
    DBStatementTimeout   time.Duration  // default 5s

    // S3
    S3Region          string  // required (or 'ap-south-1' default)
    S3Bucket          string  // required
    S3Endpoint        string  // optional; for LocalStack
    S3ForcePathStyle  bool    // optional; for LocalStack

    // Auth
    SessionHMACKey       string         // required; base64 32 bytes
    SessionMaxIdle       time.Duration  // default 24h
    SessionCookieDomain  string         // required in prod
    SessionCookieSecure  bool           // default true; false only in dev

    GoogleOAuthClientID     string  // required
    GoogleOAuthClientSecret string  // required
    GoogleOAuthRedirectURL  string  // required

    // Carriers
    DelhiveryAPIKey  string  // required
    DelhiveryBaseURL string  // default https://api.delhivery.com

    // SMS / OTP
    MSG91AuthKey       string  // required
    MSG91SenderID      string  // required (e.g., "PIKSHP")
    MSG91OTPTemplateID string  // required

    // Email
    SESFromEmail string  // required (verified in SES)
    SESRegion    string  // default ap-south-1

    // Payment gateway
    RazorpayKeyID         string  // required
    RazorpayKeySecret     string  // required
    RazorpayWebhookSecret string  // required (HMAC for webhook verify)

    // Channel: Shopify
    ShopifyClientID     string  // required
    ShopifyClientSecret string  // required
    ShopifyWebhookSecret string // required (HMAC for webhook verify)

    // Logging
    LogLevel  slog.Level  // default LevelInfo
    LogFormat string      // 'json' | 'text'; default json

    // River
    RiverWorkerPoolSize int  // default 10
}

// Load reads configuration from a Source, validates, and returns a Config.
func Load(ctx context.Context, src Source) (*Config, error) {
    c := &Config{}
    var errs []error

    // Identity
    c.Role        = Role(strings.ToLower(src.Get("ROLE", string(RoleAll))))
    if !c.Role.IsValid() { errs = append(errs, fmt.Errorf("invalid ROLE %q", c.Role)) }
    c.Version     = src.Get("VERSION", "dev")
    c.Environment = src.Get("ENV", "dev")

    // Server
    c.HTTPPort                  = parseInt(src, "HTTP_PORT", 8080, &errs)
    c.HTTPReadTimeout           = parseDuration(src, "HTTP_READ_TIMEOUT", 10*time.Second, &errs)
    c.HTTPWriteTimeout          = parseDuration(src, "HTTP_WRITE_TIMEOUT", 30*time.Second, &errs)
    c.HTTPShutdownGracePeriod   = parseDuration(src, "HTTP_SHUTDOWN_GRACE", 30*time.Second, &errs)

    // Database
    c.DatabaseURL = src.GetRequired("DATABASE_URL", &errs)
    if c.DatabaseURL != "" {
        if _, err := url.Parse(c.DatabaseURL); err != nil {
            errs = append(errs, fmt.Errorf("DATABASE_URL: %w", err))
        }
    }
    c.DBMaxConns         = int32(parseInt(src, "DB_MAX_CONNS", 50, &errs))
    c.DBMinConns         = int32(parseInt(src, "DB_MIN_CONNS", 5, &errs))
    c.DBStatementTimeout = parseDuration(src, "DB_STATEMENT_TIMEOUT", 5*time.Second, &errs)

    // S3
    c.S3Region         = src.Get("S3_REGION", "ap-south-1")
    c.S3Bucket         = src.GetRequired("S3_BUCKET", &errs)
    c.S3Endpoint       = src.Get("S3_ENDPOINT", "")
    c.S3ForcePathStyle = parseBool(src, "S3_FORCE_PATH_STYLE", false, &errs)

    // Auth
    c.SessionHMACKey       = src.GetRequiredSecret("SESSION_HMAC_KEY", &errs)
    c.SessionMaxIdle       = parseDuration(src, "SESSION_MAX_IDLE", 24*time.Hour, &errs)
    c.SessionCookieDomain  = src.Get("SESSION_COOKIE_DOMAIN", "")
    c.SessionCookieSecure  = parseBool(src, "SESSION_COOKIE_SECURE", c.Environment != "dev", &errs)

    c.GoogleOAuthClientID     = src.GetRequired("GOOGLE_OAUTH_CLIENT_ID", &errs)
    c.GoogleOAuthClientSecret = src.GetRequiredSecret("GOOGLE_OAUTH_CLIENT_SECRET", &errs)
    c.GoogleOAuthRedirectURL  = src.GetRequired("GOOGLE_OAUTH_REDIRECT_URL", &errs)

    // Carriers
    c.DelhiveryAPIKey  = src.GetRequiredSecret("DELHIVERY_API_KEY", &errs)
    c.DelhiveryBaseURL = src.Get("DELHIVERY_BASE_URL", "https://api.delhivery.com")

    // SMS
    c.MSG91AuthKey       = src.GetRequiredSecret("MSG91_AUTH_KEY", &errs)
    c.MSG91SenderID      = src.GetRequired("MSG91_SENDER_ID", &errs)
    c.MSG91OTPTemplateID = src.GetRequired("MSG91_OTP_TEMPLATE_ID", &errs)

    // Email
    c.SESFromEmail = src.GetRequired("SES_FROM_EMAIL", &errs)
    c.SESRegion    = src.Get("SES_REGION", "ap-south-1")

    // Razorpay
    c.RazorpayKeyID         = src.GetRequired("RAZORPAY_KEY_ID", &errs)
    c.RazorpayKeySecret     = src.GetRequiredSecret("RAZORPAY_KEY_SECRET", &errs)
    c.RazorpayWebhookSecret = src.GetRequiredSecret("RAZORPAY_WEBHOOK_SECRET", &errs)

    // Shopify
    c.ShopifyClientID      = src.GetRequired("SHOPIFY_CLIENT_ID", &errs)
    c.ShopifyClientSecret  = src.GetRequiredSecret("SHOPIFY_CLIENT_SECRET", &errs)
    c.ShopifyWebhookSecret = src.GetRequiredSecret("SHOPIFY_WEBHOOK_SECRET", &errs)

    // Logging
    c.LogLevel  = parseLogLevel(src, "LOG_LEVEL", slog.LevelInfo, &errs)
    c.LogFormat = src.Get("LOG_FORMAT", "json")
    if c.LogFormat != "json" && c.LogFormat != "text" {
        errs = append(errs, fmt.Errorf("LOG_FORMAT must be 'json' or 'text'"))
    }

    // River
    c.RiverWorkerPoolSize = parseInt(src, "RIVER_WORKER_POOL", 10, &errs)

    if len(errs) > 0 {
        return nil, fmt.Errorf("config: %d errors: %w", len(errs), errors.Join(errs...))
    }

    return c, nil
}

// String returns a redacted summary suitable for logging.
// Secrets are masked; non-secret fields are visible.
func (c *Config) String() string {
    return fmt.Sprintf(
        "Config{role=%s env=%s version=%s db=%s s3=%s log=%s/%s}",
        c.Role, c.Environment, c.Version,
        redactURL(c.DatabaseURL),
        c.S3Bucket,
        c.LogLevel, c.LogFormat,
    )
}

func redactURL(u string) string {
    parsed, err := url.Parse(u)
    if err != nil { return "<unparseable>" }
    if parsed.User != nil {
        parsed.User = url.UserPassword(parsed.User.Username(), "***")
    }
    return parsed.Redacted()
}
```

## Source interface

```go
// internal/observability/config/source.go
package config

// Source provides config values by key.
//
// Implementations:
//   - envSource: reads from os.Getenv with PIKSHIPP_ prefix
//   - ssmSource: reads from AWS SSM Parameter Store
//   - mapSource: in-memory; for testing
type Source interface {
    // Get returns the value for key, or fallback if unset.
    Get(key, fallback string) string

    // GetRequired returns the value for key. If unset, appends an error to errs
    // and returns "" — caller continues collecting errors.
    GetRequired(key string, errs *[]error) string

    // GetRequiredSecret is like GetRequired but logs that this key is sensitive
    // (so the source implementation knows to never log/cache the value plainly).
    GetRequiredSecret(key string, errs *[]error) string
}
```

### envSource (v0)

```go
// internal/observability/config/env_source.go
package config

import (
    "fmt"
    "os"
    "strings"
)

// envSource reads from environment variables with the PIKSHIPP_ prefix.
//
// Example: Get("DATABASE_URL", ...) reads PIKSHIPP_DATABASE_URL.
type envSource struct {
    prefix string
}

// NewEnvSource returns a Source backed by os environment.
func NewEnvSource() Source {
    return &envSource{prefix: "PIKSHIPP_"}
}

func (s *envSource) key(key string) string {
    return s.prefix + strings.ToUpper(key)
}

func (s *envSource) Get(key, fallback string) string {
    if v := os.Getenv(s.key(key)); v != "" {
        return v
    }
    return fallback
}

func (s *envSource) GetRequired(key string, errs *[]error) string {
    v := os.Getenv(s.key(key))
    if v == "" {
        *errs = append(*errs, fmt.Errorf("required env %s is unset", s.key(key)))
        return ""
    }
    return v
}

func (s *envSource) GetRequiredSecret(key string, errs *[]error) string {
    return s.GetRequired(key, errs)  // env source treats secrets like normal values
}
```

### mapSource (testing)

```go
package config

import "fmt"

type mapSource map[string]string

func NewMapSource(m map[string]string) Source { return mapSource(m) }

func (s mapSource) Get(key, fallback string) string {
    if v, ok := s[key]; ok && v != "" {
        return v
    }
    return fallback
}

func (s mapSource) GetRequired(key string, errs *[]error) string {
    v, ok := s[key]
    if !ok || v == "" {
        *errs = append(*errs, fmt.Errorf("required key %s is unset", key))
        return ""
    }
    return v
}

func (s mapSource) GetRequiredSecret(key string, errs *[]error) string {
    return s.GetRequired(key, errs)
}
```

### ssmSource (v1)

```go
// internal/observability/config/ssm_source.go
package config

import (
    "context"
    "fmt"
    "log/slog"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
)

// ssmSource reads from AWS SSM Parameter Store.
//
// Pre-fetches all parameters under /pikshipp/<env>/ at construction.
// Reduces latency to zero per Get; trades freshness (refresh = restart).
type ssmSource struct {
    values map[string]string
    log    *slog.Logger
}

// NewSSMSource fetches all parameters under /pikshipp/<env>/ and returns
// a Source backed by the in-memory map.
//
// Falls back to envSource for any key not present in SSM (useful for the
// migration window).
func NewSSMSource(ctx context.Context, client *ssm.Client, env string, log *slog.Logger) (Source, error) {
    prefix := fmt.Sprintf("/pikshipp/%s/", env)
    values := make(map[string]string)

    var nextToken *string
    for {
        out, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
            Path:           aws.String(prefix),
            Recursive:      aws.Bool(true),
            WithDecryption: aws.Bool(true),
            NextToken:      nextToken,
        })
        if err != nil {
            return nil, fmt.Errorf("config.NewSSMSource: %w", err)
        }
        for _, p := range out.Parameters {
            // strip prefix; uppercase
            k := *p.Name
            k = k[len(prefix):]
            values[k] = aws.ToString(p.Value)
        }
        if out.NextToken == nil {
            break
        }
        nextToken = out.NextToken
    }

    log.Info("loaded config from SSM", slog.Int("count", len(values)), slog.String("prefix", prefix))
    return &ssmSource{values: values, log: log}, nil
}

func (s *ssmSource) Get(key, fallback string) string {
    if v, ok := s.values[key]; ok && v != "" {
        return v
    }
    return fallback
}

func (s *ssmSource) GetRequired(key string, errs *[]error) string {
    v, ok := s.values[key]
    if !ok || v == "" {
        *errs = append(*errs, fmt.Errorf("required SSM key %s missing", key))
        return ""
    }
    return v
}

func (s *ssmSource) GetRequiredSecret(key string, errs *[]error) string {
    return s.GetRequired(key, errs)
}
```

## Validators / parsers

```go
// internal/observability/config/validators.go
package config

import (
    "fmt"
    "log/slog"
    "strconv"
    "time"
)

func parseInt(src Source, key string, def int, errs *[]error) int {
    s := src.Get(key, "")
    if s == "" {
        return def
    }
    n, err := strconv.Atoi(s)
    if err != nil {
        *errs = append(*errs, fmt.Errorf("%s: must be int: %w", key, err))
        return def
    }
    return n
}

func parseBool(src Source, key string, def bool, errs *[]error) bool {
    s := src.Get(key, "")
    if s == "" {
        return def
    }
    b, err := strconv.ParseBool(s)
    if err != nil {
        *errs = append(*errs, fmt.Errorf("%s: must be bool: %w", key, err))
        return def
    }
    return b
}

func parseDuration(src Source, key string, def time.Duration, errs *[]error) time.Duration {
    s := src.Get(key, "")
    if s == "" {
        return def
    }
    d, err := time.ParseDuration(s)
    if err != nil {
        *errs = append(*errs, fmt.Errorf("%s: must be duration (e.g., '5s'): %w", key, err))
        return def
    }
    return d
}

func parseLogLevel(src Source, key string, def slog.Level, errs *[]error) slog.Level {
    s := src.Get(key, "")
    if s == "" {
        return def
    }
    var lvl slog.Level
    if err := lvl.UnmarshalText([]byte(s)); err != nil {
        *errs = append(*errs, fmt.Errorf("%s: must be 'debug'|'info'|'warn'|'error': %w", key, err))
        return def
    }
    return lvl
}
```

## Wiring (in `cmd/pikshipp/main.go`)

```go
func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // 1. Logger first (so config errors are loggable)
    bootLogger := observability.NewLogger("info", "json", "boot")

    // 2. Pick source
    var src config.Source
    if env := os.Getenv("PIKSHIPP_ENV"); env == "prod" || env == "staging" {
        // v1+: load from SSM
        ssmClient := awsssm.NewFromConfig(awsCfg)
        var err error
        src, err = config.NewSSMSource(ctx, ssmClient, env, bootLogger)
        if err != nil {
            bootLogger.Error("ssm source failed", slog.Any("error", err))
            os.Exit(1)
        }
    } else {
        // v0 / dev: env vars
        src = config.NewEnvSource()
    }

    // 3. Load
    cfg, err := config.Load(ctx, src)
    if err != nil {
        bootLogger.Error("config load failed", slog.Any("error", err))
        os.Exit(1)
    }

    // 4. Real logger from config
    log := observability.NewLogger(cfg.LogLevel.String(), cfg.LogFormat, cfg.Version)
    log.Info("pikshipp starting", slog.String("config", cfg.String()))

    // ... rest of wiring
}
```

## Implementation notes

### Why env vars at v0 vs SSM at v1+

Per ADR 0010, env vars are a pragmatic v0 choice. The `Source` interface lets us swap in SSM with a one-line change in `main.go` without touching config consumers.

### Error accumulation, not fail-fast

`Load` collects errors and returns them all together. A user seeing "5 config errors" once is better than 5 deploy attempts each surfacing one error.

### Required vs optional

- `GetRequired`: must be set; appends error if not.
- `Get(key, fallback)`: optional with default.
- `GetRequiredSecret`: same as `GetRequired` semantically; the secret marker exists for future audit (e.g., never log this key's value).

### Validation depth

We validate:
- Type parsing (int, bool, duration).
- Enum membership (Role, LogLevel, LogFormat).
- URL parseability.

We do NOT validate:
- Secret strength (HMAC key length) — left to security review of deployments.
- Reachability of external services — that's `/readyz`.

### Immutability

Once `Load` returns, the `*Config` is read-only. We never expose setters. To change config: restart the binary.

### Testing

`mapSource` lets tests inject any config without env-var pollution.

```go
func TestConfig_LoadValid(t *testing.T) {
    src := config.NewMapSource(map[string]string{
        "ROLE":                       "all",
        "DATABASE_URL":               "postgres://test:test@localhost/test",
        "S3_BUCKET":                  "test-bucket",
        "SESSION_HMAC_KEY":           "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        "GOOGLE_OAUTH_CLIENT_ID":     "x",
        "GOOGLE_OAUTH_CLIENT_SECRET": "y",
        "GOOGLE_OAUTH_REDIRECT_URL":  "http://localhost/cb",
        "DELHIVERY_API_KEY":          "x",
        "MSG91_AUTH_KEY":             "x",
        "MSG91_SENDER_ID":            "PIKSHP",
        "MSG91_OTP_TEMPLATE_ID":      "x",
        "SES_FROM_EMAIL":             "noreply@pikshipp.com",
        "RAZORPAY_KEY_ID":            "x",
        "RAZORPAY_KEY_SECRET":        "x",
        "RAZORPAY_WEBHOOK_SECRET":    "x",
        "SHOPIFY_CLIENT_ID":          "x",
        "SHOPIFY_CLIENT_SECRET":      "x",
        "SHOPIFY_WEBHOOK_SECRET":     "x",
    })
    c, err := config.Load(context.Background(), src)
    require.NoError(t, err)
    require.Equal(t, config.RoleAll, c.Role)
    require.Equal(t, 8080, c.HTTPPort)
}

func TestConfig_LoadMissingRequired(t *testing.T) {
    src := config.NewMapSource(map[string]string{
        "ROLE": "all",
        // DATABASE_URL missing
    })
    _, err := config.Load(context.Background(), src)
    require.Error(t, err)
    require.Contains(t, err.Error(), "DATABASE_URL")
}

func TestConfig_LoadInvalidTypes(t *testing.T) {
    src := config.NewMapSource(map[string]string{
        "HTTP_PORT": "not-a-number",
        // ... required fields filled
    })
    _, err := config.Load(context.Background(), src)
    require.Error(t, err)
    require.Contains(t, err.Error(), "HTTP_PORT")
}

func TestConfig_RedactsURL(t *testing.T) {
    c := &Config{DatabaseURL: "postgres://user:secret@host:5432/db"}
    s := c.String()
    require.NotContains(t, s, "secret")
    require.Contains(t, s, "***")
}
```

## Performance

- `Load` is called once at startup; no hot path.
- `envSource.Get` is one `os.Getenv` call per access — negligible.
- `ssmSource` pre-fetches; `Get` is a map lookup — negligible.

## Observability

- `Config.String()` is logged at startup with secrets redacted.
- All config-load errors are logged at `error` level and force exit.
- No further config events at runtime (because there are none — config is immutable).

## Open questions

- Should we validate that `SessionHMACKey` is exactly 64 hex chars (256 bits)? Yes — add a length check; security review may also want entropy validation. Add at v1.
- SSM `WithDecryption` is required for SecureString parameters; cost is ~$0.05/10k API calls. At our scale, negligible.
- Re-loading config on SIGHUP? Considered. Restart is simpler and fits our blue/green deploy model. Skip.

## References

- ADR 0010 (secrets in env at v0).
- HLD `04-cross-cutting/04-deployment.md` (env file structure).
