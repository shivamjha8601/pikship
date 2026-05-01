# S3 Object Store Adapter

## Purpose

Pikshipp stores binary blobs — shipping labels, manifests, KYC documents, CSV uploads, support attachments, signed contracts, daily/monthly reports — in S3-compatible object storage. This adapter is the **single, narrow** interface for blob put/get/delete; it hides the AWS SDK and lets us use any S3-compatible store (Cloudflare R2, MinIO in dev, etc.).

## Package Layout

```
internal/objstore/
├── interface.go           // Store interface
├── errors.go
├── s3/
│   ├── adapter.go
│   ├── adapter_test.go
└── memory/                // For tests / dev
    └── adapter.go
```

## Interface

```go
package objstore

type Store interface {
    Put(ctx context.Context, key string, body io.Reader, opts PutOptions) error
    PutBytes(ctx context.Context, key string, body []byte, opts PutOptions) error
    Get(ctx context.Context, key string) (io.ReadCloser, *Metadata, error)
    GetBytes(ctx context.Context, key string) ([]byte, *Metadata, error)
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
    PresignPut(ctx context.Context, key string, ttl time.Duration, opts PresignPutOptions) (string, error)
    Delete(ctx context.Context, key string) error
    Stat(ctx context.Context, key string) (*Metadata, error)
}

type PutOptions struct {
    ContentType        string
    ContentDisposition string         // e.g., `attachment; filename="manifest.pdf"`
    CacheControl       string
    ACL                string         // "private" (default) | "public-read"
    Metadata           map[string]string  // user-defined headers (x-amz-meta-*)
    Tags               map[string]string  // S3 object tags
    SSE                string         // "AES256" | "aws:kms"
    SSEKMSKeyID        string
}

type Metadata struct {
    Size        int64
    ETag        string
    ContentType string
    LastModified time.Time
    Metadata    map[string]string
}

type PresignPutOptions struct {
    ContentType string
    MaxBytes    int64
}
```

## Sentinel Errors

```go
var (
    ErrNotFound = errors.New("objstore: not found")
    ErrTooLarge = errors.New("objstore: object too large")
    ErrAccess   = errors.New("objstore: access denied")
    ErrTransient = errors.New("objstore: transient")
)
```

## S3 Adapter

```go
package s3

import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

type Config struct {
    Region      string
    Bucket      string
    Endpoint    string                  // optional — for R2 / MinIO
    AccessKey   secrets.Secret[string]
    SecretKey   secrets.Secret[string]
    PathStyle   bool                    // true for MinIO, R2-compat
    DefaultSSE  string                  // "AES256" | "aws:kms"
    DefaultKMSKey string
    Logger      *slog.Logger
}

type Adapter struct {
    cfg     Config
    client  *s3.Client
    presign *s3.PresignClient
}

func New(ctx context.Context, cfg Config) (*Adapter, error) {
    awscfg, err := loadAWSConfig(ctx, cfg)
    if err != nil {
        return nil, err
    }
    client := s3.NewFromConfig(awscfg, func(o *s3.Options) {
        o.UsePathStyle = cfg.PathStyle
    })
    return &Adapter{
        cfg:     cfg,
        client:  client,
        presign: s3.NewPresignClient(client),
    }, nil
}
```

### Put

```go
func (a *Adapter) Put(ctx context.Context, key string, body io.Reader, opts objstore.PutOptions) error {
    in := &s3.PutObjectInput{
        Bucket:             aws.String(a.cfg.Bucket),
        Key:                aws.String(key),
        Body:               body,
        ContentType:        nilOrPtr(opts.ContentType),
        ContentDisposition: nilOrPtr(opts.ContentDisposition),
        CacheControl:       nilOrPtr(opts.CacheControl),
        Metadata:           opts.Metadata,
        ServerSideEncryption: types.ServerSideEncryption(coalesceStr(opts.SSE, a.cfg.DefaultSSE)),
        SSEKMSKeyId:        nilOrPtr(coalesceStr(opts.SSEKMSKeyID, a.cfg.DefaultKMSKey)),
    }
    if opts.ACL != "" {
        in.ACL = types.ObjectCannedACL(opts.ACL)
    }
    _, err := a.client.PutObject(ctx, in)
    if err != nil {
        return classifyS3Error(err)
    }
    return nil
}

func (a *Adapter) PutBytes(ctx context.Context, key string, body []byte, opts objstore.PutOptions) error {
    return a.Put(ctx, key, bytes.NewReader(body), opts)
}
```

