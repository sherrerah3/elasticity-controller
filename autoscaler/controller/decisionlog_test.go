package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// el logger escribe una linea JSON valida por decision.
func TestJSONLinesLoggerWritesParseableLine(t *testing.T) {
	var buf bytes.Buffer
	logger := NewJSONLinesLogger(&buf)

	entry := DecisionLogEntry{
		Timestamp:      time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		Decision:       IncreaseCapacity,
		Reason:         "p95_latency",
		CapacityBefore: 1,
		CapacityAfter:  2,
		ActionResult:   "ok",
	}
	if err := logger.Log(entry); err != nil {
		t.Fatalf("Log fallo: %v", err)
	}

	line := strings.TrimSpace(buf.String())
	var got DecisionLogEntry
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("la linea no es JSON valido: %v", err)
	}
	if got.Decision != IncreaseCapacity || got.CapacityAfter != 2 || got.ActionResult != "ok" {
		t.Errorf("campos incorrectos tras round-trip: %+v", got)
	}
}

// el loop escribe exactamente una linea por tick, con capacidad antes/despues.
func TestLoopLogsOneLinePerTick(t *testing.T) {
	var buf bytes.Buffer
	obs := &fakeObserver{signals: []Signals{
		{CPUUtilization: 60, P95LatencyMillis: 1500, RequestsPerTarget: 12, Valid: true},
		{CPUUtilization: 60, P95LatencyMillis: 1500, RequestsPerTarget: 12, Valid: true},
	}}
	act := &fakeActuator{capacity: 1}
	loop := NewLoop(obs, act, DefaultPolicyConfig(), time.Minute)
	loop.Logger = NewJSONLinesLogger(&buf)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	loop.Tick(context.Background(), now)
	loop.Tick(context.Background(), now.Add(time.Minute))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("esperaba 2 lineas de log, obtuvo %d", len(lines))
	}

	// la segunda decision debe ser el scale up (capacidad 1 -> 2).
	var second DecisionLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("segunda linea invalida: %v", err)
	}
	if second.Decision != IncreaseCapacity || second.CapacityBefore != 1 || second.CapacityAfter != 2 {
		t.Errorf("segunda decision incorrecta: %+v", second)
	}
	if second.ActionResult != "ok" {
		t.Errorf("esperaba action_result ok, obtuvo %s", second.ActionResult)
	}
}
