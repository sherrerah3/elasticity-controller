package awsx

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

// clientes del SDK listos para usar (CloudWatch, ELBv2, EC2).
type Clients struct {
	CW  *cloudwatch.Client
	ELB *elasticloadbalancingv2.Client
	EC2 *ec2.Client
}

// carga credenciales/region y configura el retryer del SDK (backoff exponencial
// para errores reintentables: throttling, 5xx). No implementamos backoff propio.
func NewClients(ctx context.Context, region string, maxRetries int) (*Clients, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithRetryMaxAttempts(maxRetries),
	)
	if err != nil {
		return nil, err
	}
	return &Clients{
		CW:  cloudwatch.NewFromConfig(cfg),
		ELB: elasticloadbalancingv2.NewFromConfig(cfg),
		EC2: ec2.NewFromConfig(cfg),
	}, nil
}
