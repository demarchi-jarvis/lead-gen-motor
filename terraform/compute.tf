# ============================================================
# AMI — Amazon Linux 2023 (mais recente, kernel otimizado)
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

  filter {
    name   = "state"
    values = ["available"]
  }
}

# ============================================================
# Par de Chaves SSH
# Gerar antes do apply: ssh-keygen -t ed25519 -f lead-gen-key -C "lead-gen-motor"
# ============================================================

resource "aws_key_pair" "lead_gen" {
  key_name   = var.chave_ssh_nome
  public_key = file("${path.module}/lead-gen-key.pub")

  tags = { Name = var.chave_ssh_nome }
}

# ============================================================
# Spot Instance Request — economia de ~70% vs On-Demand
#
# t3.medium On-Demand: ~$0.0416/hr
# t3.medium Spot:      ~$0.012-0.020/hr (dependendo da AZ/hora)
# Economia mensal estimada: ~$16 vs ~$30 → cabe no budget Academy
#
# spot_type = "persistent" → re-solicita se a instância for interrompida
# instance_interruption_behavior = "stop" → para em vez de terminar (dados preservados)
# ============================================================

resource "aws_spot_instance_request" "lead_gen" {
  ami           = data.aws_ami.amazon_linux_2023.id
  instance_type = var.instancia_tipo
  spot_price    = var.spot_preco_maximo

  # persistent: mantém o request ativo, re-solicita se interrompido
  spot_type                      = "persistent"
  instance_interruption_behavior = "stop"
  wait_for_fulfillment           = true

  subnet_id                   = aws_subnet.publica.id
  vpc_security_group_ids      = [aws_security_group.lead_gen.id]
  associate_public_ip_address = true
  iam_instance_profile        = aws_iam_instance_profile.ec2_lead_gen.name
  key_name                    = aws_key_pair.lead_gen.key_name

  user_data = base64encode(templatefile("${path.module}/scripts/userdata.sh", {
    db_senha   = var.db_senha
    app_porta  = var.app_porta
    aws_region = var.aws_region
  }))

  root_block_device {
    volume_type           = "gp3"
    volume_size           = var.volume_tamanho_gb
    iops                  = 3000  # gp3 inclui 3000 IOPS base sem custo extra
    throughput            = 125   # MiB/s base incluído
    delete_on_termination = true
    encrypted             = true
  }

  # Propaga tags para a instância criada pelo Spot request
  provisioner "local-exec" {
    command = <<-EOT
      aws ec2 create-tags \
        --resources ${self.spot_instance_id} \
        --tags \
          Key=Name,Value=lead-gen-motor \
          Key=Projeto,Value=lead-gen-motor \
          Key=Ambiente,Value=${var.ambiente} \
          Key=Gerenciado,Value=terraform \
        --region ${var.aws_region} || true
    EOT
  }

  tags = { Name = "lead-gen-spot-request" }
}

# ============================================================
# CloudWatch Log Group — 7 dias = custo mínimo
# ============================================================

resource "aws_cloudwatch_log_group" "app" {
  name              = "/lead-gen-motor/app"
  retention_in_days = var.cloudwatch_retencao_dias

  tags = { Name = "lead-gen-logs" }
}

resource "aws_cloudwatch_log_group" "sistema" {
  name              = "/lead-gen-motor/sistema"
  retention_in_days = var.cloudwatch_retencao_dias

  tags = { Name = "lead-gen-logs-sistema" }
}

# ============================================================
# CloudWatch Alarm — alerta se Spot for interrompida
# ============================================================

resource "aws_cloudwatch_metric_alarm" "spot_interrupcao" {
  alarm_name          = "lead-gen-spot-interrupcao"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "StatusCheckFailed_System"
  namespace           = "AWS/EC2"
  period              = 60
  statistic           = "Maximum"
  threshold           = 1
  alarm_description   = "Instância Spot possivelmente interrompida"
  treat_missing_data  = "notBreaching"

  dimensions = {
    InstanceId = aws_spot_instance_request.lead_gen.spot_instance_id
  }

  tags = { Name = "lead-gen-alarm-spot" }
}
