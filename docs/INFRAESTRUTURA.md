# Infraestrutura AWS — Lead Gen Motor

## Visão Geral

Toda a infraestrutura é declarativa via **Terraform**. Um único `terraform apply` cria tudo; um `terraform destroy` remove tudo.

```
AWS Academy ($30/mês)
│
└── VPC: 10.0.0.0/16 (lead-gen-vpc)
    │
    └── Subnet Pública: 10.0.1.0/24 (us-east-1a)
        │
        ├── Internet Gateway
        ├── Route Table (0.0.0.0/0 → IGW)
        └── EC2 Spot Instance (t3.medium)
            ├── Security Group (SSH + :8080 + egress HTTPS/80/587)
            ├── IAM Instance Profile (SES + CloudWatch + SSM)
            ├── EBS gp3 20GB (encrypted)
            └── Docker Compose
                ├── lead-gen-app (Go binary)
                ├── postgres:16-alpine
                └── chromedp/headless-shell
```

---

## Recursos Terraform — Detalhamento

### `networking.tf`

| Recurso | Nome | Finalidade |
|---|---|---|
| `aws_vpc` | lead-gen-vpc | Rede isolada principal |
| `aws_subnet` | lead-gen-subnet-publica | Subnet pública (IP público automático) |
| `aws_internet_gateway` | lead-gen-igw | Saída para internet |
| `aws_route_table` | lead-gen-rt-publica | Roteia 0.0.0.0/0 para o IGW |
| `aws_security_group` | lead-gen-sg | Regras de firewall |

**Regras do Security Group:**

| Direção | Porta | Protocolo | Origem | Motivo |
|---|---|---|---|---|
| Ingress | 22 | TCP | `var.seu_ip_cidr` | SSH para gestão |
| Ingress | 8080 | TCP | 0.0.0.0/0 | API pública |
| Egress | 443 | TCP | 0.0.0.0/0 | APIs externas (Meta, AWS) |
| Egress | 80 | TCP | 0.0.0.0/0 | Atualizações yum |
| Egress | 587 | TCP | 0.0.0.0/0 | SMTP SES (STARTTLS) |
| Egress | 5432 | TCP | 10.0.0.0/16 | PostgreSQL interno Docker |

> **Para produção**: substitua `0.0.0.0/0` no SSH pelo seu IP estático (`curl ifconfig.me/ip`)  
> Configure: `TF_VAR_seu_ip_cidr="$(curl -s ifconfig.me/ip)/32"`

### `compute.tf`

**Spot Instance Request** (`aws_spot_instance_request`):

```hcl
spot_type                      = "persistent"
instance_interruption_behavior = "stop"
wait_for_fulfillment           = true
```

- **`persistent`**: o request permanece ativo após interrupção — AWS re-solicita automaticamente
- **`stop`**: quando interrompida, a instância para (não termina) → **dados do EBS preservados**
- **`wait_for_fulfillment = true`**: Terraform aguarda a instância ser alocada antes de continuar

**AMI**: Amazon Linux 2023 (buscada dinamicamente — sempre a mais recente)
```hcl
data "aws_ami" "amazon_linux_2023" {
  most_recent = true
  owners      = ["amazon"]
  filter { name = "name"; values = ["al2023-ami-*-kernel-*-x86_64"] }
}
```

**Volume EBS gp3**:
- 20GB de armazenamento
- 3000 IOPS + 125 MiB/s inclusos sem custo extra no gp3
- `encrypted = true` — dados em repouso sempre cifrados
- `delete_on_termination = true` — limpeza automática ao destruir

### `iam.tf`

**Política do princípio do menor privilégio** — a EC2 só pode:

| Ação | Recurso | Justificativa |
|---|---|---|
| `ses:SendEmail`, `ses:SendRawEmail`, etc. | `*` | Envio de emails de prospecção |
| `logs:CreateLogGroup/Stream/PutEvents` | `/lead-gen-motor*` | Logs no CloudWatch |
| `cloudwatch:PutMetricData` | namespace `LeadGenMotor` | Métricas customizadas |
| `ssm:GetParameter/s/ByPath` | `/lead-gen/*` | Secrets em runtime via SSM |
| `ec2:DescribeInstances/Tags` | `*` | Auto-diagnóstico de scripts |

> **Restrição AWS Academy**: a criação de IAM roles é limitada. O nome `lead-gen-ec2-role` é necessário (sem prefixo de path especial).

### `userdata.sh`

