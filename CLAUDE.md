# CLAUDE.md — Instruções para Agentes de IA

> Este arquivo é lido automaticamente pelo Claude Code antes de qualquer tarefa.
> Contém as regras de ouro do projeto. **Siga-as sem exceção.**

---

## Identidade do Projeto

**Nome:** Lead Gen Motor  
**Stack:** Go 1.22 + PostgreSQL 16 + Docker + Terraform + AWS  
**Padrão:** Arquitetura Hexagonal (Ports & Adapters)  
**Proprietário:** Demarchi MEI  
**Idioma do código:** Comentários e logs em **Português Brasil**; identificadores Go em **inglês** (convenção Go)

---

## Regras de Código

### Go
- **Arquitetura Hexagonal é inviolável**: nunca importe um adapter diretamente no domain ou no core. O fluxo é sempre: `cmd → core → ports (interface) ← adapters`
- **Sem frameworks HTTP externos**: use apenas `net/http` da stdlib. O projeto inteiro deve poder compilar com `CGO_ENABLED=0`
- **Logging**: use exclusivamente `log/slog` (stdlib Go 1.21+). Nunca use `fmt.Println` em código de produção
- **Erros**: sempre envolva com `fmt.Errorf("contexto: %w", err)`. Nunca descarte erros silenciosamente
- **Graceful shutdown**: todo código concorrente deve respeitar `context.Context`. Nunca use `os.Exit()` fora do `main.go`
- **Testes**: ao adicionar funcionalidade, adicione teste em `_test.go` no mesmo pacote

### Terraform
- **Nunca hardcode** ARNs, IDs de conta ou nomes de recursos no `.tf`. Sempre use `var` ou `data`
- **Custo primeiro**: antes de adicionar qualquer recurso AWS, estime o custo mensal no comentário do `.tf`
- **Tags obrigatórias**: todo recurso deve ter as tags `Projeto`, `Ambiente`, `Gerenciado`

### Docker
- **Imagem da aplicação deve ter < 50MB** — use `scratch` como base final
- **Sem secrets no Dockerfile**: credenciais sempre via `.env` ou AWS Secrets Manager
- **Healthcheck obrigatório** em todo serviço do docker-compose

---

## Estrutura de Diretórios — Onde Cada Coisa Fica

```
lead-gen-motor/
├── app/
│   ├── cmd/server/main.go          ← Entry point, DI, HTTP server, shutdown
│   ├── pkg/domain/                 ← Entidades puras (NUNCA importa nada externo)
│   │   ├── lead.go                 ← Entidade Lead + métodos de negócio
│   │   └── campaign.go             ← Campanha, Job, Mensagem
│   ├── pkg/ports/                  ← Interfaces (contratos entre camadas)
│   │   ├── messenger.go            ← MessengerPort, NotificacaoPort
│   │   └── repository.go           ← LeadRepo, CampanhaRepo, JobRepo
│   ├── pkg/adapters/               ← Implementações concretas (externos)
│   │   ├── email/ses.go            ← Amazon SES
│   │   ├── whatsapp/webhook.go     ← Meta Business API
│   │   ├── social/chromedp.go      ← LinkedIn/Instagram/Facebook
│   │   └── sms/sns.go              ← Amazon SNS (notificação closer)
│   ├── pkg/core/                   ← Casos de uso (orquestração)
│   │   ├── dispatcher.go           ← Produtor de jobs (loop + backpressure)
│   │   ├── worker.go               ← Consumidor com rate limiting
│   │   └── scoring.go              ← Algoritmo de lead scoring
│   ├── pkg/repository/
│   │   └── postgres.go             ← Adapter PostgreSQL (implementa ports)
│   ├── config/
│   │   ├── config.go               ← Carrega ENV + YAML
│   │   └── limits.yaml             ← Rate limits e configurações de workers
│   ├── Dockerfile                  ← Multi-stage, final em scratch
│   └── docker-compose.yml          ← App + PostgreSQL + Chrome
├── terraform/                      ← IaC completo
├── migrations/                     ← SQL migrations (executadas pelo PostgreSQL na init)
├── scripts/
│   ├── deploy.sh                   ← Terraform + SCP + docker-compose up
│   └── destroy.sh                  ← Backup + terraform destroy
└── docs/                           ← Documentação detalhada
```

---

## Padrões de Evolução — Como Adicionar Coisas Novas

### Adicionar novo canal de mensagem
1. Crie `app/pkg/adapters/NOVOCANAL/adapter.go`
2. Implemente a interface `ports.MessengerPort` (métodos: `Enviar`, `Canal`, `Disponivel`)
3. Adicione a constante do canal em `pkg/domain/lead.go` (tipo `Canal`)
4. Registre o adapter no `cmd/server/main.go` no mapa `mensageiros`
5. Adicione limites em `config/limits.yaml`
6. Atualize o enum `canal_tipo` na migration SQL

### Adicionar nova regra de negócio ao Lead Scoring
- Edite APENAS `pkg/core/scoring.go` — método `calcularScore`
- As regras são configuráveis via `config/limits.yaml` seção `pontuacao`
- Documente a regra em `docs/REGRAS-NEGOCIO.md`

### Adicionar novo endpoint HTTP
- Adicione o handler em `cmd/server/main.go` (ou extraia para `pkg/api/` se crescer)
- Registre a rota em `registrarRotas()`
- Documente em `docs/API.md`

### Adicionar novo recurso Terraform
- Crie ou edite o `.tf` correspondente por categoria (networking, compute, iam)
- Adicione outputs relevantes em `outputs.tf`
- Estime custo mensal em comentário antes do recurso

---

## Variáveis de Ambiente — Referência Rápida

| Variável | Obrigatória | Descrição |
|---|---|---|
| `DB_SENHA` | SIM | Senha PostgreSQL |
| `AWS_REGION` | SIM | Região AWS (padrão: us-east-1) |
| `SES_REMETENTE_EMAIL` | Para email | Email verificado no SES |
| `WHATSAPP_TOKEN` | Para WhatsApp | Token Meta Business API |
| `WHATSAPP_PHONE_ID` | Para WhatsApp | Phone Number ID Meta |
| `LINKEDIN_USER/PASS` | Para LinkedIn | Credenciais da conta LinkedIn |
| `INSTAGRAM_USER/PASS` | Para Instagram | Credenciais da conta Instagram |
| `FACEBOOK_USER/PASS` | Para Facebook | Credenciais da conta Facebook |
| `SNS_NUMERO_CLOSER` | Para SMS | Número E.164 do vendedor |
| `CHROME_URL` | Para social | WebSocket do Chrome headless |

---

## Comandos Úteis

```bash
# Subir localmente para desenvolvimento
cd app && docker-compose up -d

# Ver logs da aplicação
docker-compose logs -f app

# Deploy completo na AWS
./scripts/deploy.sh

# Destruir tudo (faz backup automático)
./scripts/destroy.sh

# Checar health
curl http://localhost:8080/api/v1/health

# Ver status do dispatcher/workers
curl http://localhost:8080/api/v1/status
```

---

## Restrições do Ambiente AWS Academy (FIAP)

- Budget: **$30 USD** — instância roda ~20-22 dias/mês no Spot
- IAM: não é possível criar roles com nomes arbitrários — use prefixo `lead-gen-`
- SES: começa em **sandbox** (só envia para emails verificados). Solicitar saída do sandbox para produção
- Região padrão: **us-east-1**
- Spot Instance: se a AWS interromper, o estado `persistent` re-solicita automaticamente
