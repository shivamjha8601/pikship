# Infrastructure: Secrets handling (`internal/observability/secrets`)

> Plain env vars at v0 (acknowledged tech debt per ADR 0010). SSM Parameter Store at v1+. Pluggable interface so the migration is mechanical.

## Purpose

- Treat secrets as a separate concern from regular config.
- Provide a consistent way to load, redact, and audit secret usage.
- v0: env vars; v1: SSM. Same interface; one wiring change.

## Dependencies

- `os` (stdlib)
- (v1) `github.com/aws/aws-sdk-go-v2/service/ssm`
- `internal/observability/config` (the `Source` interface)

## Package layout

```
internal/observability/secrets/
├── doc.go
├── secret.go            ← Secret type (wraps string with redaction)
├── store.go             ← Store interface + implementations
├── env_store.go         ← env-var-backed
├── ssm_store.go         ← v1: SSM-backed
└── secret_test.go
```

## Public API

```go
// Package secrets provides a typed Secret value that prevents accidental
// disclosure (e.g., logging) and a Store interface for retrieval.
//
// Always pass secrets as Secret type, not string, in domain code. The
// Secret type's String() method redacts; only Reveal() returns the
// plaintext, and Reveal call sites are auditable.
package secrets

import (
    "fmt"
    "strings"
)

// Secret wraps a sensitive string with redacting String() and JSON
// marshalling.
//
// Construction:
//   s := secrets.New("plaintext-value")
//
// Usage:
//   fmt.Println(s)              // prints "***"
//   _ = json.Marshal(s)         // produces "***"
//   plaintext := s.Reveal()     // returns the actual value; rare
type Secret struct {
    val string
}

// New constructs a Secret from a plaintext value.
func New(val string) Secret {
    return Secret{val: val}
}

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool {
    return s.val == ""
}

// String returns "***" for any non-empty Secret.
// This means accidental fmt.Sprintf("%s", secret) produces "***".
func (s Secret) String() string {
    if s.val == "" { return "" }
    return "***"
}

// Format implements fmt.Formatter.
func (s Secret) Format(f fmt.State, c rune) {
    fmt.Fprint(f, s.String())
}

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) {
    if s.val == "" {
        return []byte(`""`), nil
    }
    return []byte(`"***"`), nil
}

// Reveal returns the actual plaintext value.
//
// Use this only at the point of consumption (e.g., when calling an
// external API or computing an HMAC). Never store the result; never
// log it; never pass to logging.
func (s Secret) Reveal() string {
    return s.val
}

// Equal compares two secrets in constant time.
//
// Useful for comparing webhook signatures: HMAC.Equal-style.
func (s Secret) Equal(other Secret) bool {
    return constantTimeEquals(s.val, other.val)
}

func constantTimeEquals(a, b string) bool {
    if len(a) != len(b) { return false }
    var diff byte
    for i := 0; i < len(a); i++ {
        diff |= a[i] ^ b[i]
    }
    return diff == 0
}

// HasPrefix is a redaction-safe prefix check (for logging "secret starts with X").
func (s Secret) HasPrefix(prefix string) bool {
    return strings.HasPrefix(s.val, prefix)
}
```

## Store interface

```go
// internal/observability/secrets/store.go
package secrets

import "context"

// Store retrieves secrets by key. Implementations are pluggable.
type Store interface {
    // Get returns the secret for key, or an error if missing.
    Get(ctx context.Context, key string) (Secret, error)

    // GetOptional returns the secret for key, or empty Secret if missing.
    // Useful for fields that are conditionally required.
    GetOptional(ctx context.Context, key string) Secret
}

// MissingSecretError is returned by Get when a key isn't found.
type MissingSecretError struct{ Key string }

func (e MissingSecretError) Error() string {
    return fmt.Sprintf("secret %q not found", e.Key)
}
```

## Env store (v0)

