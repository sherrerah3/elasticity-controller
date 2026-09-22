# Controlador de auto-escalado horizontal

Reto individual del curso **SI3016 Cloud Computing**.

- **Autor:** Samuel Herrera Hoyos
- **Universidad:** EAFIT
- **Curso:** Cloud Computing

## Qué es

Un controlador de auto-escalado horizontal que ajusta automáticamente el número
de instancias EC2 (entre 1 y 5) que sirven una aplicación web detrás de un
Application Load Balancer (ALB), en respuesta a la demanda, **sin usar Auto
Scaling Groups ni políticas de escalado administradas por AWS**. Toda la lógica
de decisión es propia.

El controlador es un proceso en Go que corre como un ciclo de control cerrado:
cada 60 segundos observa métricas (CPU, latencia P95, requests, salud de
instancias), decide (mantener, subir o bajar capacidad), actúa sobre la
infraestructura y registra la decisión.

## Estructura del repositorio

```
elasticity-controller/
├── README.md                 # este archivo (visión general)
├── app.py                    # aplicación de prueba (Flask, CPU-bound)
├── requirements.txt          # dependencias de la app
├── script.aws                # script base usado para construir la AMI de la app
├── constant-load.js          # herramienta de generación de carga (k6)
│
├── autoscaler/               # el controlador en Go
│   ├── README.md             # arquitectura del controlador y cómo correrlo
│   ├── main.go               # punto de entrada
│   ├── controller/           # motor de decisión (lógica pura, sin AWS)
│   ├── awsx/                 # capa que habla con AWS (CloudWatch, EC2, ALB)
│   ├── cmd/                  # comandos auxiliares (observe, sensitivity)
│   └── infra/                # infraestructura como código (Terraform)
│       └── README.md         # qué crea la infra y cómo aplicarla
│
├── deploy/                   # artefactos y guía de despliegue
│   └── README.md             # cómo desplegar el controlador y probarlo end-to-end
│
└── results/                  # evidencia de ejecuciones reales (logs)
    └── README.md             # qué muestran los logs capturados
```

## Componentes del entregable

| Requisito | Dónde está |
|---|---|
| Código fuente del controlador | `autoscaler/` (Go) |
| Definición de infraestructura | `autoscaler/infra/` (Terraform) |
| Instrucciones de despliegue y ejecución | `deploy/README.md` |
| Herramienta de generación de carga | `constant-load.js` (k6) |
| Mecanismo de logging de decisiones | `autoscaler/controller/decisionlog.go` + muestras en `results/` |
| Aplicación bajo prueba | `app.py` + `script.aws` |

## Cómo empezar

1. **Entender el controlador:** lee `autoscaler/README.md`.
2. **Levantar la infraestructura:** sigue `autoscaler/infra/README.md`.
3. **Desplegar y probar:** sigue `deploy/README.md`.

## Requisitos

- **Go** 1.27+ (para compilar el controlador).
- **Terraform** 1.5+ (para la infraestructura).
- **k6** (para generar carga).
- **AWS CLI** con credenciales de una cuenta (el proyecto se desarrolló en AWS
  Academy Learner Lab).

## Seguridad

Este repositorio **no contiene credenciales**. Las claves de AWS se cargan en
tiempo de ejecución (archivo `~/.aws/credentials`, fuera del repo) y la clave
privada SSH (`labsuser.pem`) y el estado de Terraform están excluidos vía
`.gitignore`.