### Get

```go
func (a *Adapter) Get(ctx context.Context, key string) (io.ReadCloser, *objstore.Metadata, error) {
    out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(a.cfg.Bucket),
        Key:    aws.String(key),
    })
    if err != nil {
        return nil, nil, classifyS3Error(err)
    }
    return out.Body, &objstore.Metadata{
        Size:        aws.ToInt64(out.ContentLength),
        ETag:        aws.ToString(out.ETag),
        ContentType: aws.ToString(out.ContentType),
        LastModified: aws.ToTime(out.LastModified),
        Metadata:    out.Metadata,
    }, nil
}

func (a *Adapter) GetBytes(ctx context.Context, key string) ([]byte, *objstore.Metadata, error) {
    rc, md, err := a.Get(ctx, key)
    if err != nil {
        return nil, nil, err
    }
    defer rc.Close()
    body, err := io.ReadAll(io.LimitReader(rc, 100<<20)) // 100 MB safety cap
    if err != nil {
        return nil, nil, err
    }
    return body, md, nil
}
```

### Presigned URLs

Presigned URLs let buyers download labels and sellers upload CSVs directly to S3 without proxying through our API.

```go
func (a *Adapter) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
    out, err := a.presign.PresignGetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(a.cfg.Bucket),
        Key:    aws.String(key),
    }, s3.WithPresignExpires(ttl))
    if err != nil {
        return "", classifyS3Error(err)
    }
    return out.URL, nil
}

func (a *Adapter) PresignPut(ctx context.Context, key string, ttl time.Duration, opts objstore.PresignPutOptions) (string, error) {
    in := &s3.PutObjectInput{
        Bucket:        aws.String(a.cfg.Bucket),
        Key:           aws.String(key),
        ContentType:   nilOrPtr(opts.ContentType),
        ContentLength: aws.Int64(opts.MaxBytes),
    }
    out, err := a.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(ttl))
    if err != nil {
        return "", classifyS3Error(err)
    }
    return out.URL, nil
}
```

### Stat / Delete

```go
func (a *Adapter) Stat(ctx context.Context, key string) (*objstore.Metadata, error) {
    out, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{
        Bucket: aws.String(a.cfg.Bucket), Key: aws.String(key),
    })
    if err != nil {
        return nil, classifyS3Error(err)
    }
    return &objstore.Metadata{
        Size: aws.ToInt64(out.ContentLength),
        ETag: aws.ToString(out.ETag),
        ContentType: aws.ToString(out.ContentType),
        LastModified: aws.ToTime(out.LastModified),
        Metadata: out.Metadata,
    }, nil
}

func (a *Adapter) Delete(ctx context.Context, key string) error {
    _, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
        Bucket: aws.String(a.cfg.Bucket), Key: aws.String(key),
    })
    if err != nil {
        return classifyS3Error(err)
    }
    return nil
}
```

### Error Classification

```go
func classifyS3Error(err error) error {
    var nsk *types.NoSuchKey
    if errors.As(err, &nsk) {
        return objstore.ErrNotFound
    }
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) {
        switch apiErr.ErrorCode() {
        case "NoSuchKey", "NotFound":
            return objstore.ErrNotFound
        case "AccessDenied", "Forbidden":
            return objstore.ErrAccess
        case "EntityTooLarge":
            return objstore.ErrTooLarge
        }
    }
    return fmt.Errorf("%w: %v", objstore.ErrTransient, err)
}
```

## Key Conventions

```go
// Object keys use seller-prefixed paths so we can shard or migrate per
// seller later. Prefix each key with a stable kind so listing by prefix
// works for ops needs.

func LabelKey(sellerID core.SellerID, shipmentID core.ShipmentID, format string) string {
    return fmt.Sprintf("labels/%s/%s.%s", sellerID, shipmentID, formatExt(format))
}
func ManifestKey(sellerID core.SellerID, manifestID core.ManifestID) string {
    return fmt.Sprintf("manifests/%s/%s.pdf", sellerID, manifestID)
}
func CSVUploadKey(sellerID core.SellerID, kind string) string {
    return fmt.Sprintf("uploads/%s/%s/%s.csv", sellerID, kind, ulid.Make())
}
func KYCDocKey(sellerID core.SellerID, docID string) string {
    return fmt.Sprintf("kyc/%s/%s", sellerID, docID)
}
func ReportKey(sellerID core.SellerID, kind string, day time.Time) string {
    return fmt.Sprintf("reports/%s/%s/%s.csv", sellerID, kind, day.Format("2006-01-02"))
}
```

