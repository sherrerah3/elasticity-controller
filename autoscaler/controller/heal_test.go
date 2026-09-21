package controller

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// observador que ademas reporta salud por instancia (implementa HealthReporter).
type healthObserver struct {
	fakeObserver
	health map[string]TargetState
}

func (h *healthObserver) InstanceHealth(ctx context.Context) (map[string]TargetState, error) {
	return h.health, nil
}

// actuador que ademas reemplaza (implementa Replacer) y registra a quien reemplazo.
type replacingActuator struct {
	fakeActuator
	replaced []string
}

func (r *replacingActuator) Replace(ctx context.Context, badID string) error {
	r.replaced = append(r.replaced, badID)
	return nil
}

// tras N ciclos unhealthy, el loop reemplaza la instancia y loguea REDUCE+INCREASE.
func TestLoopReplacesUnhealthyAfterThreshold(t *testing.T) {
	var buf bytes.Buffer
	obs := &healthObserver{health: map[string]TargetState{"i-bad": TargetUnhealthy, "i-ok": TargetHealthy}}
	act := &replacingActuator{fakeActuator: fakeActuator{capacity: 2}}

	cfg := DefaultPolicyConfig()
	cfg.UnhealthyReplaceCycles = 3
	loop := NewLoop(obs, act, cfg, time.Minute)
	loop.Logger = NewJSONLinesLogger(&buf)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	loop.Tick(context.Background(), now)                    // 1 unhealthy
	loop.Tick(context.Background(), now.Add(1*time.Minute)) // 2
	if len(act.replaced) != 0 {
		t.Fatalf("no debia reemplazar antes del umbral, replaced=%v", act.replaced)
	}
	loop.Tick(context.Background(), now.Add(2*time.Minute)) // 3 -> reemplaza

	if len(act.replaced) != 1 || act.replaced[0] != "i-bad" {
		t.Fatalf("esperaba reemplazar i-bad, obtuvo %v", act.replaced)
	}

	// debe haber una linea REDUCE y una INCREASE con reason unhealthy_replacement.
	out := buf.String()
	if !strings.Contains(out, "\"decision\":\"REDUCE_CAPACITY\"") ||
		!strings.Contains(out, "\"decision\":\"INCREASE_CAPACITY\"") ||
		!strings.Contains(out, "unhealthy_replacement") {
		t.Errorf("el log de reemplazo no tiene las dos decisiones esperadas:\n%s", out)
	}
}
