# El controlador (Go)

Código del controlador de auto-escalado. Este documento explica su arquitectura,
cómo está organizado el código, y cómo compilarlo y probarlo.

## Idea central: separar el "cerebro" de las "manos"

El código se divide en dos partes que no se mezclan:

- **`controller/` — el cerebro.** Toda la lógica de decisión. No importa el SDK
  de AWS: solo trabaja con números y reglas. Esto permite probarlo sin conectarse
  a AWS ni gastar dinero.
- **`awsx/` — las manos.** La capa que habla con AWS de verdad (leer métricas,
  lanzar y terminar instancias). Implementa las interfaces que define el cerebro.

El cerebro se comunica con las manos a través de "puertos" (interfaces en
`controller/ports.go`). Esto es inyección de dependencias: el loop recibe un
observador y un actuador que cumplen un contrato, sin saber si son reales (AWS)
o falsos (mocks de test).

## Paquetes y archivos

### `controller/` (motor de decisión, sin AWS)

| Archivo | Qué hace |
|---|---|
| `types.go` | Estructuras base: `Signals` (la foto del sistema), `Decision`, `ControllerState`. |
| `policy.go` | La política: umbrales, cooldowns, y `Decide()` (el corazón). |
| `ports.go` | Las interfaces (contratos) entre el cerebro y las manos. |
| `loop.go` | El ciclo cada 60s que coordina observar → decidir → actuar → registrar. |
| `health.go` | Detección de instancias enfermas para la auto-sanación. |
| `decisionlog.go` | Escritura de los registros a archivos JSON Lines. |
| `provisioning.go` | Estructura de la medición del tiempo de arranque (t0/t1/t2). |

### `awsx/` (capa AWS)

| Archivo | Qué hace |
|---|---|
| `clients.go` | Crea los clientes de AWS (CloudWatch, EC2, ELB) con reintentos. |
| `observer.go` | Lee métricas y salud → arma un `Signals`. |
| `metrics.go` | Detalle de cómo se piden las métricas a CloudWatch. |
| `actuator.go` | Lanza/termina instancias, registra en el ALB, mide t0/t1/t2. |

### `cmd/` (comandos auxiliares)

- `cmd/observe/` — programa de solo-lectura para validar la observación contra
  AWS antes de arrancar el controlador (útil para comprobar permisos).
- `cmd/sensitivity/` — experimento que calibra los ciclos de confirmación para
  bajar de capacidad.

## La política de decisión (resumen)

Cada ciclo produce una de tres decisiones: `MAINTAIN_CAPACITY`,
`INCREASE_CAPACITY` o `REDUCE_CAPACITY`.

- **Subir** si `p95 > 1000ms` **O** (`cpu > 45%` **Y** `requests > 8/s`).
  La latencia es la señal principal; CPU y requests solo disparan juntas.
- **Bajar** si `cpu < 15%` **Y** `p95 < 200ms` (ambas cómodas; bajar es más
  riesgoso, por eso es más exigente).
- Se exige que la condición se sostenga varios ciclos (2 para subir, 3 para bajar)
  y se respetan cooldowns tras cada acción, para evitar oscilaciones.
- Ante datos faltantes o inválidos, no se actúa (se mantiene la capacidad).

## Cómo compilar y ejecutar

Compilar el binario para Linux (destino EC2):

```bash
cd autoscaler
GOOS=linux GOARCH=amd64 go build -o autoscaler-controller .
```

Probar localmente sin AWS (modo simulado):

```bash
go run . -dry-run -interval=5s
```

## Cómo correr los tests

Los tests son de bajo nivel y no necesitan AWS (usan dobles/mocks):

```bash
cd autoscaler
go test ./...
```

**Sobre la organización de los tests:** siguen la convención de Go, donde cada
archivo `*_test.go` vive junto al código que prueba, dentro de su mismo paquete.
Esto es obligatorio en Go para que los tests puedan verificar la lógica interna
(funciones no exportadas). Los tests están en:

- `controller/policy_test.go`, `scenarios_test.go` — la política de decisión.
- `controller/loop_test.go` — el ciclo de control (con observador/actuador falsos).
- `controller/health_test.go`, `heal_test.go` — la auto-sanación.
- `controller/decisionlog_test.go` — el logging.
- `awsx/observer_test.go`, `actuator_test.go` — la capa AWS (con el SDK mockeado).

## Configuración

El controlador se configura por banderas de línea de comandos (región, ARN del
target group, subred, AMI, etc.). Ver todas con `go run . -h`. En el despliegue
real se pasan vía el servicio systemd (ver `deploy/README.md`).
