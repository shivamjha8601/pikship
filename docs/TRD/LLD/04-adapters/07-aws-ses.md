# AWS SES Email Adapter

## Purpose

AWS Simple Email Service (SES) is the v0 email vendor. The adapter sends transactional email (booking confirmations, daily digests, monthly statements) from `noreply@pikshipp.com`.

## Package Layout

```
internal/vendors/email/
├── interface.go           // Vendor interface + Result
├── errors.go
├── ses/
│   ├── adapter.go
│   ├── client.go
│   └── adapter_test.go
└── stub/
    └── adapter.go
```

## Interface

```go
package email

type Vendor interface {
    Send(ctx context.Context, req SendRequest) (*SendResponse, error)
    Name() string
}

type SendRequest struct {
    From         string             // "noreply@pikshipp.com"
    To           []string           // 1+ recipients
    Cc           []string
    Bcc          []string
    ReplyTo      []string
    Subject      string
    HTML         string             // HTML body
    Text         string             // plain-text alternate (sent for accessibility / spam score)
    Attachments  []Attachment
    Headers      map[string]string  // arbitrary, e.g., List-Unsubscribe
}

type Attachment struct {
    Filename    string
    ContentType string
    Content     []byte             // raw bytes
}

type SendResponse struct {
    MessageID string
}
```

## SES Adapter

```go
package ses

import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/sesv2"
)

type Config struct {
    Region          string
    AccessKey       secrets.Secret[string]
    SecretKey       secrets.Secret[string]
    DefaultFrom     string                // "noreply@pikshipp.com"
    ConfigSet       string                // SES configuration set for tracking
    Logger          *slog.Logger
}

type Adapter struct {
    cfg    Config
    client *sesv2.Client
}

func New(ctx context.Context, cfg Config) (*Adapter, error) {
    awscfg, err := aws.NewConfigWithCreds(cfg.AccessKey.Reveal(), cfg.SecretKey.Reveal(), cfg.Region)
    if err != nil {
        return nil, err
    }
    return &Adapter{cfg: cfg, client: sesv2.NewFromConfig(awscfg)}, nil
}

func (a *Adapter) Name() string { return "ses" }

func (a *Adapter) Send(ctx context.Context, req email.SendRequest) (*email.SendResponse, error) {
    if len(req.To) == 0 {
        return nil, email.ErrNoRecipient
    }
    from := req.From
    if from == "" {
        from = a.cfg.DefaultFrom
    }

    // Use SES "raw" send for attachments + custom headers.
    if len(req.Attachments) > 0 || len(req.Headers) > 0 {
        return a.sendRaw(ctx, req, from)
    }

    out, err := a.client.SendEmail(ctx, &sesv2.SendEmailInput{
        FromEmailAddress: aws.String(from),
        Destination: &types.Destination{
            ToAddresses:  req.To,
            CcAddresses:  req.Cc,
            BccAddresses: req.Bcc,
        },
        ReplyToAddresses: req.ReplyTo,
        Content: &types.EmailContent{
            Simple: &types.Message{
                Subject: &types.Content{Data: aws.String(req.Subject)},
                Body: &types.Body{
                    Html: &types.Content{Data: aws.String(req.HTML)},
                    Text: &types.Content{Data: aws.String(req.Text)},
                },
            },
        },
        ConfigurationSetName: aws.String(a.cfg.ConfigSet),
    })
    if err != nil {
        return nil, classifySESError(err)
    }
    return &email.SendResponse{MessageID: aws.ToString(out.MessageId)}, nil
}
```

### Raw Send (with Attachments)

```go
func (a *Adapter) sendRaw(ctx context.Context, req email.SendRequest, from string) (*email.SendResponse, error) {
    raw, err := buildRawMessage(req, from)
    if err != nil {
        return nil, err
    }
    out, err := a.client.SendEmail(ctx, &sesv2.SendEmailInput{
        FromEmailAddress: aws.String(from),
        Destination: &types.Destination{
            ToAddresses:  req.To,
            CcAddresses:  req.Cc,
            BccAddresses: req.Bcc,
        },
        Content: &types.EmailContent{
            Raw: &types.RawMessage{Data: raw},
        },
        ConfigurationSetName: aws.String(a.cfg.ConfigSet),
    })
    if err != nil {
        return nil, classifySESError(err)
    }
    return &email.SendResponse{MessageID: aws.ToString(out.MessageId)}, nil
}

func buildRawMessage(req email.SendRequest, from string) ([]byte, error) {
    var buf bytes.Buffer
    boundary := generateBoundary()

    fmt.Fprintf(&buf, "From: %s\r\n", from)
    fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(req.To, ", "))
    if len(req.Cc) > 0 {
        fmt.Fprintf(&buf, "Cc: %s\r\n", strings.Join(req.Cc, ", "))
    }
    fmt.Fprintf(&buf, "Subject: %s\r\n", encodeHeader(req.Subject))
    fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
    for k, v := range req.Headers {
        fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
    }

    if len(req.Attachments) > 0 {
        fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary)
        // Body part (text + html alternative)
        bodyBoundary := generateBoundary()
        fmt.Fprintf(&buf, "--%s\r\n", boundary)
        fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", bodyBoundary)
        writeBodyParts(&buf, bodyBoundary, req.Text, req.HTML)
        fmt.Fprintf(&buf, "\r\n--%s\r\n", bodyBoundary) // closing handled inside helper
        // Attachments
        for _, att := range req.Attachments {
            fmt.Fprintf(&buf, "--%s\r\n", boundary)
            fmt.Fprintf(&buf, "Content-Type: %s\r\n", att.ContentType)
            fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n")
            fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n\r\n", att.Filename)
            fmt.Fprintln(&buf, base64.StdEncoding.EncodeToString(att.Content))
        }
        fmt.Fprintf(&buf, "--%s--\r\n", boundary)
    } else {
        bodyBoundary := generateBoundary()
        fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", bodyBoundary)
        writeBodyParts(&buf, bodyBoundary, req.Text, req.HTML)
        fmt.Fprintf(&buf, "--%s--\r\n", bodyBoundary)
    }
    return buf.Bytes(), nil
}
```

