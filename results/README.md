# Resultados y evidencia

Logs capturados de ejecuciones reales del controlador contra AWS. Demuestran el
mecanismo de logging de decisiones y el comportamiento del sistema end-to-end.

## Archivos

### `decisions.jsonl`

El log de decisiones del controlador: una línea JSON por ciclo (cada 60s). Cada
línea registra la decisión tomada, su justificación, las señales que la
motivaron, la capacidad antes/después y el resultado de la acción.

Formato de cada línea:
```json
{
  "timestamp": "...",
  "decision": "INCREASE_CAPACITY",
  "justification": "umbral de estres sostenido por 2 ciclos consecutivos",
  "reason": "p95_latency_and_load",
  "signal": { "cpu_utilization": 98.8, "p95_latency_millis": 4614, "requests_per_target": 11.8, ... },
  "capacity_before": 1,
  "capacity_after": 2,
  "action_result": "ok"
}
```

Esta muestra contiene un ciclo de vida completo:
- **Escalado por demanda:** `INCREASE_CAPACITY` con razón `p95_latency_and_load`
  cuando la carga (k6) saturó una instancia (CPU ~98%, P95 ~4600ms).
- **Reducción:** `REDUCE_CAPACITY` con razón `low_cpu_and_latency` al bajar la
  carga y cumplirse 3 ciclos cómodos.
- **Auto-sanación:** un par `REDUCE_CAPACITY` + `INCREASE_CAPACITY` con razón
  `unhealthy_replacement`, tras matar la app en una instancia y sostenerse 3
  ciclos unhealthy.
- **Datos insuficientes:** `MAINTAIN_CAPACITY` con razón `insufficient_data`
  cuando faltan métricas frescas (p. ej. sin tráfico no hay latencia).

### `provisioning.jsonl`

Medición del tiempo de aprovisionamiento de cada instancia nueva lanzada, con
tres marcas de tiempo:
- **t0:** el controlador solicita crear la instancia (`RunInstances`).
- **t1:** EC2 reporta la instancia en estado `running`.
- **t2:** el ALB reporta la instancia `healthy` (empieza a recibir tráfico).

Y las diferencias: `aws_overhead_secs` (t1-t0), `app_startup_secs` (t2-t1) y
`total_prov_secs` (t2-t0).

En las mediciones capturadas, el tiempo total observado por el controlador es de
~118 segundos, repartido casi por igual entre el arranque de la máquina en AWS y
el arranque de la app.

Nota: por diseño, la medición tiene una resolución de un ciclo (60s), ya que el
controlador observa el progreso una vez por tick. Es el "tiempo de
aprovisionamiento observado por el controlador", no el instante físico exacto.

## Cómo analizarlos

```bash
# ver las decisiones que no fueron "mantener"
grep -v MAINTAIN_CAPACITY decisions.jsonl

# tiempos de aprovisionamiento
cat provisioning.jsonl
```
