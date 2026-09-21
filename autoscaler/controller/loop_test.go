package controller

import (
	"context"
	"testing"
	"time"
)

// observador falso: devuelve señales de una lista, en orden.
type fakeObserver struct {
	signals []Signals
	i       int
}

func (f *fakeObserver) Observe(ctx context.Context) (Signals, error) {
	if f.i >= len(f.signals) {
		return Signals{Valid: false}, nil
	}
	s := f.signals[f.i]
	f.i++
	return s, nil
}

// actuador falso in-memory: mantiene una capacidad y cuenta llamadas.
type fakeActuator struct {
	capacity  int
	upCalls   int
	downCalls int
}

func (f *fakeActuator) CurrentCapacity(ctx context.Context) (int, error) { return f.capacity, nil }
func (f *fakeActuator) ScaleUp(ctx context.Context) error {
	f.upCalls++
	f.capacity++
	return nil
}
func (f *fakeActuator) ScaleDown(ctx context.Context) error {
	f.downCalls++
	f.capacity--
	return nil
}

// el loop debe pedir un scale up cuando hay estres sostenido.
func TestLoopScalesUpOnSustainedStress(t *testing.T) {
	obs := &fakeObserver{signals: []Signals{
		{CPUUtilization: 60, P95LatencyMillis: 1500, RequestsPerTarget: 12, Valid: true},
		{CPUUtilization: 60, P95LatencyMillis: 1500, RequestsPerTarget: 12, Valid: true},
	}}
	act := &fakeActuator{capacity: 1}
	loop := NewLoop(obs, act, DefaultPolicyConfig(), time.Minute)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	loop.Tick(context.Background(), now)                       // ciclo 1: aun sin confirmar
	r := loop.Tick(context.Background(), now.Add(time.Minute)) // ciclo 2: debe subir

	if r.Decision != IncreaseCapacity {
		t.Fatalf("esperaba INCREASE_CAPACITY, obtuvo %s", r.Decision)
	}
	if act.upCalls != 1 {
		t.Errorf("esperaba 1 llamada a ScaleUp, obtuvo %d", act.upCalls)
	}
	if act.capacity != 2 {
		t.Errorf("esperaba capacidad 2 tras subir, obtuvo %d", act.capacity)
	}
}

// si el observador no trae datos, el loop mantiene capacidad (guard).
func TestLoopMaintainsOnNoData(t *testing.T) {
	obs := &fakeObserver{signals: nil} // siempre invalido
	act := &fakeActuator{capacity: 3}
	loop := NewLoop(obs, act, DefaultPolicyConfig(), time.Minute)

	r := loop.Tick(context.Background(), time.Now())
	if r.Decision != MaintainCapacity {
		t.Errorf("esperaba MAINTAIN_CAPACITY, obtuvo %s", r.Decision)
	}
	if act.upCalls != 0 || act.downCalls != 0 {
		t.Errorf("no debio actuar sobre la infra, up=%d down=%d", act.upCalls, act.downCalls)
	}
}

// la capacidad real de AWS sobreescribe el estado interno cada ciclo (opcion A).
func TestLoopSyncsCapacityFromActuator(t *testing.T) {
	obs := &fakeObserver{signals: nil}
	act := &fakeActuator{capacity: 4} // AWS dice 4
	loop := NewLoop(obs, act, DefaultPolicyConfig(), time.Minute)
	loop.state.CurrentCapacity = 1 // el estado interno esta desincronizado

	loop.Tick(context.Background(), time.Now())
	if loop.state.CurrentCapacity != 4 {
		t.Errorf("el loop debio sincronizar la capacidad a 4, quedo en %d", loop.state.CurrentCapacity)
	}
}