## Memory Adapter (Tests)

```go
package memory

type Adapter struct {
    mu      sync.Mutex
    objects map[string]storedObject
}

type storedObject struct {
    body     []byte
    metadata objstore.Metadata
}

func New() *Adapter {
    return &Adapter{objects: make(map[string]storedObject)}
}

func (a *Adapter) PutBytes(ctx context.Context, key string, body []byte, opts objstore.PutOptions) error {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.objects[key] = storedObject{
        body: append([]byte(nil), body...),
        metadata: objstore.Metadata{
            Size: int64(len(body)), ContentType: opts.ContentType,
            LastModified: time.Now(), Metadata: opts.Metadata,
            ETag: fmt.Sprintf("%x", sha256.Sum256(body)),
        },
    }
    return nil
}

func (a *Adapter) GetBytes(ctx context.Context, key string) ([]byte, *objstore.Metadata, error) {
    a.mu.Lock()
    defer a.mu.Unlock()
    obj, ok := a.objects[key]
    if !ok {
        return nil, nil, objstore.ErrNotFound
    }
    return append([]byte(nil), obj.body...), &obj.metadata, nil
}

// PresignGet returns a fake URL with the key embedded; tests can read it.
func (a *Adapter) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
    return "memory://" + key, nil
}

// ... Put / Get / Delete / Stat
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `PutBytes` (10 KB) | 25 ms | 80 ms | dominated by S3 RTT |
| `PutBytes` (1 MB) | 80 ms | 250 ms | |
| `GetBytes` (cached, ~10 KB) | 18 ms | 60 ms | |
| `PresignGet` | 1 ms | 3 ms | local signing only |
| `PresignPut` | 1 ms | 3 ms | local signing only |

## Failure Modes

| Failure | Class | Handling |
|---|---|---|
| Object missing | `ErrNotFound` | caller decides (404 to user vs. fail-fast) |
| Bucket policy denies | `ErrAccess` | startup health check should catch |
| Region mismatch | wrapped transient | startup verify bucket region |
| Network blip | `ErrTransient` | river job retries |
| Body > 100 MB | `ErrTooLarge` (in `GetBytes`) | callers that expect huge files use `Get` (streaming) instead |

## Startup Health Check

```go
func (a *Adapter) HealthCheck(ctx context.Context) error {
    _, err := a.client.HeadBucket(ctx, &s3.HeadBucketInput{
        Bucket: aws.String(a.cfg.Bucket),
    })
    return classifyS3Error(err)
}
```

## Lifecycle Policies

The S3 bucket has lifecycle rules configured via Terraform:
- `uploads/*`: delete after 30 days.
- `labels/*`: transition to GLACIER after 90 days.
- `manifests/*`: delete after 365 days.
- `kyc/*`: retain indefinitely.
- `reports/*`: delete after 365 days.

These rules are infra-as-code, not runtime concerns. Adapter has no special path for them.

## Testing

```go
func TestPutGetRoundTrip_MemoryAdapter(t *testing.T) { /* ... */ }
func TestKeyConventions_Stable(t *testing.T) { /* identical inputs → identical keys */ }
func TestClassifyS3Error_NotFound(t *testing.T) { /* ... */ }
func TestPresignGet_URLContainsExpiry(t *testing.T) { /* ... */ }
func TestS3Adapter_E2E_MockMinIO_SLT(t *testing.T) {
    // Requires testcontainers/minio. Verify against real S3 API surface.
}
```

## Open Questions

1. **Multipart uploads for large files.** SDK auto-multipart kicks in at 16 MB. **Decision:** rely on default; revisit if we ship monthly statements > 100 MB.
2. **Cross-region replication.** Disaster recovery is out of scope for v0; v1+ enable CRR for `kyc/*` only.
3. **Encryption at rest.** SSE-S3 (AES-256) is the default; KMS is configurable per-key for KYC docs only at v0. **Decision:** acceptable; expand to PII paths in v1+.
4. **Per-seller storage quotas.** Out of scope; ops monitors via S3 metrics.
5. **Public buckets.** None. We presign all reads, even public-buyer tracking page assets.

## References

- LLD §03-services/13-shipments: label / manifest storage.
- LLD §03-services/16-cod: remittance file storage.
- LLD §03-services/19-reports: scheduled exports.
- LLD §03-services/22-support: attachment refs.
- LLD §02-infrastructure/05-secrets: AWS credentials.
