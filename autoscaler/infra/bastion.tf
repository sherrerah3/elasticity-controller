# ============================================================
# Bastion host
# ============================================================
# La AMI (data.aws_ssm_parameter.ubuntu) esta en data.tf, compartida con el controller.

resource "aws_instance" "bastion" {
  ami           = data.aws_ssm_parameter.ubuntu.value
  instance_type = var.bastion_instance_type
  key_name      = var.key_name

  subnet_id                   = aws_subnet.public_a.id
  vpc_security_group_ids      = [aws_security_group.bastion.id]
  associate_public_ip_address = true

  tags = {
    Name = "autoscaler-bastion"
  }
}
