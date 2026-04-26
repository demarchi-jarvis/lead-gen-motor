output "instancia_ip_publico" {
  description = "IP público da instância EC2 Spot"
  value       = aws_spot_instance_request.lead_gen.public_ip
}

output "instancia_id" {
  description = "ID da instância EC2 criada pelo Spot request"
  value       = aws_spot_instance_request.lead_gen.spot_instance_id
}

output "spot_request_id" {
  description = "ID do Spot Instance Request (para gerenciar o request)"
  value       = aws_spot_instance_request.lead_gen.id
}

output "vpc_id" {
  description = "ID da VPC principal"
  value       = aws_vpc.principal.id
}

output "subnet_publica_id" {
  description = "ID da Subnet Pública"
  value       = aws_subnet.publica.id
}

output "security_group_id" {
  description = "ID do Security Group da aplicação"
  value       = aws_security_group.lead_gen.id
}

output "iam_role_arn" {
  description = "ARN da IAM Role EC2"
  value       = aws_iam_role.ec2_lead_gen.arn
}

output "cloudwatch_log_group" {
  description = "Nome do Log Group da aplicação"
  value       = aws_cloudwatch_log_group.app.name
}

output "comando_ssh" {
  description = "Comando SSH para acessar a instância"
  value       = "ssh -i lead-gen-key ec2-user@${aws_spot_instance_request.lead_gen.public_ip}"
}

output "url_api" {
  description = "URL base da API REST"
  value       = "http://${aws_spot_instance_request.lead_gen.public_ip}:8080/api/v1"
}

output "url_health" {
  description = "Endpoint de health check"
  value       = "http://${aws_spot_instance_request.lead_gen.public_ip}:8080/api/v1/health"
}

output "custo_estimado_mensal" {
  description = "Estimativa de custo (Spot t3.medium ~$0.016/hr = ~$11.52/mês)"
  value       = "Spot t3.medium ≈ $11-15/mês | EBS 20GB gp3 ≈ $1.60/mês | Total ≈ $13-17/mês"
}
