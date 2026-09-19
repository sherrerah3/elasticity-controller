package controller

import "time"

// Signals agrupa las señales observadas en un ciclo de evaluación.
// Se calculan siempre sobre instancias sanas (capacidad efectiva).
type Signals struct {
	Timestamp         time.Time
	CPUUtilization    float64 // porcentaje, ej. 45.2
	P95LatencyMillis  float64 // milisegundos
	RequestsPerTarget float64 // req/s por instancia sana
	HealthyInstances  int
	TotalInstances    int
}

// Decision es el resultado que el controlador debe producir en cada ciclo.
// Los tres valores son literales exactos exigidos por el reto (sección 9).
type Decision string

const (
	MaintainCapacity Decision = "MAINTAIN_CAPACITY"
	IncreaseCapacity Decision = "INCREASE_CAPACITY"
	ReduceCapacity   Decision = "REDUCE_CAPACITY"
)

// DecisionResult empaqueta la decisión junto con su justificación, para
// el logging estructurado que exige el reto.
type DecisionResult struct {
	Decision      Decision
	Justification string
}

// ControllerState es la memoria del controlador entre ciclos de evaluación.
type ControllerState struct {
	CurrentCapacity int
	History         []Signals // ventana reciente de señales, más antiguo primero
	LastScaleUp     time.Time
	LastScaleDown   time.Time
}

// Apply actualiza el estado del controlador según la decisión tomada.
// Se usa un pointer receiver (*ControllerState) porque necesitamos
// modificar el struct original, no una copia.
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

// RecordSignal añade una señal nueva al historial, manteniendo como
// máximo maxHistory elementos (ventana deslizante, para no crecer
// indefinidamente en memoria).
func (state *ControllerState) RecordSignal(s Signals, maxHistory int) {
	state.History = append(state.History, s)
	if len(state.History) > maxHistory {
		state.History = state.History[len(state.History)-maxHistory:]
	}
}