package controller

import (
	"testing"
	"time"
)

// helper: crea una señal "de estrés" reutilizable en varios escenarios.
func stressedSignal(t time.Time) Signals {
	return Signals{Timestamp: t, CPUUtilization: 60, P95LatencyMillis: 1500, RequestsPerTarget: 12, Valid: true}
}

// helper: crea una señal "cómoda" reutilizable en varios escenarios.
func comfortableSignal(t time.Time) Signals {
	return Signals{Timestamp: t, CPUUtilization: 5, P95LatencyMillis: 100, RequestsPerTarget: 1, Valid: true}
}

// verificar que el sistema no escale con solo 1 señal de estres
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

// Verificar que el cooldown si este funcionando
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

// Testear que el cooldown de scale-up protege la instancia nueva durante el tiempo configurado
func TestScaleOutCooldownAlsoBlocksScaleDown(t *testing.T) {
	cfg := DefaultPolicyConfig()
	state := ControllerState{CurrentCapacity: 1}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	state.RecordSignal(stressedSignal(now), 10)
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	result := Decide(state, cfg, now)
	state.Apply(result, now) // LastScaleUp = 00:01

	if result.Decision != IncreaseCapacity {
		t.Fatalf("esperaba scale out, obtuvo %s", result.Decision)
	}

	// dentro del cooldown de subida: aunque haya ciclos comodos, no debe bajar.
	now = now.Add(2 * time.Minute) // 2 minutos después del scale-up
	state.RecordSignal(comfortableSignal(now), 10)
	now = now.Add(1 * time.Minute)
	state.RecordSignal(comfortableSignal(now), 10) // 2 ciclos cómodos

	result = Decide(state, cfg, now)
	if result.Decision != MaintainCapacity {
		t.Errorf("dentro del cooldown: esperado MAINTAIN_CAPACITY, obtuvo %s", result.Decision)
	}
}

// Verificar que una reducción no bloquea una posterior subida si ya pasó el cooldown de subida.
func TestScaleDownDoesNotBlockScaleUpAfterCooldown(t *testing.T) {
	cfg := DefaultPolicyConfig()
	state := ControllerState{
		CurrentCapacity: 2,
		LastScaleUp:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // hace mucho tiempo
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < cfg.ScaleDownConfirmCycles; i++ {
		state.RecordSignal(comfortableSignal(now), 10)
		now = now.Add(1 * time.Minute)
	}
	result := Decide(state, cfg, now)
	state.Apply(result, now) // 2 -> 1, LastScaleDown = now

	if result.Decision != ReduceCapacity {
		t.Fatalf("esperaba scale down, obtuvo %s", result.Decision)
	}

	// Después de una bajada, el sistema puede reaccionar ante estrés sostenido
	// si ya cumplió el cooldown de subida.
	now = now.Add(4 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)

	result = Decide(state, cfg, now)
	if result.Decision != IncreaseCapacity {
		t.Errorf("con estrés sostenido después de bajar, esperaba INCREASE_CAPACITY, obtuvo %s", result.Decision)
	}
}

// Comprobar que espere los ciclos correctos para reducir
func TestTransientDipDoesNotTriggerScaleDown(t *testing.T) {
	cfg := DefaultPolicyConfig() // ScaleDownConfirmCycles = 3
	state := ControllerState{
		CurrentCapacity: 2,
		LastScaleUp:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // fuera de cooldown desde el inicio
		LastScaleDown:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // fuera de cooldown desde el inicio
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Dos ciclos cómodos
	state.RecordSignal(comfortableSignal(now), 10)
	now = now.Add(1 * time.Minute)
	state.RecordSignal(comfortableSignal(now), 10)

	// el tercero vuelve a mostrar estrés (la caída no era real).
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)
	result := Decide(state, cfg, now)

	if result.Decision == ReduceCapacity {
		t.Errorf("una caida transitoria de 2 ciclos no deberia bajar capacidad con confirm_cycles=3, obtuvo %s", result.Decision)
	}
}

// comprobar maximo intancias
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

// comprobar minimo intancias
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

// helper: señal invalida (sin datos utilizables este ciclo).
func invalidSignal(t time.Time) Signals {
	return Signals{Timestamp: t, Valid: false}
}

// una señal invalida no debe disparar REDUCE aunque sus ceros "parezcan" comodos.
func TestInvalidSignalDoesNotTriggerScaleDown(t *testing.T) {
	cfg := DefaultPolicyConfig()
	state := ControllerState{
		CurrentCapacity: 2,
		LastScaleUp:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // fuera de cooldown
		LastScaleDown:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < cfg.ScaleDownConfirmCycles; i++ {
		state.RecordSignal(invalidSignal(now), 10)
		now = now.Add(1 * time.Minute)
	}
	result := Decide(state, cfg, now)

	if result.Decision != MaintainCapacity {
		t.Errorf("señal invalida: esperado MAINTAIN_CAPACITY, obtuvo %s", result.Decision)
	}
	if result.Reason != "insufficient_data" {
		t.Errorf("señal invalida: esperado reason insufficient_data, obtuvo %s", result.Reason)
	}
}

// una señal invalida en el ultimo ciclo bloquea un scale-up que si estaba confirmado.
func TestInvalidLatestSignalBlocksScaleUp(t *testing.T) {
	cfg := DefaultPolicyConfig() // ScaleUpConfirmCycles = 2
	state := ControllerState{CurrentCapacity: 1}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// dos ciclos de estres (racha suficiente para subir)...
	state.RecordSignal(stressedSignal(now), 10)
	now = now.Add(1 * time.Minute)
	state.RecordSignal(stressedSignal(now), 10)

	// ...pero el ciclo actual llega sin datos: no se debe actuar.
	now = now.Add(1 * time.Minute)
	state.RecordSignal(invalidSignal(now), 10)
	result := Decide(state, cfg, now)

	if result.Decision != MaintainCapacity {
		t.Errorf("ultima señal invalida: esperado MAINTAIN_CAPACITY, obtuvo %s", result.Decision)
	}
}

// sin historial, tampoco se actua.
func TestEmptyHistoryIsInsufficientData(t *testing.T) {
	cfg := DefaultPolicyConfig()
	state := ControllerState{CurrentCapacity: 3}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	result := Decide(state, cfg, now)
	if result.Decision != MaintainCapacity || result.Reason != "insufficient_data" {
		t.Errorf("historial vacio: esperado MAINTAIN_CAPACITY/insufficient_data, obtuvo %s/%s", result.Decision, result.Reason)
	}
}
