variable "aws_region" {
  description = "Region de AWS a usar"
  type        = string
  default     = "us-east-1"
}

variable "vpc_cidr" {
  description = "Rango CIDR de la VPC"
  type        = string
  default     = "172.16.0.0/16"
}

variable "my_ip" {
  description = "Tu IP publica, para restringir SSH al bastion"
  type        = string
  # sin default a proposito: te obliga a pasarla explicitamente,
  # para no dejar el bastion abierto a todo internet por accidente
}

variable "key_name" {
  description = "Nombre del key pair EC2 para SSH al bastion. En AWS Academy es 'vockey' (labsuser.pem)."
  type        = string
  default     = "vockey"
}

variable "bastion_instance_type" {
  description = "Tipo de instancia del bastion"
  type        = string
  default     = "t3.micro"
}

variable "instance_profile_name" {
  description = "Nombre del IAM instance profile para el controller. En AWS Academy es 'LabInstanceProfile' (rol LabRole)."
  type        = string
  default     = "LabInstanceProfile"
}

variable "controller_instance_type" {
  description = "Tipo de instancia del controller (binario Go liviano: solo polling y llamadas HTTP)"
  type        = string
  default     = "t3.micro"
}

variable "app_ami" {
  description = "AMI pre-construida de la app (Ubuntu + Flask/gunicorn). La misma que usa el controller."
  type        = string
  default     = "ami-09a801a9696d3cc3d"
}

variable "app_instance_type" {
  description = "Tipo de instancia de la app"
  type        = string
  default     = "t3.micro"
}

variable "managed_tag_key" {
  description = "Clave del tag con que el controller identifica sus instancias gestionadas"
  type        = string
  default     = "autoscaler-managed"
}

variable "managed_tag_value" {
  description = "Valor del tag de instancias gestionadas"
  type        = string
  default     = "true"
}
