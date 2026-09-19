package controller

import (
	"testing"
	"time"
)

// helper: crea una señal "de estrés" reutilizable en varios escenarios.
func stressedSignal(t time.Time) Signals {
	return Signals{Timestamp: t, CPUUtilization: 60, P95LatencyMillis: 1500, RequestsPerTarget: 12}
}

// helper: crea una señal "cómoda" reutilizable en varios escenarios.
func comfortableSignal(t time.Time) Signals {
	return Signals{Timestamp: t, CPUUtilization: 5, P95LatencyMillis: 100, RequestsPerTarget: 1}
}

func TestSustainedScaleUpTriggersOnConfirmCycle(t *testing.T) {
	cfg := DefaultPolicyConfig() // ScaleUpConfirmCycles = 2
	state := ControllerState{CurrentCapacity: 1}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Ciclo 1: primera señal de estrés — todavía no hay suficiente
	// historia para confirmar (se necesitan 2 ciclos consecutivos).
	state.RecordSignal(stressedSignal(now), 10)
	result := Decide(state, cfg, now)
	if result.Decision != MaintainCapacity {
		t.Errorf("ciclo 1: esperado MAINTAIN_CAPACITY, obtuvo %s", result.Decision)
	}

	// Ciclo 2: segunda señal de estrés consecutiva — ahora sí debe subir.
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	result = Decide(state, cfg, now)
	if result.Decision != IncreaseCapacity {
		t.Errorf("ciclo 2: esperado INCREASE_CAPACITY, obtuvo %s", result.Decision)
	}
}

func TestCooldownBlocksImmediateRescale(t *testing.T) {
	cfg := DefaultPolicyConfig()
	state := ControllerState{CurrentCapacity: 1}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Confirmamos la subida (2 ciclos de estrés).
	state.RecordSignal(stressedSignal(now), 10)
	Decide(state, cfg, now)
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	result := Decide(state, cfg, now)
	state.Apply(result, now) // ahora CurrentCapacity=2, LastScaleUp=now

	if state.CurrentCapacity != 2 {
		t.Fatalf("esperaba capacidad 2 tras la subida, obtuvo %d", state.CurrentCapacity)
	}

	// Un minuto después, el estrés continúa — pero seguimos en cooldown
	// (el cooldown de subida es de 3 minutos), así que NO debe subir de nuevo.
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	result = Decide(state, cfg, now)
	if result.Decision != MaintainCapacity {
		t.Errorf("en cooldown: esperado MAINTAIN_CAPACITY, obtuvo %s", result.Decision)
	}

	// Pasan 3 minutos más (ya se cumplió el cooldown completo) y el
	// estrés sigue sostenido — ahora sí debe poder subir de nuevo.
	now = now.Add(3 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	result = Decide(state, cfg, now)
	if result.Decision != IncreaseCapacity {
		t.Errorf("tras cooldown: esperado INCREASE_CAPACITY, obtuvo %s", result.Decision)
	}
}

func TestTransientDipDoesNotTriggerScaleDown(t *testing.T) {
	cfg := DefaultPolicyConfig() // ScaleDownConfirmCycles = 3
	state := ControllerState{
		CurrentCapacity: 2,
		LastScaleDown:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // fuera de cooldown desde el inicio
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Dos ciclos cómodos (una caída transitoria)...
	state.RecordSignal(comfortableSignal(now), 10)
	now = now.Add(1 * time.Minute)
	state.RecordSignal(comfortableSignal(now), 10)

	// ...pero el tercero vuelve a mostrar estrés (la caída no era real).
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	result := Decide(state, cfg, now)

	if result.Decision == ReduceCapacity {
		t.Errorf("una caida transitoria de 2 ciclos no deberia bajar capacidad con confirm_cycles=3, obtuvo %s", result.Decision)
	}
}

func TestNeverExceedsMaxInstances(t *testing.T) {
	cfg := DefaultPolicyConfig()
	state := ControllerState{CurrentCapacity: cfg.MaxInstances} // ya en el tope (5)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		now = now.Add(1 * time.Minute)
		state.RecordSignal(stressedSignal(now), 10)
		result := Decide(state, cfg, now)
		state.Apply(result, now)
	}

	if state.CurrentCapacity != cfg.MaxInstances {
		t.Errorf("la capacidad nunca debe superar MaxInstances (%d), quedo en %d", cfg.MaxInstances, state.CurrentCapacity)
	}
}

func TestNeverGoesBelowMinInstances(t *testing.T) {
	cfg := DefaultPolicyConfig()
	state := ControllerState{CurrentCapacity: cfg.MinInstances} // ya en el minimo (1)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		now = now.Add(1 * time.Minute)
		state.RecordSignal(comfortableSignal(now), 10)
		result := Decide(state, cfg, now)
		state.Apply(result, now)
	}

	if state.CurrentCapacity != cfg.MinInstances {
		t.Errorf("la capacidad nunca debe bajar de MinInstances (%d), quedo en %d", cfg.MinInstances, state.CurrentCapacity)
	}
}