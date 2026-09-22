# Guía de despliegue del controlador

Pasos para llevar el controlador (binario Go) a la instancia EC2 del controller,
correrlo como servicio, probarlo end-to-end y recoger la evidencia.

Todo se ejecuta desde tu WSL, con las credenciales de AWS Academy cargadas.
La AMI de la app ya está construida: `ami-09a801a9696d3cc3d`.

> Nota: los valores concretos (IPs, ARNs) cambian en cada `terraform apply`.
> Toma los tuyos de `terraform output` y reemplaza los `<...>` de esta guía.

---

## 0. Credenciales y región

Carga las credenciales del Learner Lab en `~/.aws/credentials` (bloque de
"AWS Details" -> "AWS CLI") y fija la región para toda la sesión:

```bash
export AWS_DEFAULT_REGION=us-east-1
aws sts get-caller-identity      # confirma que las credenciales sirven
```

Luego levanta la infraestructura:

```bash
cd autoscaler/infra
terraform apply        # escribe "yes" para confirmar
```

Esto crea también una **instancia semilla** de la app, de modo que el controlador
arranca con una instancia sana que observar (no hay que hacer nada extra para el
arranque en frío).

Cuando termine, guarda los outputs (los usarás en varios pasos):

```bash
terraform output
```

Anota: `bastion_public_ip`, `controller_private_ip`, `target_group_arn`,
`private_subnet_ids`, `app_security_group_id`, `alb_dns_name`.

Las dimensiones de CloudWatch (`tg-dimension`, `lb-dimension`) se sacan de los ARN:
- `tg-dimension` = la parte del ARN del target group desde `targetgroup/...`
  Ej: si el ARN es `arn:aws:.../targetgroup/autoscaler-app-tg/abc123`,
  la dimensión es `targetgroup/autoscaler-app-tg/abc123`.
- `lb-dimension` = la parte del ARN del ALB desde `app/...`
  Ej: `app/autoscaler-alb/def456`.

Comandos para extraerlas automáticamente:
```bash
# tg-dimension
terraform output -raw target_group_arn | sed 's#.*:\(targetgroup/.*\)#\1#'
# lb-dimension (a partir del ARN del ALB)
aws elbv2 describe-load-balancers --names autoscaler-alb \
  --query 'LoadBalancers[0].LoadBalancerArn' --output text \
  | sed 's#.*:loadbalancer/\(app/.*\)#\1#'
```

---

## 1. Cross-compilar el binario

Desde la carpeta del módulo Go, genera el ejecutable para Linux x86_64:

```bash
cd autoscaler
GOOS=linux GOARCH=amd64 /usr/local/go/bin/go build -o autoscaler-controller .
```

Esto crea el archivo `autoscaler-controller` (un solo binario, sin dependencias).

---

## 1b. (Opcional pero recomendado) Validar permisos con `cmd/observe`

Antes de arrancar el controlador completo, confirma que el rol de AWS permite
leer métricas. Este comando solo lee (no actúa):

```bash
TG_ARN=$(cd infra && terraform output -raw target_group_arn)
TG_DIM=$(echo "$TG_ARN" | sed 's#.*:\(targetgroup/.*\)#\1#')
LB_DIM=$(aws elbv2 describe-load-balancers --names autoscaler-alb \
  --query 'LoadBalancers[0].LoadBalancerArn' --output text \
  | sed 's#.*:loadbalancer/\(app/.*\)#\1#')

go run ./cmd/observe -target-group-arn="$TG_ARN" -tg-dimension="$TG_DIM" -lb-dimension="$LB_DIM"
```

Si imprime las señales en JSON (aunque sea con `valid: false` por falta de
tráfico), los permisos funcionan. Si falla con `AccessDenied`, hay que resolver
permisos antes de seguir.

Nota: la latencia P95 solo aparece cuando hay tráfico reciente (el ALB solo
publica esa métrica cuando hay peticiones). En reposo es normal ver `valid: false`.

---

## 2. Configurar SSH con ProxyJump (bastión -> controller)

El controller está en una subred privada: solo se llega saltando por el bastión.
Edita `~/.ssh/config` (créalo si no existe) y agrega, reemplazando las IPs:

