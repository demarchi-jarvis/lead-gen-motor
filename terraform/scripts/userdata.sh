#!/bin/bash
# ============================================================
# UserData Script — Lead Gen Motor
# Executado uma vez na primeira inicialização da instância
# Instala Docker, Docker Compose e prepara o ambiente
# ============================================================
set -euo pipefail

# Redireciona toda saída para log + console
exec > >(tee /var/log/userdata-lead-gen.log | logger -t userdata -s 2>/dev/console) 2>&1

echo "============================================================"
echo " Lead Gen Motor — Inicialização da Instância"
echo " Data/Hora: $(date '+%Y-%m-%d %H:%M:%S UTC')"
echo "============================================================"

# ============================================================
# 1. Atualização do Sistema
# ============================================================
echo "--- [1/7] Atualizando sistema base ---"
dnf update -y --quiet
dnf install -y git curl wget htop jq unzip --quiet

# ============================================================
# 2. Instalação Docker
# ============================================================
echo "--- [2/7] Instalando Docker ---"
dnf install -y docker
systemctl enable docker
systemctl start docker
usermod -aG docker ec2-user

# Configura daemon Docker: log rotation para não estourar disco
cat > /etc/docker/daemon.json << 'DOCKEREOF'
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  },
  "storage-driver": "overlay2"
}
DOCKEREOF

systemctl restart docker
echo "Docker version: $(docker --version)"

# ============================================================
# 3. Instalação Docker Compose v2
# ============================================================
echo "--- [3/7] Instalando Docker Compose ---"
COMPOSE_VERSION=$(curl -sf https://api.github.com/repos/docker/compose/releases/latest | jq -r '.tag_name' || echo "v2.27.0")
COMPOSE_URL="https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-linux-x86_64"

curl -SL "$COMPOSE_URL" -o /usr/local/bin/docker-compose
chmod +x /usr/local/bin/docker-compose
ln -sf /usr/local/bin/docker-compose /usr/bin/docker-compose

echo "Docker Compose version: $(docker-compose --version)"

# ============================================================
# 4. Instalação AWS CLI v2 (para scripts de operação)
# ============================================================
echo "--- [4/7] Verificando AWS CLI ---"
if ! command -v aws &> /dev/null; then
  curl -sf "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip
  unzip -q /tmp/awscliv2.zip -d /tmp/
  /tmp/aws/install
  rm -rf /tmp/aws /tmp/awscliv2.zip
fi
echo "AWS CLI version: $(aws --version)"

# ============================================================
# 5. Instalação CloudWatch Agent
# ============================================================
echo "--- [5/7] Configurando CloudWatch Agent ---"
dnf install -y amazon-cloudwatch-agent

cat > /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json << 'CWEOF'
{
  "agent": {
    "metrics_collection_interval": 60,
    "run_as_user": "cwagent"
  },
  "logs": {
    "logs_collected": {
      "files": {
        "collect_list": [
          {
            "file_path": "/var/log/lead-gen/*.log",
            "log_group_name": "/lead-gen-motor/app",
            "log_stream_name": "{instance_id}/app",
            "timestamp_format": "%Y-%m-%dT%H:%M:%S"
          },
          {
            "file_path": "/var/log/userdata-lead-gen.log",
            "log_group_name": "/lead-gen-motor/sistema",
            "log_stream_name": "{instance_id}/userdata"
          }
        ]
      }
    }
  },
  "metrics": {
    "namespace": "LeadGenMotor",
    "metrics_collected": {
      "mem": {
        "measurement": ["mem_used_percent"]
      },
      "disk": {
        "measurement": ["disk_used_percent"],
        "resources": ["/"]
      }
    }
  }
}
CWEOF

systemctl enable amazon-cloudwatch-agent
systemctl start amazon-cloudwatch-agent

# ============================================================
# 6. Prepara Diretório da Aplicação
# ============================================================
echo "--- [6/7] Preparando ambiente da aplicação ---"

APP_DIR="/opt/lead-gen-motor"
mkdir -p "$APP_DIR"
mkdir -p /var/log/lead-gen
chown -R ec2-user:ec2-user "$APP_DIR" /var/log/lead-gen

# Arquivo .env com configurações base (sobrescrever após deploy)
cat > "$APP_DIR/.env" << ENVEOF
# Configurações Lead Gen Motor — EDITAR ANTES DO PRIMEIRO USE
APP_PORTA=8080
APP_AMBIENTE=prod
AWS_REGION=${aws_region}

# Banco de Dados
DB_HOST=postgres
DB_PORTA=5432
DB_NOME=leadgen
DB_USUARIO=leadgen
DB_SENHA=${db_senha}

# SES — verificar domínio/email no console AWS antes de usar
SES_REMETENTE_EMAIL=noreply@seudominio.com
SES_REMETENTE_NOME=Lead Gen Motor

# WhatsApp Business API (substitua pelo seu token)
WHATSAPP_API_URL=https://graph.facebook.com/v18.0
WHATSAPP_TOKEN=SEU_TOKEN_AQUI
WHATSAPP_PHONE_ID=SEU_PHONE_NUMBER_ID

# Chrome para chromedp (container separado)
CHROME_URL=ws://chromedp:9222

# Workers
WORKERS_QUANTIDADE=5
WORKERS_FILA_TAMANHO=100
ENVEOF

chown ec2-user:ec2-user "$APP_DIR/.env"
chmod 600 "$APP_DIR/.env"

# ============================================================
# 7. Serviço Systemd — auto-start da aplicação
# ============================================================
echo "--- [7/7] Configurando serviço systemd ---"

cat > /etc/systemd/system/lead-gen-motor.service << 'SVCEOF'
[Unit]
Description=Lead Gen Motor - Omnichannel Prospecting Engine
Documentation=https://github.com/demarchi/lead-gen-motor
After=docker.service network-online.target
Requires=docker.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/lead-gen-motor
ExecStartPre=-/usr/bin/docker-compose pull --quiet
ExecStart=/usr/bin/docker-compose up -d --remove-orphans
ExecStop=/usr/bin/docker-compose down --timeout 30
ExecReload=/usr/bin/docker-compose restart

User=ec2-user
Group=ec2-user

Restart=on-failure
RestartSec=30
StartLimitIntervalSec=300
StartLimitBurst=5

StandardOutput=journal
StandardError=journal
SyslogIdentifier=lead-gen-motor

[Install]
WantedBy=multi-user.target
SVCEOF

systemctl daemon-reload
systemctl enable lead-gen-motor

# ============================================================
# Finalização
# ============================================================
echo ""
echo "============================================================"
echo " Inicialização CONCLUÍDA com sucesso!"
echo " Próximos passos:"
echo " 1. Copie os arquivos da aplicação para: $APP_DIR"
echo " 2. Edite o .env: $APP_DIR/.env"
echo " 3. Inicie: sudo systemctl start lead-gen-motor"
echo "    ou:     cd $APP_DIR && docker-compose up -d"
echo "============================================================"
