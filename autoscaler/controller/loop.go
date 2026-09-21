package controller

import (
	"context"
	"log"
	"time"
)

// coordina observador, motor de decision y actuador cada Interval.
type Loop struct {
	Observer   MetricsObserver
	Actuator   Actuator
	Config     PolicyConfig
	Logger     DecisionLogger
	Interval   time.Duration
	MaxHistory int

	state  ControllerState
	health *HealthTracker
}

// crea un Loop con historial suficiente para el mayor de los confirm cycles.
func NewLoop(obs MetricsObserver, act Actuator, cfg PolicyConfig, interval time.Duration) *Loop {
	maxHistory := cfg.ScaleUpConfirmCycles
	if cfg.ScaleDownConfirmCycles > maxHistory {
		maxHistory = cfg.ScaleDownConfirmCycles
	}
	return &Loop{
		Observer:   obs,
		Actuator:   act,
		Config:     cfg,
		Logger:     NopLogger{}, // por defecto no persiste; se sustituye desde main.
		Interval:   interval,
		MaxHistory: maxHistory,
		health:     NewHealthTracker(cfg.UnhealthyReplaceCycles),
	}
}

// ejecuta el ciclo hasta que se cancela el contexto.
func (l *Loop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.Interval)
	defer ticker.Stop()

	// primer ciclo inmediato, sin esperar al primer tick.
	l.Tick(ctx, time.Now())

	for {
		select {
		case <-ctx.Done():
			log.Println("loop detenido:", ctx.Err())
			return
		case now := <-ticker.C:
			l.Tick(ctx, now)
		}
	}
}

// un ciclo completo: sincroniza capacidad real, observa, decide y actua.
func (l *Loop) Tick(ctx context.Context, now time.Time) DecisionResult {
	// opcion A: la capacidad real de AWS es la fuente de verdad.
	if capacity, err := l.Actuator.CurrentCapacity(ctx); err != nil {
		log.Println("no se pudo leer la capacidad real, se usa el estado interno:", err)
	} else {
		l.state.CurrentCapacity = capacity
	}

	// auto-sanacion: independiente de la politica de demanda, corre antes que ella.
	l.healSick(ctx, now)

	signal, err := l.Observer.Observe(ctx)
	if err != nil {
		// sin observacion, se registra una señal invalida: el guard hara MAINTAIN.
		log.Println("fallo la observacion, ciclo tratado como datos insuficientes:", err)
		signal = Signals{Timestamp: now, Valid: false}
	}
	l.state.RecordSignal(signal, l.MaxHistory)

	capacityBefore := l.state.CurrentCapacity
	result := Decide(l.state, l.Config, now)

	actionResult, actionErr := "none", error(nil)
	switch result.Decision {
	case IncreaseCapacity:
		if err := l.Actuator.ScaleUp(ctx); err != nil {
			// la accion fallo: no se aplica el estado, se reintenta el proximo ciclo.
			log.Println("scale up fallo, se reintentara:", err)
			actionResult, actionErr = "failed", err
		} else {
			l.state.Apply(result, now)
			actionResult = "ok"
		}
	case ReduceCapacity:
		if err := l.Actuator.ScaleDown(ctx); err != nil {
			log.Println("scale down fallo, se reintentara:", err)
			actionResult, actionErr = "failed", err
		} else {
			l.state.Apply(result, now)
			actionResult = "ok"
		}
	}

	l.logDecision(result, capacityBefore, actionResult, actionErr)
	return result
}

// reemplaza instancias con racha de unhealthy sostenida. Requiere que el observer
// implemente HealthReporter y el actuator Replacer; si no, no hace nada.
func (l *Loop) healSick(ctx context.Context, now time.Time) {
	reporter, ok := l.Observer.(HealthReporter)
	if !ok {
		return
	}
	replacer, ok := l.Actuator.(Replacer)
	if !ok {
		return
	}

	health, err := reporter.InstanceHealth(ctx)
	if err != nil {
		log.Println("no se pudo leer la salud por instancia:", err)
		return
	}

	for _, badID := range l.health.Update(health) {
		// orden lanzar->terminar: nunca baja de capacidad (tradeoff: puede rozar
		// el maximo por un instante, preferible a quedarse corto ante trafico).
		if err := replacer.Replace(ctx, badID); err != nil {
			log.Printf("reemplazo de %s fallo, se reintentara: %v", badID, err)
			continue
		}
		l.logReplacement(now, badID)
	}
}

// un reemplazo se registra como DOS decisiones: REDUCE + INCREASE, reason unhealthy_replacement.
func (l *Loop) logReplacement(now time.Time, badID string) {
	base := DecisionLogEntry{
		Timestamp:      now,
		Reason:         "unhealthy_replacement",
		Justification:  "instancia " + badID + " unhealthy sostenida: reemplazo",
		CapacityBefore: l.state.CurrentCapacity,
		CapacityAfter:  l.state.CurrentCapacity, // neto 0: -1 +1
		ActionResult:   "ok",
	}
	reduce := base
	reduce.Decision = ReduceCapacity
	increase := base
	increase.Decision = IncreaseCapacity

	if err := l.Logger.Log(reduce); err != nil {
		log.Println("no se pudo escribir el log de reemplazo (reduce):", err)
	}
	if err := l.Logger.Log(increase); err != nil {
		log.Println("no se pudo escribir el log de reemplazo (increase):", err)
	}
}

// escribe una linea en el log de decisiones (errores de log no detienen el loop).
func (l *Loop) logDecision(result DecisionResult, capacityBefore int, actionResult string, actionErr error) {
	entry := DecisionLogEntry{
		Timestamp:      result.Signal.Timestamp,
		Decision:       result.Decision,
		Justification:  result.Justification,
		Reason:         result.Reason,
		Signal:         result.Signal,
		CapacityBefore: capacityBefore,
		CapacityAfter:  l.state.CurrentCapacity,
		ActionResult:   actionResult,
	}
	if actionErr != nil {
		entry.ActionError = actionErr.Error()
	}
	if err := l.Logger.Log(entry); err != nil {
		log.Println("no se pudo escribir el log de decision:", err)
	}
}
