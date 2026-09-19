package main

import (
	"fmt"
	"time"

	"autoscaler/controller"
)

func comfortable() controller.Signals {
	return controller.Signals{CPUUtilization: 5, P95LatencyMillis: 100, RequestsPerTarget: 1}
}

func stressed() controller.Signals {
	return controller.Signals{CPUUtilization: 60, P95LatencyMillis: 1500, RequestsPerTarget: 12}
}

func transientDip() []controller.Signals {
	return []controller.Signals{
		stressed(), stressed(), stressed(),
		comfortable(), comfortable(), // caida de solo 2 ciclos
		stressed(), stressed(), stressed(), stressed(), stressed(),
	}
}

func sustainedDrop() []controller.Signals {
	return []controller.Signals{
		stressed(), stressed(), stressed(),
		comfortable(), comfortable(), comfortable(), comfortable(), comfortable(),
		comfortable(), comfortable(), comfortable(), comfortable(), comfortable(),
	}
}

func noisy() []controller.Signals {
	return []controller.Signals{
		comfortable(), comfortable(), stressed(), comfortable(),
		comfortable(), comfortable(), stressed(), comfortable(),
		comfortable(), stressed(), comfortable(), comfortable(),
		stressed(), comfortable(), comfortable(), comfortable(),
	}
}

func runScenario(signals []controller.Signals, confirmCycles int) {
	cfg := controller.DefaultPolicyConfig()
	cfg.ScaleDownConfirmCycles = confirmCycles

	// Aislamos el efecto de N: apagamos los cooldowns a proposito, para
	// que lo unico que varie entre corridas sea el numero de datapoints
	// de confirmacion, no otro mecanismo interfiriendo.
	cfg.ScaleDownCooldown = 0
	cfg.ScaleUpCooldown = 0

	state := controller.ControllerState{CurrentCapacity: 3}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	reduceCount, increaseCount, firstReduceAtCycle := 0, 0, -1

	for i, s := range signals {
		s.Timestamp = now
		state.RecordSignal(s, 10)
		result := controller.Decide(state, cfg, now)
		state.Apply(result, now)

		switch result.Decision {
		case controller.ReduceCapacity:
			reduceCount++
			if firstReduceAtCycle == -1 {
				firstReduceAtCycle = i + 1
			}
		case controller.IncreaseCapacity:
			increaseCount++
		}

		now = now.Add(1 * time.Minute)
	}

	fmt.Printf("  N=%d | bajadas=%d subidas=%d primera_bajada_en_ciclo=%d capacidad_final=%d\n",
		confirmCycles, reduceCount, increaseCount, firstReduceAtCycle, state.CurrentCapacity)
}

func main() {
	scenarios := []struct {
		name    string
		signals func() []controller.Signals
	}{
		{"Escenario 1 - caida transitoria (2 ciclos comodos, NO deberia bajar)", transientDip},
		{"Escenario 2 - bajada sostenida real (10 ciclos comodos seguidos)", sustainedDrop},
		{"Escenario 3 - ruido cerca del limite (sin patron sostenido claro)", noisy},
	}

	for _, sc := range scenarios {
		fmt.Println(sc.name)
		for _, n := range []int{2, 3, 4} {
			runScenario(sc.signals(), n)
		}
		fmt.Println()
	}
}