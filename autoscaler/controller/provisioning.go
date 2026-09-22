package controller

import "time"

// una medicion completa de aprovisionamiento de una instancia (t0/t1/t2).
type ProvisioningRecord struct {
	InstanceID    string    `json:"instance_id"`
	T0            time.Time `json:"t0"`                // RunInstances solicitado
	T1            time.Time `json:"t1"`                // EC2 reporta running
	T2            time.Time `json:"t2"`                // ALB reporta healthy
	AWSOverhead   float64   `json:"aws_overhead_secs"` // t1 - t0
	AppStartup    float64   `json:"app_startup_secs"`  // t2 - t1
	TotalProvSecs float64   `json:"total_prov_secs"`   // t2 - t0
}

// destino de las mediciones de aprovisionamiento.
type ProvisioningSink interface {
	Record(r ProvisioningRecord) error
}
