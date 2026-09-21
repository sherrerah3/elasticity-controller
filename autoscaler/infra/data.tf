# AMI de Ubuntu 26.04 LTS (resolute) resuelta via SSM Parameter Store de
# Canonical: es el metodo recomendado por Canonical/AWS y evita hardcodear un
# ID de AMI que cambia por region y por cada imagen nueva publicada.
# Compartida por bastion y controller para que ambos usen exactamente la misma
# imagen (una sola fuente de verdad, sin riesgo de que se desincronicen).
data "aws_ssm_parameter" "ubuntu" {
  name = "/aws/service/canonical/ubuntu/server/resolute/stable/current/amd64/hvm/ebs-gp3/ami-id"
}
