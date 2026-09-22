# Infraestructura (Terraform)

Define, como código, toda la infraestructura AWS sobre la que corre el sistema.
Región: `us-east-1`.

## Qué crea

Una VPC propia (`172.16.0.0/16`) con separación pública/privada:

- **Red** (`network.tf`): VPC, 2 subredes públicas y 2 privadas (una por zona de
  disponibilidad), Internet Gateway, y 2 NAT Gateways (uno por AZ, para alta
  disponibilidad de la salida a internet de las subredes privadas).
- **Balanceador** (`alb.tf`): Application Load Balancer en las subredes públicas,
  un Target Group (puerto 8080, health check en `/health`) y su listener HTTP.
  El target group se crea **vacío**: el controlador registra instancias en tiempo
  de ejecución.
- **Seguridad** (`security.tf`): 4 security groups (bastión, ALB, app, controller)
  con permisos mínimos y referencias entre grupos.
- **Bastión** (`bastion.tf`): instancia en subred pública, único punto de entrada
  SSH desde internet.
- **Controlador** (`controller.tf`): instancia en subred privada donde corre el
  binario Go. Solo accesible por SSH saltando desde el bastión.
- **Instancia semilla** (`app_seed.tf`): una primera instancia de la app, para
  que el controlador tenga algo que observar desde el arranque (arranque en frío).
- **Salidas** (`outputs.tf`): valores que necesita el despliegue (DNS del ALB,
  ARN del target group, IDs de subredes, IPs de bastión y controlador, etc.).

## Archivos

| Archivo | Contenido |
|---|---|
| `main.tf` | Provider AWS y tags por defecto. |
| `variables.tf` | Variables configurables (región, CIDR, AMI, key pair, etc.). |
| `network.tf` | VPC, subredes, IGW, NAT, tablas de ruteo. |
| `security.tf` | Security groups y sus reglas. |
| `alb.tf` | ALB, target group y listener. |
| `bastion.tf` | Instancia bastión. |
| `controller.tf` | Instancia del controlador. |
| `app_seed.tf` | Instancia semilla de la app. |
| `data.tf` | AMI base de Ubuntu (vía SSM Parameter Store). |
| `outputs.tf` | Valores de salida. |

## Cómo usarla

Requiere credenciales de AWS cargadas y una variable con tu IP pública (para
restringir el SSH al bastión).

```bash
cd autoscaler/infra

# tu IP publica va en terraform.tfvars (no se versiona):
echo 'my_ip = "TU.IP.PUBLICA"' > terraform.tfvars

terraform init      # una vez
terraform apply     # crea todo; escribe "yes"
terraform output    # muestra los valores que usaras en el despliegue
```

Al terminar de trabajar, **destruir para no incurrir en costos** (los NAT
Gateways cobran por hora):

```bash
terraform destroy
```

## Notas de diseño

- **2 NAT Gateways** (uno por AZ): alta disponibilidad a costa de mayor costo.
  Razonable para el reto; se documenta el tradeoff.
- **La AMI de la app** (`app_ami`) es una imagen pre-construida con la app ya
  instalada. Se construye una vez a partir de una instancia con `script.aws`
  (ver la raíz del repo). Persiste entre sesiones del Learner Lab.
- **IAM:** el controlador usa el `LabInstanceProfile` de AWS Academy (permisos
  amplios), porque el entorno no permite crear roles propios. El diseño original
  especifica un rol de mínimo privilegio, que se aplicaría en una cuenta propia.

## Seguridad

No se versionan: el estado de Terraform (`*.tfstate`, puede contener datos
sensibles), las variables locales (`terraform.tfvars`, contiene tu IP) ni la
clave privada SSH (`*.pem`). Todo está en el `.gitignore`.
