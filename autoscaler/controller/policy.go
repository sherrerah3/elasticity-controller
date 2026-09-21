package controller

import (
	"fmt"
	"time"
)

// umbrales y parámetros de la política de decisión
type PolicyConfig struct {
	ScaleUpCPUThreshold      float64
	ScaleUpP95Threshold      float64
	ScaleUpRequestsThreshold float64

	ScaleDownCPUThreshold float64
	ScaleDownP95Threshold float64

	ScaleUpConfirmCycles   int
	ScaleDownConfirmCycles int

	ScaleUpCooldown   time.Duration
	ScaleDownCooldown time.Duration

	MinInstances int
	MaxInstances int

	// N chequeos unhealthy consecutivos para disparar reemplazo (auto-sanacion).
	UnhealthyReplaceCycles int
}

// configuración definida en Exp01.
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		ScaleUpCPUThreshold:      45.0,
		ScaleUpP95Threshold:      1000.0,
		ScaleUpRequestsThreshold: 8.0,

		ScaleDownCPUThreshold: 15.0,
		ScaleDownP95Threshold: 200.0,

		ScaleUpConfirmCycles:   2,
		ScaleDownConfirmCycles: 3,

		ScaleUpCooldown:   3 * time.Minute,
		ScaleDownCooldown: 5 * time.Minute,

		MinInstances: 1,
		MaxInstances: 5,

		UnhealthyReplaceCycles: 3,
	}
}

// condicion para agregar instancias
func scaleUpBreach(s Signals, cfg PolicyConfig) bool {
	latencyAlert := s.P95LatencyMillis > cfg.ScaleUpP95Threshold
	loadAlert := s.RequestsPerTarget > cfg.ScaleUpRequestsThreshold &&
		s.CPUUtilization > cfg.ScaleUpCPUThreshold

	return latencyAlert || loadAlert
}

// condicion para reducir instancias
func scaleDownComfortable(s Signals, cfg PolicyConfig) bool {
	return s.CPUUtilization < cfg.ScaleDownCPUThreshold &&
		s.P95LatencyMillis < cfg.ScaleDownP95Threshold
}

func scaleUpReason(s Signals, cfg PolicyConfig) string {
	latencyAlert := s.P95LatencyMillis > cfg.ScaleUpP95Threshold
	loadAlert := s.RequestsPerTarget > cfg.ScaleUpRequestsThreshold &&
		s.CPUUtilization > cfg.ScaleUpCPUThreshold

	switch {
	case latencyAlert && loadAlert:
		return "p95_latency_and_load"
	case latencyAlert:
		return "p95_latency"
	default:
		return "load"
	}
}

// verificar que ultimas n mediciones cumplen una condicion
func lastNSatisfy(history []Signals, n int, condition func(Signals) bool) bool {
	if n <= 0 || len(history) < n {
		return false
	}
	recent := history[len(history)-n:]
	for _, s := range recent {
		if !condition(s) {
			return false
		}
	}
	return true
}

// Decide si puede escalar hacia arriba/abajo y lo justifica
func Decide(state ControllerState, cfg PolicyConfig, now time.Time) DecisionResult {
	latestSignal := Signals{}
	if len(state.History) > 0 {
		latestSignal = state.History[len(state.History)-1]
	}

	// guard de datos insuficientes (diseño 5.5): ante metricas faltantes o
	// invalidas no se actua, para no asumir el mejor ni el peor caso.
	if len(state.History) == 0 || !latestSignal.Valid {
		return DecisionResult{
			Decision:      MaintainCapacity,
			Justification: "datos insuficientes: metricas faltantes, retrasadas o invalidas",
			Reason:        "insufficient_data",
			Signal:        latestSignal,
			Capacity:      state.CurrentCapacity,
		}
	}

	canScaleUp := state.CurrentCapacity < cfg.MaxInstances &&
		now.Sub(state.LastScaleUp) >= cfg.ScaleUpCooldown

	if canScaleUp && lastNSatisfy(state.History, cfg.ScaleUpConfirmCycles, func(s Signals) bool {
		return scaleUpBreach(s, cfg)
	}) {
		return DecisionResult{
			Decision:      IncreaseCapacity,
			Justification: fmt.Sprintf("umbral de estres sostenido por %d ciclos consecutivos", cfg.ScaleUpConfirmCycles),
			Reason:        scaleUpReason(latestSignal, cfg),
			Signal:        latestSignal,
			Capacity:      state.CurrentCapacity,
		}
	}

	canScaleDown := state.CurrentCapacity > cfg.MinInstances &&
		now.Sub(state.LastScaleDown) >= cfg.ScaleDownCooldown &&
		now.Sub(state.LastScaleUp) >= cfg.ScaleUpCooldown

	if canScaleDown && lastNSatisfy(state.History, cfg.ScaleDownConfirmCycles, func(s Signals) bool {
		return scaleDownComfortable(s, cfg)
	}) {
		return DecisionResult{
			Decision:      ReduceCapacity,
			Justification: fmt.Sprintf("sistema comodo sostenido por %d ciclos consecutivos", cfg.ScaleDownConfirmCycles),
			Reason:        "low_cpu_and_latency",
			Signal:        latestSignal,
			Capacity:      state.CurrentCapacity,
		}
	}

	return DecisionResult{
		Decision:      MaintainCapacity,
		Justification: "sin condiciones sostenidas de subida ni bajada, o en cooldown, o en limite min/max",
		Reason:        "no_sustained_condition",
		Signal:        latestSignal,
		Capacity:      state.CurrentCapacity,
	}
}
