#!/bin/bash
# ============================================================
# deploy.sh — Sobe toda a infraestrutura e aplicação
#
# O QUE FAZ:
#   1. Gera par de chaves SSH (se não existir)
#   2. Terraform init + plan + apply (EC2 Spot + VPC)
#   3. Aguarda instância ficar pronta
#   4. Copia arquivos da aplicação via SCP
#   5. Inicia a aplicação via Docker Compose
#
# USO:
#   chmod +x scripts/deploy.sh
#   ./scripts/deploy.sh
#   ./scripts/deploy.sh --auto-approve   # pula confirmação do terraform
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RAIZ="$(cd "$SCRIPT_DIR/.." && pwd)"
TF_DIR="$RAIZ/terraform"
APP_DIR="$RAIZ/app"

# Cores para output
VERDE='\033[0;32m'
AMARELO='\033[1;33m'
VERMELHO='\033[0;31m'
AZUL='\033[0;34m'
RESET='\033[0m'

log_info()    { echo -e "${AZUL}[INFO]${RESET} $*"; }
log_sucesso() { echo -e "${VERDE}[OK]${RESET} $*"; }
log_aviso()   { echo -e "${AMARELO}[AVISO]${RESET} $*"; }
log_erro()    { echo -e "${VERMELHO}[ERRO]${RESET} $*" >&2; }

AUTO_APPROVE=""
if [[ "${1:-}" == "--auto-approve" ]]; then
  AUTO_APPROVE="-auto-approve"
fi

# ============================================================
# 1. Pré-requisitos
# ============================================================
log_info "Verificando pré-requisitos..."

for cmd in terraform aws docker ssh scp; do
  if ! command -v "$cmd" &> /dev/null; then
    log_erro "Comando '$cmd' não encontrado. Instale e tente novamente."
    exit 1
  fi
done

# Verifica credenciais AWS
if ! aws sts get-caller-identity &>/dev/null; then
  log_erro "Credenciais AWS não configuradas ou expiradas."
  log_erro "No AWS Academy: copie as credenciais do 'AWS Details' e exporte:"
  log_erro "  export AWS_ACCESS_KEY_ID=..."
  log_erro "  export AWS_SECRET_ACCESS_KEY=..."
  log_erro "  export AWS_SESSION_TOKEN=..."
  exit 1
fi

CONTA_ID=$(aws sts get-caller-identity --query Account --output text)
log_sucesso "AWS autenticado — Conta: $CONTA_ID"

# ============================================================
# 2. Par de Chaves SSH
# ============================================================
CHAVE_PRIV="$TF_DIR/lead-gen-key"
CHAVE_PUB="$TF_DIR/lead-gen-key.pub"

if [[ ! -f "$CHAVE_PRIV" ]]; then
  log_info "Gerando par de chaves SSH..."
  ssh-keygen -t ed25519 -f "$CHAVE_PRIV" -C "lead-gen-motor-$(date +%Y%m%d)" -N ""
  chmod 600 "$CHAVE_PRIV"
  log_sucesso "Par de chaves gerado: $CHAVE_PRIV"
else
  log_info "Par de chaves SSH já existe: $CHAVE_PRIV"
fi

# ============================================================
# 3. Terraform
# ============================================================
log_info "Inicializando Terraform..."
cd "$TF_DIR"

terraform init -upgrade

log_info "Validando configuração Terraform..."
terraform validate

log_info "Gerando plano de execução..."
terraform plan -out=tfplan

if [[ -z "$AUTO_APPROVE" ]]; then
  echo ""
  echo -e "${AMARELO}Revise o plano acima. Continuar com o apply? (s/N)${RESET}"
  read -r CONFIRMA
  if [[ "$CONFIRMA" != "s" && "$CONFIRMA" != "S" ]]; then
    log_aviso "Deploy cancelado pelo usuário."
    exit 0
  fi
fi

log_info "Aplicando infraestrutura..."
terraform apply $AUTO_APPROVE tfplan

# Captura outputs
IP_PUBLICO=$(terraform output -raw instancia_ip_publico 2>/dev/null || echo "")
INSTANCIA_ID=$(terraform output -raw instancia_id 2>/dev/null || echo "")

if [[ -z "$IP_PUBLICO" ]]; then
  log_erro "Não foi possível obter o IP público da instância."
  exit 1
fi

log_sucesso "Infraestrutura provisionada!"
log_sucesso "  Instância: $INSTANCIA_ID"
log_sucesso "  IP Público: $IP_PUBLICO"

# ============================================================
# 4. Aguarda instância ficar acessível via SSH
# ============================================================
log_info "Aguardando instância inicializar (pode levar 2-3 minutos)..."

