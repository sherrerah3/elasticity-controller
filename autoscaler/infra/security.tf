# ============================================================
# Security Groups
# ============================================================

# ------------------------------------------------------------
# Bastion
# ------------------------------------------------------------
resource "aws_security_group" "bastion" {
  name        = "autoscaler-sg-bastion"
  description = "SSH access to bastion host"
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "autoscaler-sg-bastion"
  }
}

resource "aws_vpc_security_group_ingress_rule" "bastion_ssh" {
  security_group_id = aws_security_group.bastion.id

  description = "SSH from administrator public IP"

  ip_protocol = "tcp"
  from_port   = 22
  to_port     = 22
  cidr_ipv4   = "${var.my_ip}/32"
}

resource "aws_vpc_security_group_egress_rule" "bastion_all_out" {
  security_group_id = aws_security_group.bastion.id

  description = "Bastion outbound traffic"

  ip_protocol = "-1"
  cidr_ipv4   = "0.0.0.0/0"
}


# ------------------------------------------------------------
# Application Load Balancer
# ------------------------------------------------------------
resource "aws_security_group" "alb" {
  name        = "autoscaler-sg-alb"
  description = "Security group for the Application Load Balancer"
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "autoscaler-sg-alb"
  }
}

resource "aws_vpc_security_group_ingress_rule" "alb_http" {
  security_group_id = aws_security_group.alb.id

  description = "Public HTTP access to ALB"

  ip_protocol = "tcp"
  from_port   = 80
  to_port     = 80
  cidr_ipv4   = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "alb_to_app" {
  security_group_id = aws_security_group.alb.id

  description                  = "ALB to application instances"
  referenced_security_group_id = aws_security_group.app.id

  ip_protocol = "tcp"
  from_port   = 8080
  to_port     = 8080
}


# ------------------------------------------------------------
# Application instances
# ------------------------------------------------------------
resource "aws_security_group" "app" {
  name        = "autoscaler-sg-app"
  description = "Security group for application EC2 instances"
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "autoscaler-sg-app"
  }
}

resource "aws_vpc_security_group_ingress_rule" "app_from_alb" {
  security_group_id = aws_security_group.app.id

  description                  = "Application traffic from ALB"
  referenced_security_group_id = aws_security_group.alb.id

  ip_protocol = "tcp"
  from_port   = 8080
  to_port     = 8080
}

resource "aws_vpc_security_group_ingress_rule" "app_ssh_from_bastion" {
  security_group_id = aws_security_group.app.id

  description                  = "SSH administration through bastion"
  referenced_security_group_id = aws_security_group.bastion.id

  ip_protocol = "tcp"
  from_port   = 22
  to_port     = 22
}

resource "aws_vpc_security_group_egress_rule" "app_all_out" {
  security_group_id = aws_security_group.app.id

  description = "Application outbound traffic through NAT"

  ip_protocol = "-1"
  cidr_ipv4   = "0.0.0.0/0"
}


# ------------------------------------------------------------
# Controller
# ------------------------------------------------------------
resource "aws_security_group" "controller" {
  name        = "autoscaler-sg-controller"
  description = "Security group for the Go autoscaling controller"
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "autoscaler-sg-controller"
  }
}

resource "aws_vpc_security_group_ingress_rule" "controller_ssh_from_bastion" {
  security_group_id = aws_security_group.controller.id

  description                  = "SSH administration through bastion (logs, JSON de decisiones, debug)"
  referenced_security_group_id = aws_security_group.bastion.id

  ip_protocol = "tcp"
  from_port   = 22
  to_port     = 22
}

# Egress "todo" en vez de solo 443, de forma deliberada:
# definir cualquier regla de egress explicita elimina el allow-all por defecto
# de AWS, lo que rompe la resolucion DNS (53/UDP-TCP) que el SDK de AWS necesita
# para resolver los endpoints (ec2/elb/cloudwatch...) antes de abrir el HTTPS.
# En vez de enumerar 443 + 53, relajamos el egress a todo, igual que app y bastion.
# El minimo privilegio que realmente importa para el controller se aplica a nivel
# de IAM (que acciones puede ejecutar), no de puertos de salida de red.
resource "aws_vpc_security_group_egress_rule" "controller_all_out" {
  security_group_id = aws_security_group.controller.id

  description = "Controller outbound traffic through NAT (AWS APIs + DNS)"

  ip_protocol = "-1"
  cidr_ipv4   = "0.0.0.0/0"
}