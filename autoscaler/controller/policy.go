package controller

import "time"

// PolicyConfig contiene todos los umbrales y parámetros ajustables de la
// política de decisión, como datos en vez de constantes dispersas.
type PolicyConfig struct {
	ScaleUpCPUThreshold         float64
	ScaleUpP95Threshold         float64
	ScaleUpRequestsThreshold    float64

	ScaleDownCPUThreshold float64
	ScaleDownP95Threshold float64

	ScaleUpConfirmCycles   int
	ScaleDownConfirmCycles int

	ScaleUpCooldown   time.Duration
	ScaleDownCooldown time.Duration

	MinInstances int
	MaxInstances int
}

// DefaultPolicyConfig devuelve la configuración definida a partir de
// EXP-01 (ver documento de diseño, sección 5).
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		ScaleUpCPUThreshold:         45.0,
		ScaleUpP95Threshold:         1000.0,
		ScaleUpRequestsThreshold:    8.0,

		ScaleDownCPUThreshold: 15.0,
		ScaleDownP95Threshold: 200.0,

		ScaleUpConfirmCycles:   2,
		ScaleDownConfirmCycles: 3,

		ScaleUpCooldown:   3 * time.Minute,
		ScaleDownCooldown: 5 * time.Minute,

		MinInstances: 1,
		MaxInstances: 5,
	}
}

func scaleUpBreach(s Signals, cfg PolicyConfig) bool {
	latencyAlert := s.P95LatencyMillis > cfg.ScaleUpP95Threshold
	loadAlert := s.RequestsPerTarget > cfg.ScaleUpRequestsThreshold &&
		s.CPUUtilization > cfg.ScaleUpCPUThreshold

	return latencyAlert || loadAlert
}

func scaleDownComfortable(s Signals, cfg PolicyConfig) bool {
	return s.CPUUtilization < cfg.ScaleDownCPUThreshold &&
		s.P95LatencyMillis < cfg.ScaleDownP95Threshold
}

func lastNSatisfy(history []Signals, n int, condition func(Signals) bool) bool {
	if len(history) < n {
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

func Decide(state ControllerState, cfg PolicyConfig, now time.Time) DecisionResult {
	canScaleUp := state.CurrentCapacity < cfg.MaxInstances &&
		now.Sub(state.LastScaleUp) >= cfg.ScaleUpCooldown

	if canScaleUp && lastNSatisfy(state.History, cfg.ScaleUpConfirmCycles, func(s Signals) bool {
		return scaleUpBreach(s, cfg)
	}) {
		return DecisionResult{
			Decision:      IncreaseCapacity,
			Justification: "umbral de estres sostenido por confirm_cycles ciclos consecutivos",
		}
	}

	canScaleDown := state.CurrentCapacity > cfg.MinInstances &&
		now.Sub(state.LastScaleDown) >= cfg.ScaleDownCooldown

	if canScaleDown && lastNSatisfy(state.History, cfg.ScaleDownConfirmCycles, func(s Signals) bool {
		return scaleDownComfortable(s, cfg)
	}) {
		return DecisionResult{
			Decision:      ReduceCapacity,
			Justification: "sistema comodo sostenido por confirm_cycles ciclos consecutivos",
		}
	}

	return DecisionResult{
		Decision:      MaintainCapacity,
		Justification: "sin condiciones sostenidas de subida ni bajada, o en cooldown, o en limite min/max",
	}
}