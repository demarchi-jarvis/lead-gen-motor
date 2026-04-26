#!/bin/bash
# ============================================================
# destroy.sh — Destrói TODA a infraestrutura AWS
#
# USE COM CUIDADO: remove VPC, EC2, Security Groups, IAM Role
# Os dados do PostgreSQL (EBS volume) também serão DELETADOS
#
# USO:
#   ./scripts/destroy.sh
#   ./scripts/destroy.sh --auto-approve   # pula confirmação
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RAIZ="$(cd "$SCRIPT_DIR/.." && pwd)"
TF_DIR="$RAIZ/terraform"

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

echo ""
echo -e "${VERMELHO}╔══════════════════════════════════════════════════════╗${RESET}"
echo -e "${VERMELHO}║           DESTRUIÇÃO DE INFRAESTRUTURA AWS           ║${RESET}"
echo -e "${VERMELHO}║                                                      ║${RESET}"
echo -e "${VERMELHO}║  Esta operação irá DELETAR permanentemente:          ║${RESET}"
echo -e "${VERMELHO}║  • Instância EC2 Spot                                ║${RESET}"
echo -e "${VERMELHO}║  • VPC, Subnet, Security Group                       ║${RESET}"
echo -e "${VERMELHO}║  • IAM Role e Instance Profile                       ║${RESET}"
echo -e "${VERMELHO}║  • CloudWatch Log Groups (dados de log)              ║${RESET}"
echo -e "${VERMELHO}║  • Volume EBS (DADOS DO POSTGRESQL!)                 ║${RESET}"
echo -e "${VERMELHO}╚══════════════════════════════════════════════════════╝${RESET}"
echo ""

if [[ -z "$AUTO_APPROVE" ]]; then
  echo -e "${VERMELHO}CONFIRMAÇÃO NECESSÁRIA${RESET}"
  echo -e "Digite ${AMARELO}DESTRUIR${RESET} para confirmar (qualquer outra coisa cancela):"
  read -r CONFIRMA

  if [[ "$CONFIRMA" != "DESTRUIR" ]]; then
    log_aviso "Operação cancelada. Nenhum recurso foi removido."
    exit 0
  fi
fi

# ============================================================
# Backup opcional antes de destruir
# ============================================================
IP_FILE="$RAIZ/.ultimo-ip"
if [[ -f "$IP_FILE" ]]; then
  IP_PUBLICO=$(cat "$IP_FILE")
  CHAVE_PRIV="$TF_DIR/lead-gen-key"

  if [[ -f "$CHAVE_PRIV" ]]; then
    log_info "Tentando fazer backup do PostgreSQL antes de destruir..."
    BACKUP_DIR="$RAIZ/backups"
    mkdir -p "$BACKUP_DIR"
    BACKUP_FILE="$BACKUP_DIR/backup_$(date +%Y%m%d_%H%M%S).sql.gz"

    if ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -i "$CHAVE_PRIV" "ec2-user@$IP_PUBLICO" \
       "cd /opt/lead-gen-motor && docker-compose exec -T postgres pg_dump -U leadgen leadgen 2>/dev/null" \
       | gzip > "$BACKUP_FILE" 2>/dev/null; then
      log_sucesso "Backup salvo em: $BACKUP_FILE"
    else
      log_aviso "Falha no backup (instância pode estar indisponível). Continuando destroy..."
    fi
  fi
fi

# ============================================================
# Terraform Destroy
# ============================================================
cd "$TF_DIR"

if [[ ! -f "terraform.tfstate" ]] && [[ ! -f ".terraform/terraform.tfstate" ]]; then
  log_aviso "Nenhum tfstate encontrado. Infraestrutura pode já estar destruída."
  exit 0
fi

log_info "Iniciando terraform destroy..."

terraform destroy $AUTO_APPROVE

# ============================================================
# Limpeza local
# ============================================================
log_info "Limpando arquivos temporários locais..."
rm -f "$TF_DIR/tfplan"
rm -f "$RAIZ/.ultimo-ip"

log_sucesso "=============================================="
log_sucesso " INFRAESTRUTURA DESTRUÍDA COM SUCESSO!"
log_sucesso " Todos os recursos AWS foram removidos."
log_sucesso " Custo zerado a partir de agora."
log_sucesso "=============================================="

if ls "$RAIZ/backups/"*.gz &>/dev/null 2>&1; then
  echo ""
  log_info "Backups disponíveis em: $RAIZ/backups/"
  ls -lh "$RAIZ/backups/"*.gz
fi
