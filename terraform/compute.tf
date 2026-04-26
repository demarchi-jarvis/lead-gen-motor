# ============================================================
# AMI — Amazon Linux 2023
# ============================================================
data "aws_ami" "amazon_linux_2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-*-kernel-*-x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

# ============================================================
# Par de Chaves SSH
# ============================================================
resource "aws_key_pair" "lead_gen" {
  key_name   = var.chave_ssh_nome
  public_key = file("${path.module}/lead-gen-key.pub")
  tags       = { Name = var.chave_ssh_nome }
}

# ============================================================
# Instancia EC2 (On-Demand para compatibilidade Academy)
# ============================================================
resource "aws_instance" "lead_gen" {
  ami                         = data.aws_ami.amazon_linux_2023.id
  instance_type               = var.instancia_tipo
  subnet_id                   = aws_subnet.publica.id
  vpc_security_group_ids      = [aws_security_group.lead_gen.id]
  associate_public_ip_address = true
  iam_instance_profile        = "LabInstanceProfile"
  key_name                    = aws_key_pair.lead_gen.key_name

  user_data = base64encode(templatefile("${path.module}/scripts/userdata.sh", {
    db_senha        = var.db_senha
    app_porta       = var.app_porta
    aws_region      = var.aws_region
    COMPOSE_VERSION = "v2.21.0"
  }))

  root_block_device {
    volume_type           = "gp3"
    volume_size = 30
    iops                  = 3000
    throughput            = 125
    delete_on_termination = true
    encrypted             = true
  }

  tags = { 
    Name = "lead-gen-motor"
    Projeto = "lead-gen-motor"
  }
}

# ============================================================
# CloudWatch Log Groups
# ============================================================
resource "aws_cloudwatch_log_group" "app" {
  name              = "/lead-gen-motor/app"
  retention_in_days = var.cloudwatch_retencao_dias
  tags              = { Name = "lead-gen-logs" }
}

resource "aws_cloudwatch_log_group" "sistema" {
  name              = "/lead-gen-motor/sistema"
  retention_in_days = var.cloudwatch_retencao_dias
  tags              = { Name = "lead-gen-logs-sistema" }
}