```go
// internal/observability/secrets/env_store.go
package secrets

import (
    "context"
    "os"
    "strings"
)

// envStore reads secrets from environment variables.
//
// Prefix is "PIKSHIPP_"; key is upper-cased and prefixed.
type envStore struct{}

// NewEnvStore returns a Store backed by os.Getenv.
func NewEnvStore() Store {
    return &envStore{}
}

func (s *envStore) Get(ctx context.Context, key string) (Secret, error) {
    envKey := "PIKSHIPP_" + strings.ToUpper(key)
    v := os.Getenv(envKey)
    if v == "" {
        return Secret{}, MissingSecretError{Key: key}
    }
    return New(v), nil
}

func (s *envStore) GetOptional(ctx context.Context, key string) Secret {
    s2, err := s.Get(ctx, key)
    if err != nil { return Secret{} }
    return s2
}
```

## SSM store (v1+)

```go
// internal/observability/secrets/ssm_store.go
package secrets

import (
    "context"
    "fmt"
    "log/slog"
    "strings"
    "sync"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
)

// ssmStore retrieves secrets from AWS SSM Parameter Store with KMS decryption.
//
// Parameters are pre-fetched at construction (under /pikshipp/<env>/secrets/)
// to avoid runtime SSM calls. For runtime rotation, restart the binary.
type ssmStore struct {
    client *ssm.Client
    prefix string

    mu     sync.RWMutex
    cache  map[string]Secret
    log    *slog.Logger
}

// NewSSMStore constructs an SSM-backed Store.
//
// env is "dev" | "staging" | "prod"; the SSM prefix is /pikshipp/<env>/secrets/.
//
// On construction, pre-fetches all parameters under the prefix. If SSM is
// unreachable, returns an error (binary should refuse to start).
func NewSSMStore(ctx context.Context, client *ssm.Client, env string, log *slog.Logger) (Store, error) {
    s := &ssmStore{
        client: client,
        prefix: fmt.Sprintf("/pikshipp/%s/secrets/", env),
        cache:  make(map[string]Secret),
        log:    log,
    }
    if err := s.refresh(ctx); err != nil {
        return nil, fmt.Errorf("secrets.NewSSMStore: %w", err)
    }
    return s, nil
}

func (s *ssmStore) refresh(ctx context.Context) error {
    var nextToken *string
    count := 0
    for {
        out, err := s.client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
            Path:           aws.String(s.prefix),
            Recursive:      aws.Bool(true),
            WithDecryption: aws.Bool(true),
            NextToken:      nextToken,
        })
        if err != nil {
            return err
        }
        s.mu.Lock()
        for _, p := range out.Parameters {
            k := *p.Name
            k = strings.ToLower(strings.TrimPrefix(k, s.prefix))
            s.cache[k] = New(aws.ToString(p.Value))
            count++
        }
        s.mu.Unlock()

        if out.NextToken == nil { break }
        nextToken = out.NextToken
    }
    s.log.Info("secrets loaded from SSM",
        slog.Int("count", count),
        slog.String("prefix", s.prefix))
    return nil
}

func (s *ssmStore) Get(ctx context.Context, key string) (Secret, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    if sec, ok := s.cache[key]; ok {
        return sec, nil
    }
    return Secret{}, MissingSecretError{Key: key}
}

func (s *ssmStore) GetOptional(ctx context.Context, key string) Secret {
    sec, err := s.Get(ctx, key)
    if err != nil { return Secret{} }
    return sec
}
```

## Memory store (testing)

