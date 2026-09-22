output "alb_dns_name" {
  description = "DNS name of the Application Load Balancer"
  value       = aws_lb.app.dns_name
}

output "target_group_arn" {
  description = "ARN of the application target group (lo usa el controller para RegisterTargets)"
  value       = aws_lb_target_group.app.arn
}

output "alb_security_group_id" {
  description = "Security group ID of the ALB"
  value       = aws_security_group.alb.id
}

output "private_subnet_ids" {
  description = "IDs de las subredes privadas donde el controller lanza las instancias EC2"
  value       = [aws_subnet.private_a.id, aws_subnet.private_b.id]
}

output "app_security_group_id" {
  description = "Security group ID de las instancias de la app (lo necesita el controller al hacer RunInstances)"
  value       = aws_security_group.app.id
}

output "bastion_public_ip" {
  description = "IP publica del bastion para acceso SSH"
  value       = aws_instance.bastion.public_ip
}

output "controller_private_ip" {
  description = "IP privada del controller (para SSH/scp saltando por el bastion con ProxyJump)"
  value       = aws_instance.controller.private_ip
}

output "app_seed_instance_id" {
  description = "ID de la instancia semilla de la app (arranque en frio del pool gestionado)"
  value       = aws_instance.app_seed.id
}
