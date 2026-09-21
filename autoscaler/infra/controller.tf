# ============================================================
# Controller host (proceso Go de auto-escalado)
# ============================================================
# Vive en una subred privada: sin IP publica, sale a internet (APIs de AWS)
# via NAT, y solo es accesible por SSH saltando desde el bastion.
#
# Sin user_data a proposito: el despliegue del binario Go se hace aparte
# (cross-compilar con GOOS=linux GOARCH=amd64, copiar por scp via ProxyJump
# el bastion, y configurar como servicio systemd). Asi Terraform no necesita
# saber como se construye/versiona el binario: infraestructura y despliegue
# quedan como responsabilidades separadas.

resource "aws_instance" "controller" {
  ami           = data.aws_ssm_parameter.ubuntu.value
  instance_type = var.controller_instance_type
  key_name      = var.key_name

  subnet_id              = aws_subnet.private_a.id
  vpc_security_group_ids = [aws_security_group.controller.id]
  iam_instance_profile   = var.instance_profile_name

  # Sin associate_public_ip_address: la subred privada no asigna IP publica
  # (map_public_ip_on_launch = false), no hay que declarar nada en contra.

  tags = {
    Name = "autoscaler-controller"
  }
}
