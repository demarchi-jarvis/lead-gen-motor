#!/bin/bash
# ============================================================
# deploy.sh — Lead Gen Motor: deploy completo na AWS Academy
#
# O QUE FAZ:
#   1. Verifica pré-requisitos e credenciais AWS
#   2. Garante par de chaves SSH (gera se não existir)
#   3. Remove key pair conflitante no AWS (evita erro de recriação)
#   4. Terraform init + plan + apply (EC2 On-Demand + VPC)
#   5. Aguarda instância + Docker ficarem prontos (userdata)
#   6. Copia código fonte + app completo para EC2 via rsync/scp
#   7. Builda imagem Docker na EC2 e inicia os containers
#
# USO:
#   chmod +x scripts/deploy.sh
#   ./scripts/deploy.sh                  # deploy com confirmação
#   ./scripts/deploy.sh --auto-approve   # pula confirmação terraform
#   ./scripts/deploy.sh --rebuild        # força rebuild da imagem Go
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RAIZ="$(cd "$SCRIPT_DIR/.." && pwd)"
TF_DIR="$RAIZ/terraform"
APP_DIR="$RAIZ/app"

VERDE='\033[0;32m'
AMARELO='\033[1;33m'
VERMELHO='\033[0;31m'
AZUL='\033[0;34m'
RESET='\033[0m'

ok()   { echo -e "${VERDE}[OK]${RESET} $*"; }
info() { echo -e "${AZUL}[INFO]${RESET} $*"; }
warn() { echo -e "${AMARELO}[AVISO]${RESET} $*"; }
erro() { echo -e "${VERMELHO}[ERRO]${RESET} $*" >&2; }
sep()  { echo -e "${AZUL}────────────────────────────────────────────${RESET}"; }

AUTO_APPROVE=""
REBUILD=""
for arg in "${@:-}"; do
  [[ "$arg" == "--auto-approve" ]] && AUTO_APPROVE="-auto-approve"
  [[ "$arg" == "--rebuild" ]]      && REBUILD="--build"
done

echo ""
echo -e "${AZUL}============================================================${RESET}"
echo -e "${AZUL}   Lead Gen Motor — Deploy AWS Academy${RESET}"
echo -e "${AZUL}============================================================${RESET}"
echo ""

# ============================================================
# 1. Pré-requisitos
# ============================================================
sep
info "Verificando pré-requisitos..."

for cmd in terraform aws ssh scp; do
  if ! command -v "$cmd" &>/dev/null; then
    erro "Comando '$cmd' não encontrado."
    exit 1
  fi
done

# Verifica credenciais AWS
if ! aws sts get-caller-identity &>/dev/null; then
  erro "Credenciais AWS inválidas ou expiradas."
  erro "AWS Academy: vá em 'AWS Details' > copie as credenciais > execute:"
  erro ""
  erro "  cat >> ~/.aws/credentials << 'EOF'"
  erro "  [default]"
  erro "  aws_access_key_id=ASIA..."
  erro "  aws_secret_access_key=..."
  erro "  aws_session_token=..."
  erro "  EOF"
  erro ""
  erro "Ou exporte como variáveis de ambiente:"
  erro "  export AWS_ACCESS_KEY_ID=..."
  erro "  export AWS_SECRET_ACCESS_KEY=..."
  erro "  export AWS_SESSION_TOKEN=..."
  exit 1
fi

CONTA_ID=$(aws sts get-caller-identity --query Account --output text)
ok "AWS autenticado — Conta: $CONTA_ID"

# ============================================================
# 2. Par de Chaves SSH
# ============================================================
sep
CHAVE_PRIV="$TF_DIR/lead-gen-key"
CHAVE_PUB="$TF_DIR/lead-gen-key.pub"

if [[ ! -f "$CHAVE_PRIV" ]]; then
  info "Gerando par de chaves SSH RSA..."
  ssh-keygen -t rsa -b 2048 -f "$CHAVE_PRIV" -C "lead-gen-motor-$(date +%Y%m%d)" -N ""
  chmod 600 "$CHAVE_PRIV"
  ok "Par de chaves gerado: $CHAVE_PRIV"
else
  # Garante que pub key existe (pode ter sido gerada externamente)
  if [[ ! -f "$CHAVE_PUB" ]]; then
    ssh-keygen -y -f "$CHAVE_PRIV" > "$CHAVE_PUB"
    info "Chave pública extraída da privada existente"
  fi
  ok "Par de chaves SSH: $CHAVE_PRIV"
fi
chmod 600 "$CHAVE_PRIV"

