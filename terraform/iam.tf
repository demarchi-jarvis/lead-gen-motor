/*
# ============================================================
# IAM Role EC2 — Permissões mínimas necessárias
# AWS Academy tem restrições: não cria roles com nomes arbitrários
# Usar prefixo padrão para evitar bloqueio
# ============================================================

resource "aws_iam_role" "ec2_lead_gen" {
  name = "lead-gen-ec2-role"
  path = "/"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "EC2AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })

  tags = { Name = "lead-gen-ec2-role" }
}

# ============================================================
# Policy Customizada — SES + CloudWatch + SSM Parameter Store
# ============================================================

resource "aws_iam_role_policy" "lead_gen_permissoes" {
  name = "lead-gen-permissoes"
  role = aws_iam_role.ec2_lead_gen.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [

      # SES — envio de emails de prospecção
      {
        Sid    = "SESEnvioEmail"
        Effect = "Allow"
        Action = [
          "ses:SendEmail",
          "ses:SendRawEmail",
          "ses:SendTemplatedEmail",
          "ses:GetSendQuota",
          "ses:GetSendStatistics",
          "ses:ListVerifiedEmailAddresses"
        ]
        Resource = "*"
      },

      # CloudWatch Logs — observabilidade da aplicação Go
      {
        Sid    = "CloudWatchLogsAplicacao"
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:DescribeLogGroups",
          "logs:DescribeLogStreams"
        ]
        Resource = "arn:aws:logs:${var.aws_region}:*:log-group:/lead-gen-motor*"
      },

      # CloudWatch Metrics — métricas customizadas (leads processados/hora)
      {
        Sid    = "CloudWatchMetricas"
        Effect = "Allow"
        Action = [
          "cloudwatch:PutMetricData",
          "cloudwatch:GetMetricStatistics"
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "cloudwatch:namespace" = "LeadGenMotor"
          }
        }
      },

      # SSM Parameter Store — segredos em runtime (credenciais APIs)
      {
        Sid    = "SSMParameterStore"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:GetParameters",
          "ssm:GetParametersByPath"
        ]
        Resource = "arn:aws:ssm:${var.aws_region}:*:parameter/lead-gen/*"
      },

      # EC2 describe — para scripts de auto-diagnóstico
      {
        Sid    = "EC2DescribeSelf"
        Effect = "Allow"
        Action = [
          "ec2:DescribeInstances",
          "ec2:DescribeTags"
        ]
        Resource = "*"
      }
    ]
  })
}

# Instance Profile — elo entre EC2 e IAM Role
resource "aws_iam_instance_profile" "ec2_lead_gen" {
  name = "lead-gen-instance-profile"
  role = aws_iam_role.ec2_lead_gen.name

  tags = { Name = "lead-gen-instance-profile" }
}
*/
