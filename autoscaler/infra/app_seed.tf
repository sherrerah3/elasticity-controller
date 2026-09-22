# Instancia semilla de la app para el arranque en frio: da al controller algo
# que observar desde el inicio. Lleva el mismo tag gestionado, asi pasa a ser
# parte del pool y el controller puede subir/bajar desde 1.

resource "aws_instance" "app_seed" {
  ami                    = var.app_ami
  instance_type          = var.app_instance_type
  subnet_id              = aws_subnet.private_a.id
  vpc_security_group_ids = [aws_security_group.app.id]
  key_name               = var.key_name
  monitoring             = true # monitoreo detallado: CPU cada 1 min (diseño 3)

  tags = {
    Name                  = "autoscaler-app"
    (var.managed_tag_key) = var.managed_tag_value
  }
}

# registro de la semilla en el target group (esta instancia la gestiona Terraform,
# a diferencia de las que lanza el controller en runtime via RegisterTargets).
resource "aws_lb_target_group_attachment" "app_seed" {
  target_group_arn = aws_lb_target_group.app.arn
  target_id        = aws_instance.app_seed.id
  port             = 8080
}