```go
// internal/observability/secrets/memory_store.go
package secrets

import "context"

// MemoryStore is a thread-safe in-memory Store for tests.
type MemoryStore struct {
    secrets map[string]Secret
}

// NewMemoryStore returns a Store with the given initial secrets.
func NewMemoryStore(secrets map[string]string) *MemoryStore {
    s := &MemoryStore{secrets: make(map[string]Secret, len(secrets))}
    for k, v := range secrets {
        s.secrets[k] = New(v)
    }
    return s
}

func (m *MemoryStore) Get(ctx context.Context, key string) (Secret, error) {
    if s, ok := m.secrets[key]; ok {
        return s, nil
    }
    return Secret{}, MissingSecretError{Key: key}
}

func (m *MemoryStore) GetOptional(ctx context.Context, key string) Secret {
    return m.secrets[key]
}

func (m *MemoryStore) Set(key, value string) {
    m.secrets[key] = New(value)
}
```

## Integration with Config

The secret-typed fields in `Config` use `secrets.Secret`:

```go
type Config struct {
    // ... non-secret fields ...

    SessionHMACKey       secrets.Secret
    GoogleOAuthSecret    secrets.Secret
    DelhiveryAPIKey      secrets.Secret
    MSG91AuthKey         secrets.Secret
    RazorpayKeySecret    secrets.Secret
    ShopifyClientSecret  secrets.Secret
    // ...
}
```

`Load()` populates these from a `secrets.Store`:

```go
func Load(ctx context.Context, src config.Source, sec secrets.Store) (*Config, error) {
    c := &Config{}
    var errs []error

    // ... non-secret loading ...

    var err error
    if c.SessionHMACKey, err = sec.Get(ctx, "session_hmac_key"); err != nil {
        errs = append(errs, err)
    }
    if c.GoogleOAuthSecret, err = sec.Get(ctx, "google_oauth_client_secret"); err != nil {
        errs = append(errs, err)
    }
    // ... etc

    if len(errs) > 0 {
        return nil, fmt.Errorf("config: %w", errors.Join(errs...))
    }
    return c, nil
}
```

## Usage in code

```go
// In auth/session.go:
func (a *OpaqueSessionAuth) sign(token string) string {
    h := hmac.New(sha256.New, []byte(a.cfg.SessionHMACKey.Reveal()))
    h.Write([]byte(token))
    return base64.URLEncoding.EncodeToString(h.Sum(nil))
}

// In carriers/delhivery/adapter.go:
func (a *Adapter) Book(ctx context.Context, req BookingRequest) (*BookingResult, error) {
    httpReq.Header.Set("Authorization", "Token " + a.cfg.APIKey.Reveal())
    // ...
}
```

`Reveal()` is the **only** way to get the plaintext. Code review flags every `Reveal()` call site.

## Implementation notes

### Why a Secret type at all

- Prevents accidental logging (`log.Info("loaded", "key", secret)` produces `"key":"***"`).
- Prevents serialization to clients (json marshalling redacts).
- Forces explicit `Reveal()` at consumption — auditable in code review.

### Why constant-time comparison

When validating HMAC signatures from webhooks, timing attacks could leak info if naive `==` is used. `Secret.Equal` is constant-time.

### What if a secret is leaked?

- Rotate immediately in env file or SSM.
- Restart binary.
- Audit log access (code review tool checks for `Reveal()` calls and recent commits touching them).

### Multi-instance considerations at v1+

Each instance pre-fetches secrets at startup. If you rotate a secret in SSM:
1. Update SSM Parameter Store.
2. Rolling-restart instances (one at a time; ALB drains).
3. Brief window where some instances have old secret, some have new.

For HMAC keys (e.g., `SESSION_HMAC_KEY`), this is a problem: existing sessions signed with old key may fail verification on new-key instances. Mitigation: support **two valid keys** during rotation:
- `SESSION_HMAC_KEY_PRIMARY` — used to sign new tokens.
- `SESSION_HMAC_KEY_SECONDARY` — accepted on verify, but not signed with.
- After all old tokens have expired, drop secondary.

This pattern is documented in the auth LLD (`02-infrastructure/06-auth.md`).

### Why no caching with TTL

If a secret is rotated, we want a clean cutover via restart, not a fuzzy "updated within 5 min" window. Restarts are cheap; ambiguity is expensive.

### Why no per-call SSM fetch

