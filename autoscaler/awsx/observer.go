package awsx

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"autoscaler/controller"
)

// subconjunto del SDK de CloudWatch que usamos (mockeable en tests).
type cloudwatchAPI interface {
	GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

// subconjunto del SDK de ELBv2 que usamos (mockeable en tests).
type elbAPI interface {
	DescribeTargetHealth(ctx context.Context, in *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error)
}

// observa CloudWatch + ALB y arma un controller.Signals por ciclo.
type Observer struct {
	CW             cloudwatchAPI
	ELB            elbAPI
	TargetGroupARN string
	TGDimension    string        // valor de la dimension TargetGroup (targetgroup/.../id)
	LBDimension    string        // valor de la dimension LoadBalancer (app/.../id)
	Lookback       time.Duration // cuanto se mira hacia atras (amplio, p.ej. 5 min) para no perder datapoints
}

// segundos del periodo de agregacion de CloudWatch (cada datapoint cubre 60s).
const periodSecs = 60.0

// implementa controller.MetricsObserver.
func (o *Observer) Observe(ctx context.Context) (controller.Signals, error) {
	now := time.Now()
	sig := controller.Signals{Timestamp: now}

	healthyIDs, healthy, total, err := o.targetHealth(ctx)
	if err != nil {
		return controller.Signals{Timestamp: now, Valid: false}, err
	}
	sig.HealthyInstances = healthy
	sig.TotalInstances = total

	// sin instancias sanas no hay analisis de demanda posible: datos insuficientes.
	if healthy == 0 {
		sig.Valid = false
		return sig, nil
	}

	// ventana amplia hacia atras: CloudWatch publica los datapoints del ALB con
	// 1-3 min de retraso, asi que se piden varios minutos y se toma el mas reciente.
	lookback := o.Lookback
	if lookback <= 0 {
		lookback = 5 * time.Minute
	}
	start := now.Add(-lookback)
	queries := o.buildQueries(healthyIDs)

	out, err := o.CW.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		StartTime:         aws.Time(start),
		EndTime:           aws.Time(now),
		MetricDataQueries: queries,
	})
	if err != nil {
		return controller.Signals{Timestamp: now, Valid: false}, err
	}

	results := indexResults(out.MetricDataResults)

	// P95 de latencia (segundos -> ms). Señal principal.
	p95, okP95 := latestValue(results["p95"])
	sig.P95LatencyMillis = p95 * 1000.0

	// requests por target: el datapoint es la suma de UN periodo (60s) -> req/s.
	// Se divide por el periodo, no por la ventana de consulta.
	reqCount, okReq := latestValue(results["req"])
	sig.RequestsPerTarget = reqCount / periodSecs

	// CPU promedio SOLO sobre instancias sanas.
	cpu, okCPU := averageCPU(results, healthyIDs)
	sig.CPUUtilization = cpu

	// datos suficientes si tenemos al menos P95 y CPU (las dos señales de la politica).
	sig.Valid = okP95 && okCPU
	_ = okReq // requests no es condicion autonoma; no gatea la validez

	return sig, nil
}

// estado por instancia (id -> TargetState). Implementa controller.HealthReporter.
func (o *Observer) InstanceHealth(ctx context.Context) (map[string]controller.TargetState, error) {
	out, err := o.ELB.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(o.TargetGroupARN),
	})
	if err != nil {
		return nil, err
	}
	states := make(map[string]controller.TargetState, len(out.TargetHealthDescriptions))
	for _, d := range out.TargetHealthDescriptions {
		if d.Target == nil || d.Target.Id == nil || d.TargetHealth == nil {
			continue
		}
		states[*d.Target.Id] = mapTargetState(d.TargetHealth.State)
	}
	return states, nil
}

// traduce el estado del SDK al TargetState del dominio.
func mapTargetState(s elbtypes.TargetHealthStateEnum) controller.TargetState {
	switch s {
	case elbtypes.TargetHealthStateEnumHealthy:
		return controller.TargetHealthy
	case elbtypes.TargetHealthStateEnumUnhealthy:
		return controller.TargetUnhealthy
	case elbtypes.TargetHealthStateEnumInitial:
		return controller.TargetInitial
	case elbtypes.TargetHealthStateEnumDraining:
		return controller.TargetDraining
	case elbtypes.TargetHealthStateEnumUnused:
		return controller.TargetUnused
	default:
		return controller.TargetInitial // desconocido: tratar como no-falla
	}
}

// cuenta salud y devuelve los InstanceId sanos.
func (o *Observer) targetHealth(ctx context.Context) (healthyIDs []string, healthy, total int, err error) {
	out, err := o.ELB.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(o.TargetGroupARN),
	})
	if err != nil {
		return nil, 0, 0, err
	}
	for _, d := range out.TargetHealthDescriptions {
		total++
		if d.TargetHealth != nil && d.TargetHealth.State == elbtypes.TargetHealthStateEnumHealthy {
			healthy++
			if d.Target != nil && d.Target.Id != nil {
				healthyIDs = append(healthyIDs, *d.Target.Id)
			}
		}
	}
	return healthyIDs, healthy, total, nil
}
