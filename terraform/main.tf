terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  # Backend S3 opcional — descomente após criar o bucket manualmente no AWS Academy
  # backend "s3" {
  #   bucket = "lead-gen-tfstate-CONTA_ID"
  #   key    = "prod/terraform.tfstate"
  #   region = "us-east-1"
  # }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Projeto      = "lead-gen-motor"
      Ambiente     = var.ambiente
      Gerenciado   = "terraform"
      Proprietario = "demarchi-mei"
      CustoAlvo    = "aws-academy-30usd"
    }
  }
}
