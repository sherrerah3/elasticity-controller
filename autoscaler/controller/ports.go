package controller

import "context"

// observa las señales de un ciclo (CloudWatch + ALB).
type MetricsObserver interface {
	Observe(ctx context.Context) (Signals, error)
}

// ejecuta las decisiones sobre la infraestructura (EC2 + ALB).
type Actuator interface {
	// numero real de instancias gestionadas ahora mismo (fuente de verdad).
	CurrentCapacity(ctx context.Context) (int, error)
	ScaleUp(ctx context.Context) error
	ScaleDown(ctx context.Context) error
}

// estado por instancia (id -> TargetState). Opcional: habilita la auto-sanacion.
type HealthReporter interface {
	InstanceHealth(ctx context.Context) (map[string]TargetState, error)
}

// reemplazo de una instancia concreta (lanzar nueva -> terminar la mala).
// Opcional: habilita la auto-sanacion.
type Replacer interface {
	Replace(ctx context.Context, badInstanceID string) error
}
