variable "aws_region" {
  description = "Região AWS para implantação (AWS Academy usa us-east-1 por padrão)"
  type        = string
  default     = "us-east-1"
}

variable "ambiente" {
  description = "Ambiente de implantação"
  type        = string
  default     = "prod"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.ambiente)
    error_message = "Ambiente deve ser: dev, staging ou prod."
  }
}

variable "vpc_cidr" {
  description = "CIDR block principal da VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "subnet_publica_cidr" {
  description = "CIDR da subnet pública (instância exposta à internet)"
  type        = string
  default     = "10.0.1.0/24"
}

variable "instancia_tipo" {
  description = "Tipo EC2 — t3.medium: 2vCPU/4GB, suficiente para Go + PostgreSQL"
  type        = string
  default     = "t3.medium"
}

variable "spot_preco_maximo" {
  description = "Preço máximo/hora Spot em USD (on-demand t3.medium ~$0.0416 → Spot ~$0.012)"
  type        = string
  default     = "0.05"
}

variable "chave_ssh_nome" {
  description = "Nome do par de chaves SSH (gerar: ssh-keygen -t ed25519 -f lead-gen-key)"
  type        = string
  default     = "lead-gen-key"
}

variable "app_porta" {
  description = "Porta HTTP da aplicação Go"
  type        = number
  default     = 8080
}

variable "seu_ip_cidr" {
  description = "SEU IP para acesso SSH (formato x.x.x.x/32) — NUNCA deixe 0.0.0.0/0 em produção"
  type        = string
  default     = "0.0.0.0/0"
}

variable "volume_tamanho_gb" {
  description = "Tamanho do volume EBS raiz em GB (gp3 — ~$0.08/GB-mês)"
  type        = number
  default     = 20
}

variable "cloudwatch_retencao_dias" {
  description = "Retenção de logs no CloudWatch (mínimo para economizar custo)"
  type        = number
  default     = 7
}

variable "db_senha" {
  description = "Senha do PostgreSQL — OBRIGATÓRIO: export TF_VAR_db_senha='SuaSenhaForte'"
  type        = string
  sensitive   = true
  # sem default — força definição explícita via TF_VAR_db_senha ou tfvars
}
