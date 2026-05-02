package slt

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tclocalstack "github.com/testcontainers/testcontainers-go/modules/localstack"
)

// LocalStackEndpoints holds resolved URLs for each service.
type LocalStackEndpoints struct {
	S3Endpoint  string
	SESEndpoint string
	Region      string
}

// NewLocalStack starts a LocalStack container with S3 and SES enabled
// and returns the service endpoints. Container is torn down via t.Cleanup.
func NewLocalStack(t *testing.T) LocalStackEndpoints {
	t.Helper()
	ctx := context.Background()

	ctr, err := tclocalstack.Run(ctx,
		"localstack/localstack:4",
		testcontainers.WithEnv(map[string]string{
			"SERVICES":       "s3,ses",
			"DEFAULT_REGION": "us-east-1",
		}),
	)
	if err != nil {
		t.Fatalf("slt.NewLocalStack: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	// Give LocalStack a moment to initialise services.
	time.Sleep(1 * time.Second)

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("slt.NewLocalStack: host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "4566")
	if err != nil {
		t.Fatalf("slt.NewLocalStack: port: %v", err)
	}

	base := "http://" + host + ":" + port.Port()
	return LocalStackEndpoints{
		S3Endpoint:  base,
		SESEndpoint: base,
		Region:      "us-east-1",
	}
}