# ============================================================
# 3. Remove key pair AWS antigo se existir (evita conflito)
# ============================================================
sep
KEY_NAME=$(grep 'chave_ssh_nome' "$TF_DIR/terraform.tfvars" 2>/dev/null | grep -oP '"\K[^"]+' || echo "lead-gen-key")
info "Verificando key pair '$KEY_NAME' na AWS..."

if aws ec2 describe-key-pairs --key-names "$KEY_NAME" --region us-east-1 &>/dev/null; then
  warn "Key pair '$KEY_NAME' já existe na AWS. Removendo para atualizar com a chave atual..."
  aws ec2 delete-key-pair --key-name "$KEY_NAME" --region us-east-1
  ok "Key pair antigo removido"
  # Também remove do Terraform state para evitar conflito de import
  cd "$TF_DIR"
  terraform state rm aws_key_pair.lead_gen 2>/dev/null || true
else
  ok "Nenhum key pair conflitante encontrado"
fi

# ============================================================
# 4. Terraform
# ============================================================
sep
info "Inicializando Terraform..."
cd "$TF_DIR"

terraform init -upgrade -reconfigure 2>&1 | tail -5

info "Validando configuração..."
terraform validate

info "Gerando plano..."
terraform plan -out=tfplan

if [[ -z "$AUTO_APPROVE" ]]; then
  echo ""
  echo -e "${AMARELO}Continuar com o apply? [s/N]${RESET}"
  read -r CONFIRMA
  if [[ "$CONFIRMA" != "s" && "$CONFIRMA" != "S" ]]; then
    warn "Deploy cancelado."
    exit 0
  fi
fi

info "Aplicando infraestrutura..."
terraform apply $AUTO_APPROVE tfplan

IP_PUBLICO=$(terraform output -raw instancia_ip_publico 2>/dev/null || echo "")
if [[ -z "$IP_PUBLICO" ]]; then
  erro "Não foi possível obter o IP público da instância."
  terraform output
  exit 1
fi

ok "Infraestrutura provisionada! IP: $IP_PUBLICO"
echo "$IP_PUBLICO" > "$RAIZ/.ultimo-ip"

# ============================================================
# 5. Aguarda instância ficar acessível via SSH
# ============================================================
sep
info "Aguardando SSH ficar disponível (pode levar 2-3 min)..."

SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=8 -o BatchMode=yes -i $CHAVE_PRIV"
TENTATIVA=0
until ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "echo SSH_OK" 2>/dev/null | grep -q "SSH_OK"; do
  TENTATIVA=$((TENTATIVA + 1))
  if [[ $TENTATIVA -ge 40 ]]; then
    erro "Timeout aguardando SSH. Verifique Security Group e status da instância."
    exit 1
  fi
  echo -n "."
  sleep 8
done
echo ""
ok "SSH disponível!"

# ============================================================
# 6. Aguarda Docker (instalado pelo userdata)
# ============================================================
sep
info "Aguardando Docker ser instalado pelo userdata (~2 min)..."
TENTATIVA=0
until ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "docker info &>/dev/null && echo DOCKER_OK" 2>/dev/null | grep -q "DOCKER_OK"; do
  TENTATIVA=$((TENTATIVA + 1))
  if [[ $TENTATIVA -ge 20 ]]; then
    erro "Timeout aguardando Docker. Log do userdata:"
    ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "tail -30 /var/log/userdata-lead-gen.log" 2>/dev/null || true
    exit 1
  fi
  echo -n "."
  sleep 10
done
echo ""
ok "Docker pronto!"

# ============================================================
# 7. Prepara estrutura de diretórios na EC2
# ============================================================
sep
info "Preparando diretórios na EC2..."

ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "
  sudo mkdir -p /opt/lead-gen-motor/app
  sudo mkdir -p /opt/lead-gen-motor/migrations
  sudo mkdir -p /var/log/lead-gen
  sudo chown -R ec2-user:ec2-user /opt/lead-gen-motor /var/log/lead-gen
  chmod 755 /opt/lead-gen-motor/app
"
ok "Diretórios criados"

# ============================================================
# 8. Copia código fonte + configs para EC2
# ============================================================
sep
info "Copiando aplicação para EC2 (pode levar ~30s)..."

# Usa rsync se disponível, senão scp
if command -v rsync &>/dev/null; then
  rsync -az --progress \
    --exclude='.git' \
    --exclude='*.test' \
    --exclude='vendor/' \
    -e "ssh $SSH_OPTS" \
    "$APP_DIR/" \
    "ec2-user@$IP_PUBLICO:/opt/lead-gen-motor/app/"
else
  # Fallback: scp recursivo
  scp $SSH_OPTS -r "$APP_DIR/." "ec2-user@$IP_PUBLICO:/opt/lead-gen-motor/app/"
