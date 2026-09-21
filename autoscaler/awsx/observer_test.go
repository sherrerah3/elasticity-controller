package awsx

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// --- mocks ---

type mockCW struct {
	out *cloudwatch.GetMetricDataOutput
	err error
}

func (m *mockCW) GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	return m.out, m.err
}

type mockELB struct {
	out *elasticloadbalancingv2.DescribeTargetHealthOutput
	err error
}

func (m *mockELB) DescribeTargetHealth(ctx context.Context, in *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error) {
	return m.out, m.err
}

func targetHealth(id string, state elbtypes.TargetHealthStateEnum) elbtypes.TargetHealthDescription {
	return elbtypes.TargetHealthDescription{
		Target:       &elbtypes.TargetDescription{Id: aws.String(id)},
		TargetHealth: &elbtypes.TargetHealth{State: state},
	}
}

func metricResult(id string, value float64) cwtypes.MetricDataResult {
	return cwtypes.MetricDataResult{Id: aws.String(id), Values: []float64{value}}
}

// P95 en segundos se convierte a ms; requests (conteo) se convierte a req/s; CPU promedia sanas.
func TestObserveConvertsUnits(t *testing.T) {
	elb := &mockELB{out: &elasticloadbalancingv2.DescribeTargetHealthOutput{
		TargetHealthDescriptions: []elbtypes.TargetHealthDescription{
			targetHealth("i-1", elbtypes.TargetHealthStateEnumHealthy),
			targetHealth("i-2", elbtypes.TargetHealthStateEnumHealthy),
		},
	}}
	cw := &mockCW{out: &cloudwatch.GetMetricDataOutput{
		MetricDataResults: []cwtypes.MetricDataResult{
			metricResult("p95", 1.2),  // 1.2 s -> 1200 ms
			metricResult("req", 480),  // 480 en 60s -> 8 req/s
			metricResult("cpu_0", 40), // i-1
			metricResult("cpu_1", 60), // i-2 -> promedio 50
		},
	}}

	obs := &Observer{CW: cw, ELB: elb, Window: 60 * time.Second}
	sig, err := obs.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe fallo: %v", err)
	}
	if !sig.Valid {
		t.Fatal("esperaba señal valida")
	}
	if sig.P95LatencyMillis != 1200 {
		t.Errorf("P95: esperaba 1200 ms, obtuvo %v", sig.P95LatencyMillis)
	}
	if sig.RequestsPerTarget != 8 {
		t.Errorf("requests: esperaba 8 req/s, obtuvo %v", sig.RequestsPerTarget)
	}
	if sig.CPUUtilization != 50 {
		t.Errorf("CPU: esperaba promedio 50, obtuvo %v", sig.CPUUtilization)
	}
	if sig.HealthyInstances != 2 || sig.TotalInstances != 2 {
		t.Errorf("salud: esperaba 2/2, obtuvo %d/%d", sig.HealthyInstances, sig.TotalInstances)
	}
}

// sin instancias sanas -> datos insuficientes (Valid=false), sin llamar a CloudWatch.
func TestObserveNoHealthyIsInvalid(t *testing.T) {
	elb := &mockELB{out: &elasticloadbalancingv2.DescribeTargetHealthOutput{
		TargetHealthDescriptions: []elbtypes.TargetHealthDescription{
			targetHealth("i-1", elbtypes.TargetHealthStateEnumUnhealthy),
		},
	}}
	obs := &Observer{CW: &mockCW{}, ELB: elb, Window: 60 * time.Second}

	sig, err := obs.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe fallo: %v", err)
	}
	if sig.Valid {
		t.Error("sin instancias sanas la señal debe ser invalida")
	}
	if sig.TotalInstances != 1 || sig.HealthyInstances != 0 {
		t.Errorf("salud: esperaba 0/1, obtuvo %d/%d", sig.HealthyInstances, sig.TotalInstances)
	}
}

// solo se promedia CPU de las sanas: una instancia unhealthy no aporta su serie.
func TestObserveAveragesCPUOnlyHealthy(t *testing.T) {
	elb := &mockELB{out: &elasticloadbalancingv2.DescribeTargetHealthOutput{
		TargetHealthDescriptions: []elbtypes.TargetHealthDescription{
			targetHealth("i-1", elbtypes.TargetHealthStateEnumHealthy),
			targetHealth("i-2", elbtypes.TargetHealthStateEnumUnhealthy), // no cuenta
		},
	}}
	cw := &mockCW{out: &cloudwatch.GetMetricDataOutput{
		MetricDataResults: []cwtypes.MetricDataResult{
			metricResult("p95", 0.1),
			metricResult("req", 60),
			metricResult("cpu_0", 30), // solo i-1 (unica sana)
		},
	}}
	obs := &Observer{CW: cw, ELB: elb, Window: 60 * time.Second}

	sig, err := obs.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe fallo: %v", err)
	}
	if sig.CPUUtilization != 30 {
		t.Errorf("CPU: esperaba 30 (solo la sana), obtuvo %v", sig.CPUUtilization)
	}
	if sig.HealthyInstances != 1 || sig.TotalInstances != 2 {
		t.Errorf("salud: esperaba 1/2, obtuvo %d/%d", sig.HealthyInstances, sig.TotalInstances)
	}
}