Script executado **uma única vez** na primeira inicialização da instância. Sequence:

```
1. dnf update -y                    (~3 min)
2. dnf install docker               (~1 min)
3. systemctl enable/start docker
4. usermod -aG docker ec2-user
5. instala Docker Compose v2        (~1 min)
6. instala/configura CloudWatch Agent
7. cria /opt/lead-gen-motor/
8. gera .env template
9. cria lead-gen-motor.service (systemd)
```

**Log do userdata**: `/var/log/userdata-lead-gen.log`  
**Para acompanhar**: `tail -f /var/log/userdata-lead-gen.log`

---

## Estimativa de Custos (AWS Academy $30)

| Recurso | Tipo | Custo/hora | Custo/mês (720h) |
|---|---|---|---|
| EC2 Spot t3.medium | Compute | ~$0.016 | ~$11.52 |
| EBS gp3 20GB | Storage | $0.08/GB-mês | $1.60 |
| CloudWatch Logs | Logs (7 dias) | ~$0.50/GB | ~$0.50 |
| Data Transfer | Egress | $0.09/GB | ~$0.50 |
| **TOTAL ESTIMADO** | | | **~$14-16/mês** |

> Spot t3.medium pode variar entre $0.012-$0.020/hr dependendo do horário.  
> Com $30 de budget, você tem ~45-50 dias de operação.

**Dica de economia**: desligue nos finais de semana com `./scripts/destroy.sh` e re-suba quando precisar.

---

## Mapa de Arquivos Terraform

```
terraform/
├── main.tf          Provider AWS, versões, backend (S3 opcional)
├── variables.tf     Todos os parâmetros configuráveis
├── networking.tf    VPC, subnet, IGW, route table, security group
├── iam.tf           Role, policy, instance profile
├── compute.tf       Spot request, AMI lookup, key pair, CloudWatch
├── outputs.tf       IP, IDs, comandos úteis pós-deploy
└── scripts/
    └── userdata.sh  Bootstrap da instância (Docker + serviços)
```

---

## Operações

### Deploy Completo
```bash
# 1. Configure credenciais AWS Academy
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...

# 2. Execute o deploy (faz tudo automaticamente)
./scripts/deploy.sh

# Ou manualmente:
cd terraform
ssh-keygen -t ed25519 -f lead-gen-key -N ""
terraform init
terraform apply
```

### Destruir Tudo
```bash
./scripts/destroy.sh
# Faz backup automático do PostgreSQL antes de destruir
```

### Acessar a Instância
```bash
ssh -i terraform/lead-gen-key ec2-user@$(cat .ultimo-ip)
```

### Ver Logs na Instância
```bash
# Logs da aplicação Go
ssh -i terraform/lead-gen-key ec2-user@$(cat .ultimo-ip) \
  "cd /opt/lead-gen-motor && docker-compose logs -f app"

# Logs do userdata (inicialização)
ssh -i terraform/lead-gen-key ec2-user@$(cat .ultimo-ip) \
  "tail -100 /var/log/userdata-lead-gen.log"

# CloudWatch Logs (sem SSH)
aws logs tail /lead-gen-motor/app --follow
```

### Atualizar Só a Aplicação (sem re-criar infra)
```bash
IP=$(cat .ultimo-ip)
KEY=terraform/lead-gen-key

# Copia novos arquivos
scp -i $KEY -r app/. ec2-user@$IP:/opt/lead-gen-motor/

# Reinicia containers
ssh -i $KEY ec2-user@$IP \
  "cd /opt/lead-gen-motor && docker-compose up -d --build app"
```

---

## Troubleshooting

### Spot não foi alocada
```bash
# Verifica preço spot atual
aws ec2 describe-spot-price-history \
  --instance-types t3.medium \
  --product-descriptions "Linux/UNIX" \
  --region us-east-1 \
  --max-items 5
```
Se o preço spot estiver acima de `$0.05`, aumente `var.spot_preco_maximo`.

### IAM Role bloqueada (AWS Academy)
O AWS Academy pode bloquear criação de roles com nomes específicos.  
Solução: renomeie no `variables.tf` → `default = "LabRole"` (role pré-criada no Academy).

### SSH não conecta
1. Verifique se `var.seu_ip_cidr` inclui seu IP atual
2. Aguarde 2-3 min após o `terraform apply` (userdata ainda rodando)
3. Verifique se a Spot request foi fulfillada: `aws ec2 describe-spot-instance-requests`