MAX_TENTATIVAS=30
TENTATIVA=0
while ! ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 \
      -i "$CHAVE_PRIV" "ec2-user@$IP_PUBLICO" "echo 'SSH OK'" &>/dev/null; do
  TENTATIVA=$((TENTATIVA + 1))
  if [[ $TENTATIVA -ge $MAX_TENTATIVAS ]]; then
    log_erro "Timeout aguardando SSH. Verifique o Security Group e o status da instância."
    exit 1
  fi
  echo -n "."
  sleep 10
done
echo ""
log_sucesso "Instância acessível via SSH!"

# ============================================================
# 5. Aguarda Docker ficar pronto (userdata script)
# ============================================================
log_info "Aguardando Docker ser instalado pelo userdata..."
TENTATIVA=0
while ! ssh -o StrictHostKeyChecking=no -i "$CHAVE_PRIV" "ec2-user@$IP_PUBLICO" \
      "docker info &>/dev/null && echo 'DOCKER_OK'" 2>/dev/null | grep -q "DOCKER_OK"; do
  TENTATIVA=$((TENTATIVA + 1))
  if [[ $TENTATIVA -ge 18 ]]; then
    log_erro "Timeout aguardando Docker. Verifique o log: /var/log/userdata-lead-gen.log"
    exit 1
  fi
  echo -n "."
  sleep 10
done
echo ""
log_sucesso "Docker pronto!"

# ============================================================
# 6. Cria diretórios e copia arquivos para EC2
# ============================================================
log_info "Copiando arquivos da aplicação para EC2..."

SSH_OPTS="-o StrictHostKeyChecking=no -i $CHAVE_PRIV"

ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "
  mkdir -p /opt/lead-gen-motor/data/postgres
  mkdir -p /var/log/lead-gen
"

# Copia docker-compose e config
scp $SSH_OPTS "$APP_DIR/docker-compose.yml"     "ec2-user@$IP_PUBLICO:/opt/lead-gen-motor/"
scp $SSH_OPTS -r "$APP_DIR/config"              "ec2-user@$IP_PUBLICO:/opt/lead-gen-motor/"
scp $SSH_OPTS -r "$RAIZ/migrations"             "ec2-user@$IP_PUBLICO:/opt/lead-gen-motor/"

# Copia .env se existir
if [[ -f "$APP_DIR/.env" ]]; then
  scp $SSH_OPTS "$APP_DIR/.env" "ec2-user@$IP_PUBLICO:/opt/lead-gen-motor/.env"
  log_sucesso "Arquivo .env copiado"
else
  log_aviso ".env não encontrado em $APP_DIR/.env — editando template na instância"
fi

# ============================================================
# 7. Build e Start da Aplicação
# ============================================================
log_info "Construindo imagem Docker na instância (pode demorar ~3 minutos)..."

ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" << 'REMOTE_SCRIPT'
  set -euo pipefail
  cd /opt/lead-gen-motor

  # Copia código fonte se não estiver lá ainda
  # Em produção real: usar docker pull de ECR ou GitHub Actions
  echo "Iniciando build e deploy..."

  docker-compose pull chromedp postgres 2>/dev/null || true
  docker-compose up -d --build

  # Aguarda health check
  echo "Aguardando aplicação inicializar..."
  TENTATIVA=0
  while ! curl -sf http://localhost:8080/api/v1/health > /dev/null 2>&1; do
    TENTATIVA=$((TENTATIVA + 1))
    if [[ $TENTATIVA -ge 12 ]]; then
      echo "AVISO: Aplicação não respondeu ao health check. Verifique os logs."
      docker-compose logs --tail=50 app
      break
    fi
    sleep 5
    echo -n "."
  done
  echo ""
  echo "Deploy concluído!"
REMOTE_SCRIPT

log_sucesso "=============================================="
log_sucesso " DEPLOY CONCLUÍDO COM SUCESSO!"
log_sucesso "=============================================="
echo ""
echo -e "  ${AZUL}API:${RESET}    http://$IP_PUBLICO:8080/api/v1"
echo -e "  ${AZUL}Health:${RESET} http://$IP_PUBLICO:8080/api/v1/health"
echo -e "  ${AZUL}SSH:${RESET}    ssh -i $CHAVE_PRIV ec2-user@$IP_PUBLICO"
echo -e "  ${AZUL}Logs:${RESET}   ssh -i $CHAVE_PRIV ec2-user@$IP_PUBLICO 'cd /opt/lead-gen-motor && docker-compose logs -f app'"
echo ""

# Salva IP para uso posterior
echo "$IP_PUBLICO" > "$RAIZ/.ultimo-ip"
log_info "IP salvo em: $RAIZ/.ultimo-ip"
