package controller

import "time"

// señales observadas en un ciclo de evaluación.
type Signals struct {
	Timestamp         time.Time `json:"timestamp"`
	CPUUtilization    float64   `json:"cpu_utilization"`
	P95LatencyMillis  float64   `json:"p95_latency_millis"`
	RequestsPerTarget float64   `json:"requests_per_target"`
	HealthyInstances  int       `json:"healthy_instances"`
	TotalInstances    int       `json:"total_instances"`

	// false si faltan datos este ciclo; una señal invalida nunca provoca escalado.
	Valid bool `json:"valid"`
}

// resultado que el controlador debe producir en cada ciclo.
type Decision string

const (
	MaintainCapacity Decision = "MAINTAIN_CAPACITY"
	IncreaseCapacity Decision = "INCREASE_CAPACITY"
	ReduceCapacity   Decision = "REDUCE_CAPACITY"
)

// empaquetar la decisión junto con su justificación y metricas
type DecisionResult struct {
	Decision      Decision
	Justification string
	Reason        string
	Signal        Signals
	Capacity      int
}

// memoria del controlador entre ciclos de evaluación.
type ControllerState struct {
	CurrentCapacity int
	History         []Signals
	LastScaleUp     time.Time
	LastScaleDown   time.Time
}

// actualizar estado del controlador según decisión tomada (pointer para modificar struct original)
func (state *ControllerState) Apply(result DecisionResult, now time.Time) {
	switch result.Decision {
	case IncreaseCapacity:
		state.CurrentCapacity++
		state.LastScaleUp = now
	case ReduceCapacity:
		state.CurrentCapacity--
		state.LastScaleDown = now
	}
}

// añadir señal nueva al historial
func (state *ControllerState) RecordSignal(s Signals, maxHistory int) {
	state.History = append(state.History, s)
	if len(state.History) > maxHistory {
		state.History = state.History[len(state.History)-maxHistory:]
	}
}