```
Host bastion
    HostName <bastion_public_ip>
    User ubuntu
    IdentityFile ~/ruta/a/labsuser.pem

Host controller
    HostName <controller_private_ip>
    User ubuntu
    IdentityFile ~/ruta/a/labsuser.pem
    ProxyJump bastion
```

Asegura permisos de la llave:
```bash
chmod 400 ~/ruta/a/labsuser.pem
```

Prueba el salto:
```bash
ssh controller        # deberías entrar directo al controller
```

---

## 3. Copiar el binario y el servicio al controller

Con la config SSH lista, `scp` usa el mismo salto automáticamente:

```bash
scp autoscaler-controller controller:/home/ubuntu/
scp ../deploy/autoscaler-controller.service controller:/home/ubuntu/
```

---

## 4. Instalar y arrancar el servicio (dentro del controller)

Entra al controller y edita el `.service` con los valores reales:

```bash
ssh controller
```

Ya dentro, reemplaza los `<...>` del archivo con tus outputs. Ejemplo con nano:
```bash
nano /home/ubuntu/autoscaler-controller.service
```
Rellena: `<AMI_APP>` = ami-09a801a9696d3cc3d, `<TARGET_GROUP_ARN>`, `<TG_DIMENSION>`,
`<LB_DIMENSION>`, `<PRIVATE_SUBNET_ID>` (una de las privadas), `<APP_SG_ID>`.

Instala y arranca:
```bash
sudo cp /home/ubuntu/autoscaler-controller.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now autoscaler-controller
```

Verifica que corre y mira sus logs:
```bash
sudo systemctl status autoscaler-controller
journalctl -u autoscaler-controller -f        # logs en vivo (Ctrl+C para salir)
```

Y revisa que empiece a escribir decisiones:
```bash
tail -f /home/ubuntu/decisions.jsonl
```

---

## 5. Prueba end-to-end de DEMANDA (con k6)

Desde tu WSL (no dentro del controller), genera carga contra el **DNS del ALB en
el puerto 80** (el ALB reenvía al 8080 interno). Los parámetros van por `-e`:

```bash
k6 run -e BASE_URL="http://<alb_dns_name>" -e RATE=25 -e ITERATIONS=60000 -e DURATION=5m constant-load.js
```

- `RATE` = peticiones por segundo. `ITERATIONS` = trabajo (CPU) por petición.
- Calibración: `RATE=25 ITERATIONS=60000` sube la carga lo suficiente para
  disparar el escalado sin colapsar la app (peticiones muy pesadas saturan tanto
  que matan el health check antes de escalar).

Observa en el controller (`tail -f /home/ubuntu/decisions.jsonl`) cómo, tras 2
ciclos de estrés confirmado, decide `INCREASE_CAPACITY` y lanza una instancia.
Al parar k6, tras los cooldowns y 3 ciclos cómodos, decide `REDUCE_CAPACITY`.

---

## 6. Prueba end-to-end de AUTO-SANACIÓN

Simula una instancia caída. Entra a una instancia de la app (saltando por el
bastión) y detén gunicorn:

```bash
# desde el bastión, ssh a la IP privada de una instancia de la app
sudo systemctl stop autoscaler-app
```

En unos ciclos, el ALB la marcará unhealthy. Tras 3 ciclos unhealthy sostenidos,
el controller debe reemplazarla: verás en el log dos entradas seguidas con
reason "unhealthy_replacement" (un REDUCE y un INCREASE).

---

## 7. Recoger la evidencia (antes de destruir)

Los logs viven en el filesystem del controller. Para bajarlos a tu PC antes de apagar:

```bash
scp controller:/home/ubuntu/decisions.jsonl ./decisions.jsonl
scp controller:/home/ubuntu/provisioning.jsonl ./provisioning.jsonl
```

Analiza el aprovisionamiento (tiempos reales t0/t1/t2):
```bash
cat provisioning.jsonl | jq '{id: .instance_id, aws: .aws_overhead_secs, app: .app_startup_secs, total: .total_prov_secs}'
```

El `total_prov_secs` real sirve para calibrar el cooldown de subida.

---

## 8. Apagar para no gastar (fin de sesión)

Los 2 NAT Gateways cuestan por hora. Al terminar:

```bash
cd autoscaler/infra
terraform destroy       # escribe "yes"
```

La AMI (`ami-09a801a9696d3cc3d`) persiste entre sesiones, no se borra con destroy.