## Error Classification

```go
func classifySESError(err error) error {
    var apiErr smithy.APIError
    if !errors.As(err, &apiErr) {
        return fmt.Errorf("%w: %v", email.ErrTransient, err)
    }
    switch apiErr.ErrorCode() {
    case "MessageRejected":
        return fmt.Errorf("%w: %s", email.ErrPermanent, apiErr.ErrorMessage())
    case "MailFromDomainNotVerified", "EmailAddressNotVerified":
        return fmt.Errorf("%w: %s", email.ErrConfig, apiErr.ErrorMessage())
    case "AccountSendingPaused", "ConfigurationSetSendingPaused":
        return fmt.Errorf("%w: %s", email.ErrAccountSuspended, apiErr.ErrorMessage())
    case "Throttling", "TooManyRequestsException":
        return fmt.Errorf("%w: %s", email.ErrRateLimited, apiErr.ErrorMessage())
    case "InvalidParameterValue":
        return fmt.Errorf("%w: %s", email.ErrInvalidInput, apiErr.ErrorMessage())
    default:
        return fmt.Errorf("%w: %s", email.ErrTransient, apiErr.ErrorMessage())
    }
}
```

## Sentinel Errors

```go
package email

var (
    ErrTransient        = errors.New("email: transient")
    ErrPermanent        = errors.New("email: permanent")
    ErrConfig           = errors.New("email: misconfiguration (e.g., domain not verified)")
    ErrAccountSuspended = errors.New("email: SES account paused")
    ErrRateLimited      = errors.New("email: rate limited")
    ErrInvalidInput     = errors.New("email: invalid input")
    ErrNoRecipient      = errors.New("email: no recipient")
)
```

## Bounce / Complaint Handling

SES forwards bounces and complaints to an SNS topic; we subscribe via HTTPS endpoint:

```
POST /vendors/ses/sns
{
  "Type": "Notification",
  "Message": "{...bounce or complaint json...}",
  ...
}
```

The handler:
1. Verifies SNS message signature.
2. Parses bounce / complaint.
3. Marks recipient address in `email_suppression` table; future sends to that address are blocked at the notifications-service preference layer.

```sql
CREATE TABLE email_suppression (
    address     text       PRIMARY KEY,
    reason      text       NOT NULL,           -- "hard_bounce" | "complaint" | "manual"
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    detail      jsonb
);
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Send` (simple) | 180 ms | 600 ms | dominated by SES API |
| `Send` (raw with PDF attachment) | 320 ms | 900 ms | + raw msg construction |
| `buildRawMessage` (without attachments) | 200 µs | 800 µs | |

## Failure Modes

| Failure | Class |
|---|---|
| SES paused (reputation) | `ErrAccountSuspended` — alert ops; suspend email channel via policy. |
| Domain not verified | `ErrConfig` — surfaced loudly at startup if `VerifiedIdentityCheck` fails. |
| Recipient bounced | persisted in `email_suppression`; future sends to that address suppressed. |
| Throttle | `ErrRateLimited` — river retries with backoff. |
| Body too large | `ErrInvalidInput` — surface to template author. |

## Startup Health Check

```go
// At startup, the email service verifies the From domain is a verified
// SES identity in this region.
func (a *Adapter) HealthCheck(ctx context.Context) error {
    out, err := a.client.GetEmailIdentity(ctx, &sesv2.GetEmailIdentityInput{
        EmailIdentity: aws.String(a.cfg.DefaultFrom),
    })
    if err != nil {
        return fmt.Errorf("email: identity lookup: %w", err)
    }
    if !out.VerifiedForSendingStatus {
        return errors.New("email: from address is not verified in SES")
    }
    return nil
}
```

## Testing

```go
func TestBuildRawMessage_NoAttachments(t *testing.T) { /* shape check */ }
func TestBuildRawMessage_WithPDFAttachment(t *testing.T) { /* MIME boundaries */ }
func TestClassifySESError_AllCodes(t *testing.T) { /* table-driven */ }
func TestSend_HappyPath_Mock(t *testing.T) {
    // Uses a fake AWS-SDK middleware to assert request shape and inject success.
}
func TestSendRaw_AttachmentBytesIntact(t *testing.T) { /* base64 round-trip */ }
```

## Open Questions

1. **DKIM per seller.** Send from `noreply@<seller-domain>` via SES per-tenant configuration. **Decision:** v0 single domain; v1+ adds.
2. **List-Unsubscribe headers.** Required by Gmail's bulk-sender policy. **Decision:** notifications service adds `List-Unsubscribe` for non-transactional kinds (digests).
3. **Bounce window cleanup.** `email_suppression` grows unbounded. **Decision:** purge `complaint`-type entries after 1 year; `hard_bounce` retained indefinitely.
4. **Reuse SES configuration set per environment.** `ConfigSet` is per-env (`pikshipp-prod`, `pikshipp-staging`).

## References

- LLD §03-services/20-notifications: consumer.
- LLD §02-infrastructure/05-secrets: `AccessKey`/`SecretKey` storage.
- LLD §04-adapters/05-msg91-sms: parallel structure.
