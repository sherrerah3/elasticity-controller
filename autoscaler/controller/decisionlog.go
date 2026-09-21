package controller

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// una linea del log de decisiones (formato JSON Lines, una por ciclo/accion).
type DecisionLogEntry struct {
	Timestamp        time.Time `json:"timestamp"`
	Decision         Decision  `json:"decision"`
	Justification    string    `json:"justification"`
	Reason           string    `json:"reason"`
	Signal           Signals   `json:"signal"`
	CapacityBefore   int       `json:"capacity_before"`
	CapacityAfter    int       `json:"capacity_after"`
	ActionResult     string    `json:"action_result"`               // ok | failed | none
	ActionError      string    `json:"action_error,omitempty"`      // detalle si failed
	ProvisioningSecs float64   `json:"provisioning_secs,omitempty"` // t2-t0, cuando aplique (Task 5)
}

// destino del log de decisiones.
type DecisionLogger interface {
	Log(entry DecisionLogEntry) error
}

// escribe cada decision como una linea JSON en un io.Writer (thread-safe).
type JSONLinesLogger struct {
	mu  sync.Mutex
	w   io.Writer
	enc *json.Encoder
}

func NewJSONLinesLogger(w io.Writer) *JSONLinesLogger {
	return &JSONLinesLogger{w: w, enc: json.NewEncoder(w)}
}

// abre (o crea) un archivo en modo append para el log.
func OpenDecisionLogFile(path string) (*JSONLinesLogger, *os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}
	return NewJSONLinesLogger(f), f, nil
}

func (l *JSONLinesLogger) Log(entry DecisionLogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enc.Encode(entry) // Encode agrega el salto de linea -> JSON Lines
}

// logger nulo para tests o cuando no se quiere persistir.
type NopLogger struct{}

func (NopLogger) Log(DecisionLogEntry) error { return nil }

// escribe mediciones de aprovisionamiento como JSON Lines (implementa ProvisioningSink).
type JSONLinesProvisioning struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func NewJSONLinesProvisioning(w io.Writer) *JSONLinesProvisioning {
	return &JSONLinesProvisioning{enc: json.NewEncoder(w)}
}

func OpenProvisioningFile(path string) (*JSONLinesProvisioning, *os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}
	return NewJSONLinesProvisioning(f), f, nil
}

func (l *JSONLinesProvisioning) Record(r ProvisioningRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enc.Encode(r)
}
