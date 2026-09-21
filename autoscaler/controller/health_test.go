package controller

import (
	"reflect"
	"testing"
)

// N-1 ciclos unhealthy no disparan; el N-esimo si.
func TestHealthTrackerFiresAtThreshold(t *testing.T) {
	h := NewHealthTracker(3)
	sick := map[string]TargetState{"i-1": TargetUnhealthy}

	if got := h.Update(sick); len(got) != 0 {
		t.Errorf("ciclo 1: no debia reemplazar, obtuvo %v", got)
	}
	if got := h.Update(sick); len(got) != 0 {
		t.Errorf("ciclo 2: no debia reemplazar, obtuvo %v", got)
	}
	got := h.Update(sick) // ciclo 3: dispara
	if !reflect.DeepEqual(got, []string{"i-1"}) {
		t.Errorf("ciclo 3: esperaba [i-1], obtuvo %v", got)
	}
}

// una instancia que vuelve a healthy reinicia su racha.
func TestHealthTrackerResetsOnRecovery(t *testing.T) {
	h := NewHealthTracker(3)
	h.Update(map[string]TargetState{"i-1": TargetUnhealthy}) // 1
	h.Update(map[string]TargetState{"i-1": TargetUnhealthy}) // 2
	h.Update(map[string]TargetState{"i-1": TargetHealthy})   // recupera -> racha 0

	h.Update(map[string]TargetState{"i-1": TargetUnhealthy}) // 1
	if got := h.Update(map[string]TargetState{"i-1": TargetUnhealthy}); len(got) != 0 {
		t.Errorf("tras recuperar, 2 ciclos no deben disparar, obtuvo %v", got)
	}
}

// una instancia sana nunca se reemplaza.
func TestHealthTrackerHealthyNeverFires(t *testing.T) {
	h := NewHealthTracker(2)
	for i := 0; i < 5; i++ {
		if got := h.Update(map[string]TargetState{"i-1": TargetHealthy}); len(got) != 0 {
			t.Errorf("instancia sana no debe reemplazarse, obtuvo %v", got)
		}
	}
}

// una instancia en 'initial' (recien creada) NUNCA se reemplaza aunque persista.
func TestHealthTrackerInitialNeverFires(t *testing.T) {
	h := NewHealthTracker(2)
	for i := 0; i < 5; i++ {
		if got := h.Update(map[string]TargetState{"i-1": TargetInitial}); len(got) != 0 {
			t.Errorf("una instancia en initial no debe reemplazarse, obtuvo %v", got)
		}
	}
}

// draining/unused se ignoran (no disparan reemplazo).
func TestHealthTrackerDrainingUnusedIgnored(t *testing.T) {
	h := NewHealthTracker(2)
	h.Update(map[string]TargetState{"i-1": TargetDraining})
	if got := h.Update(map[string]TargetState{"i-1": TargetUnused}); len(got) != 0 {
		t.Errorf("draining/unused no deben disparar reemplazo, obtuvo %v", got)
	}
}
