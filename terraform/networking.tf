# ============================================================
# VPC Principal — Single VPC, low cost, sem NAT Gateway
# NAT Gateway custaria ~$32/mês sozinho — evitado com subnet pública
# ============================================================

resource "aws_vpc" "principal" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = { Name = "lead-gen-vpc" }
}

# ============================================================
# Subnet Pública — instância recebe IP público diretamente
# ============================================================

resource "aws_subnet" "publica" {
  vpc_id                  = aws_vpc.principal.id
  cidr_block              = var.subnet_publica_cidr
  availability_zone       = "${var.aws_region}a"
  map_public_ip_on_launch = true

  tags = { Name = "lead-gen-subnet-publica" }
}

# ============================================================
# Internet Gateway + Tabela de Rotas
# ============================================================

resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.principal.id
  tags   = { Name = "lead-gen-igw" }
}

resource "aws_route_table" "publica" {
  vpc_id = aws_vpc.principal.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }

  tags = { Name = "lead-gen-rt-publica" }
}

resource "aws_route_table_association" "publica" {
  subnet_id      = aws_subnet.publica.id
  route_table_id = aws_route_table.publica.id
}

# ============================================================
# Security Group — Princípio do Menor Privilégio
# Apenas: SSH (IP restrito), App Port, HTTPS saída
# ============================================================

resource "aws_security_group" "lead_gen" {
  name        = "lead-gen-sg"
  description = "Lead Gen Motor — acesso mínimo necessário"
  vpc_id      = aws_vpc.principal.id

  ingress {
    description = "SSH — somente IP autorizado"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.seu_ip_cidr]
  }

  ingress {
    description = "API Go — porta da aplicação"
    from_port   = var.app_porta
    to_port     = var.app_porta
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Necessário para: SES SMTP, APIs WhatsApp, LinkedIn, Instagram
  egress {
    description = "Saída HTTPS — APIs externas"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "Saída HTTP — atualizações, yum"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # SMTP SES (porta 587 com STARTTLS)
  egress {
    description = "SMTP SES — envio de email"
    from_port   = 587
    to_port     = 587
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Comunicação interna Docker (containers se comunicam pela rede interna)
  egress {
    description = "PostgreSQL interno Docker"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = { Name = "lead-gen-sg" }
}
