package awsx

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

const (
	period       = 60 // segundos de agregacion en CloudWatch
	cpuIDPrefix  = "cpu_"
	namespaceEC2 = "AWS/EC2"
	namespaceALB = "AWS/ApplicationELB"
)

// arma las queries de GetMetricData: P95 latencia, requests por target y CPU por instancia sana.
func (o *Observer) buildQueries(healthyIDs []string) []cwtypes.MetricDataQuery {
	queries := []cwtypes.MetricDataQuery{
		{
			Id:    aws.String("p95"),
			Label: aws.String("p95_latency"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(namespaceALB),
					MetricName: aws.String("TargetResponseTime"),
					Dimensions: o.albDimensions(),
				},
				Period: aws.Int32(period),
				Stat:   aws.String("p95"),
			},
		},
		{
			Id:    aws.String("req"),
			Label: aws.String("requests_per_target"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(namespaceALB),
					MetricName: aws.String("RequestCountPerTarget"),
					Dimensions: []cwtypes.Dimension{
						{Name: aws.String("TargetGroup"), Value: aws.String(o.TGDimension)},
					},
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Sum"),
			},
		},
	}

	// una query de CPU (Average) por cada instancia sana.
	for i, id := range healthyIDs {
		queries = append(queries, cwtypes.MetricDataQuery{
			Id:    aws.String(fmt.Sprintf("%s%d", cpuIDPrefix, i)),
			Label: aws.String("cpu_" + id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(namespaceEC2),
					MetricName: aws.String("CPUUtilization"),
					Dimensions: []cwtypes.Dimension{
						{Name: aws.String("InstanceId"), Value: aws.String(id)},
					},
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Average"),
			},
		})
	}
	return queries
}

// dimensiones del ALB para TargetResponseTime (TargetGroup + LoadBalancer).
func (o *Observer) albDimensions() []cwtypes.Dimension {
	dims := []cwtypes.Dimension{
		{Name: aws.String("TargetGroup"), Value: aws.String(o.TGDimension)},
	}
	if o.LBDimension != "" {
		dims = append(dims, cwtypes.Dimension{Name: aws.String("LoadBalancer"), Value: aws.String(o.LBDimension)})
	}
	return dims
}

// indexa los resultados de GetMetricData por su Id.
func indexResults(results []cwtypes.MetricDataResult) map[string]cwtypes.MetricDataResult {
	m := make(map[string]cwtypes.MetricDataResult, len(results))
	for _, r := range results {
		if r.Id != nil {
			m[*r.Id] = r
		}
	}
	return m
}

// devuelve el datapoint mas reciente de una serie (o false si no hay datos).
func latestValue(r cwtypes.MetricDataResult) (float64, bool) {
	if len(r.Values) == 0 {
		return 0, false
	}
	// GetMetricData devuelve los valores mas recientes primero.
	return r.Values[0], true
}

// promedia la CPU de las series cpu_* (solo instancias sanas). false si ninguna tiene datos.
func averageCPU(results map[string]cwtypes.MetricDataResult, healthyIDs []string) (float64, bool) {
	var sum float64
	var n int
	for i := range healthyIDs {
		id := fmt.Sprintf("%s%d", cpuIDPrefix, i)
		if v, ok := latestValue(results[id]); ok {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}
