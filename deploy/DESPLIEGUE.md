# Guía de despliegue del controlador

Pasos para llevar el controlador (binario Go) a la instancia EC2 del controller,
correrlo como servicio, probarlo end-to-end y recoger la evidencia.

Todo se ejecuta desde tu WSL, con las credenciales de AWS Academy cargadas.
La AMI de la app ya está construida: `ami-09a801a9696d3cc3d`.

---

## 0. Levantar la infraestructura

```bash
cd autoscaler/infra
terraform apply        # escribe "yes" para confirmar
```

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

Desde tu WSL (no dentro del controller), genera carga contra el ALB.
Edita `constant-load.js` para que `BASE_URL` apunte al `alb_dns_name`, o pásalo por variable:

```bash
BASE_URL="http://<alb_dns_name>" RATE=15 k6 run constant-load.js
```

Observa en el controller (en `decisions.jsonl` o journalctl) cómo, tras un par
de ciclos de estrés confirmado, decide INCREASE_CAPACITY y lanza instancias.
Al parar k6, tras los cooldowns, debería decidir REDUCE_CAPACITY.

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

Los logs viven en el filesystem del controller. Bájalos a tu PC antes de apagar:

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