Cost: every domain call fetching SSM = $0.05 per 10k API calls. At our v1 traffic (~10k requests/sec festive), that's $50/month *just* for secret retrieval. Pre-fetching at startup is free.

## Test patterns

```go
func TestSecret_StringRedacts(t *testing.T) {
    s := secrets.New("super-secret-value")
    require.Equal(t, "***", s.String())
    require.Equal(t, "***", fmt.Sprint(s))
    require.Equal(t, "***", fmt.Sprintf("%s", s))
    require.Equal(t, "***", fmt.Sprintf("%v", s))
}

func TestSecret_JSONRedacts(t *testing.T) {
    s := secrets.New("super-secret-value")
    data, err := json.Marshal(s)
    require.NoError(t, err)
    require.Equal(t, `"***"`, string(data))
}

func TestSecret_RevealReturnsPlaintext(t *testing.T) {
    s := secrets.New("super-secret-value")
    require.Equal(t, "super-secret-value", s.Reveal())
}

func TestSecret_EmptyRedacts(t *testing.T) {
    s := secrets.New("")
    require.Equal(t, "", s.String())
    require.True(t, s.IsZero())
}

func TestSecret_EqualConstantTime(t *testing.T) {
    a := secrets.New("secret-1234")
    b := secrets.New("secret-1234")
    c := secrets.New("secret-5678")
    require.True(t, a.Equal(b))
    require.False(t, a.Equal(c))
}

func TestEnvStore_Get(t *testing.T) {
    os.Setenv("PIKSHIPP_TEST_SECRET", "value")
    defer os.Unsetenv("PIKSHIPP_TEST_SECRET")

    s := secrets.NewEnvStore()
    sec, err := s.Get(context.Background(), "test_secret")
    require.NoError(t, err)
    require.Equal(t, "value", sec.Reveal())
}

func TestEnvStore_GetMissing(t *testing.T) {
    s := secrets.NewEnvStore()
    _, err := s.Get(context.Background(), "definitely_missing")
    require.Error(t, err)

    var missingErr secrets.MissingSecretError
    require.True(t, errors.As(err, &missingErr))
}

func TestMemoryStore_Get(t *testing.T) {
    s := secrets.NewMemoryStore(map[string]string{"k": "v"})
    sec, err := s.Get(context.Background(), "k")
    require.NoError(t, err)
    require.Equal(t, "v", sec.Reveal())
}
```

## Linter rules

```yaml
# .golangci.yml additions
linters-settings:
  forbidigo:
    forbid:
      - p: '\.Reveal\(\)'
        msg: "secrets.Secret.Reveal() — verify this call site is necessary"
        # Allowlisted in: cmd/, adapters/*, auth/sign.go
```

This isn't a hard ban — `Reveal` is necessary at consumption — but it forces every PR with a new `Reveal` to be reviewed.

## Performance

- `New`, `String`, `Reveal`: zero allocation, constant time.
- `MarshalJSON`: 1 allocation for the small redaction.
- `EnvStore.Get`: 1 syscall (`os.Getenv`); ~100ns.
- `SSMStore.Get` (cache hit): mutex + map lookup; ~30ns.
- `SSMStore.refresh` (startup): one SSM API call per 10 parameters, ~200ms total at startup.

## Open questions

- **Vault / AWS Secrets Manager** instead of SSM? Both work; SSM is cheaper and we don't need rotation automation at v1. Reconsider at v2.
- **Periodic refresh of cached secrets** (without restart) for SSM rotation? Defer; rotation = restart.
- **Sealing data at rest** (e.g., encrypt sensitive DB columns like Aadhaar)? Requires KMS; deferred to v1 (per ADR 0010).

## References

- ADR 0010 (secrets in env at v0).
- HLD `04-cross-cutting/04-deployment.md` (env file setup at v0).
- HLD `05-cross-cutting/01-security-and-compliance.md` (PRD-level security tenets).
