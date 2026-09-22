package controller

// estado de un target en el ALB (espejo de los estados de DescribeTargetHealth).
type TargetState string

const (
	TargetInitial   TargetState = "initial"   // arrancando, aun sin veredicto: NO contar
	TargetHealthy   TargetState = "healthy"   // sana
	TargetUnhealthy TargetState = "unhealthy" // enferma genuina: SI contar
	TargetDraining  TargetState = "draining"  // en retiro: ignorar
	TargetUnused    TargetState = "unused"    // desregistrada: ignorar
)

// cuenta rachas de unhealthy genuinos por instancia (initial/draining/unused no cuentan).
type HealthTracker struct {
	threshold int            // N chequeos unhealthy seguidos para disparar reemplazo
	streak    map[string]int // instanceID -> unhealthy consecutivos
}

func NewHealthTracker(threshold int) *HealthTracker {
	return &HealthTracker{threshold: threshold, streak: map[string]int{}}
}

// actualiza los contadores con el estado de este ciclo y devuelve las instancias
// que alcanzaron el umbral de unhealthy sostenido (candidatas a reemplazo).
func (h *HealthTracker) Update(states map[string]TargetState) []string {
	var toReplace []string

	for id, state := range states {
		switch state {
		case TargetUnhealthy:
			h.streak[id]++
			if h.streak[id] >= h.threshold {
				toReplace = append(toReplace, id)
				h.streak[id] = 0
			}
		case TargetHealthy:
			h.streak[id] = 0
		default:
			// initial/draining/unused: no es falla genuina, no acumula racha.
			h.streak[id] = 0
		}
	}

	// olvidar instancias que ya no aparecen en el target group.
	for id := range h.streak {
		if _, ok := states[id]; !ok {
			delete(h.streak, id)
		}
	}
	return toReplace
}