fi

# Copia migrations (um nível acima de app/ para manter ../migrations funcional)
scp $SSH_OPTS -r "$RAIZ/migrations/." "ec2-user@$IP_PUBLICO:/opt/lead-gen-motor/migrations/"

ok "Código fonte copiado"

# ============================================================
# 9. Configura .env na EC2 (sem credenciais AWS — usa LabInstanceProfile)
# ============================================================
sep
info "Configurando .env na EC2..."

ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "
  chmod 600 /opt/lead-gen-motor/app/.env 2>/dev/null || true

  # Remove variáveis AWS temporárias do .env (a instância usa LabInstanceProfile)
  if [[ -f /opt/lead-gen-motor/app/.env ]]; then
    sed -i '/^AWS_ACCESS_KEY_ID=/d'    /opt/lead-gen-motor/app/.env
    sed -i '/^AWS_SECRET_ACCESS_KEY=/d' /opt/lead-gen-motor/app/.env
    sed -i '/^AWS_SESSION_TOKEN=/d'    /opt/lead-gen-motor/app/.env
    echo 'AWS_REGION=us-east-1' >> /opt/lead-gen-motor/app/.env
    chmod 600 /opt/lead-gen-motor/app/.env
    echo '.env configurado'
  fi
"

# ============================================================
# 10. Build + Start da Aplicação
# ============================================================
sep
info "Gerando go.sum e construindo imagem Docker na EC2..."
info "(primeiro build demora ~5 min — baixando go modules e images)"

COMPOSE_CMD="sudo docker compose"

ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "
  set -euo pipefail
  cd /opt/lead-gen-motor/app

  # Para containers existentes
  ${COMPOSE_CMD} down --timeout 10 2>/dev/null || true

  # Gera go.sum se não existir (pode ter sido ignorado no scp)
  if [[ ! -f go.sum ]]; then
    echo 'Gerando go.sum via container Go...'
    sudo docker run --rm \\
      -v \$(pwd):/app \\
      -w /app \\
      -e GOMODCACHE=/tmp/gomodcache \\
      -e GOPATH=/tmp/gopath \\
      golang:1.22-alpine \\
      sh -c 'apk add --no-cache git >/dev/null 2>&1 && go mod tidy 2>&1'
  fi

  # Pull das imagens de infraestrutura
  echo 'Baixando imagens postgres e chromedp...'
  ${COMPOSE_CMD} pull postgres chromedp 2>/dev/null || true

  # Build + Start
  echo 'Buildando e iniciando containers...'
  ${COMPOSE_CMD} up -d $REBUILD --remove-orphans
"

# ============================================================
# 11. Aguarda aplicação responder
# ============================================================
sep
info "Aguardando aplicação ficar pronta..."
TENTATIVA=0
until curl -sf "http://$IP_PUBLICO:8080/api/v1/health" &>/dev/null; do
  TENTATIVA=$((TENTATIVA + 1))
  if [[ $TENTATIVA -ge 20 ]]; then
    warn "Aplicação ainda não respondeu. Mostrando logs:"
    ssh $SSH_OPTS "ec2-user@$IP_PUBLICO" "cd /opt/lead-gen-motor/app && sudo docker compose logs --tail=40 app" || true
    break
  fi
  echo -n "."
  sleep 10
done
echo ""

# ============================================================
# Status Final
# ============================================================
sep
echo ""
echo -e "${VERDE}============================================================${RESET}"
echo -e "${VERDE}   DEPLOY CONCLUÍDO!${RESET}"
echo -e "${VERDE}============================================================${RESET}"
echo ""
echo -e "  ${AZUL}IP:${RESET}      $IP_PUBLICO"
echo -e "  ${AZUL}API:${RESET}     http://$IP_PUBLICO:8080/api/v1"
echo -e "  ${AZUL}Health:${RESET}  http://$IP_PUBLICO:8080/api/v1/health"
echo -e "  ${AZUL}SSH:${RESET}     ssh -i $CHAVE_PRIV ec2-user@$IP_PUBLICO"
echo ""
echo -e "  ${AMARELO}Logs:${RESET}    ssh -i $CHAVE_PRIV ec2-user@$IP_PUBLICO"
echo -e "           'cd /opt/lead-gen-motor/app && sudo docker compose logs -f app'"
echo ""

HEALTH=$(curl -sf "http://$IP_PUBLICO:8080/api/v1/health" 2>/dev/null || echo '{"status":"iniciando..."}')
echo -e "  Health: ${VERDE}$HEALTH${RESET}"
echo ""
