# DOCUMENTATION.md — Lead Gen Motor

> Documentação técnica exaustiva. Analisa o código real gerado, explica cada decisão arquitetural, padrão de projeto, configuração de infraestrutura e estimativa de custo. Escrita para engenheiros que vão manter, escalar ou auditar este sistema.

---

## Índice

1. [Capa e Sumário](#1-capa-e-sumário)
2. [Introdução e Visão Geral](#2-introdução-e-visão-geral)
3. [Objetivo e Escopo](#3-objetivo-e-escopo)
4. [Stack Tecnológica e Vantagens](#4-stack-tecnológica-e-vantagens)
5. [Arquitetura e Fluxo de Dados](#5-arquitetura-e-fluxo-de-dados)
6. [Detalhamento Técnico: Backend Go](#6-detalhamento-técnico-backend-go)
7. [Detalhamento Técnico: Infraestrutura IaC](#7-detalhamento-técnico-infraestrutura-iac)
8. [Guia de Execução](#8-guia-de-execução)
9. [Requisitos e Qualidade](#9-requisitos-e-qualidade)
10. [Operacional e Custos](#10-operacional-e-custos)
11. [Encerramento](#11-encerramento)

---

## 1. Capa e Sumário

```
╔══════════════════════════════════════════════════════════════════════════╗
║                       LEAD GEN MOTOR — v1.0.0                          ║
║                                                                          ║
║  Motor de Prospecção Omnichannel B2B                                    ║
║  Automação de outreach via 6 canais com rate limiting inteligente       ║
║                                                                          ║
║  Linguagem : Go 1.22 (CGO_ENABLED=0, binário estático ~20MB)           ║
║  Banco     : PostgreSQL 16 Alpine                                        ║
║  Cloud     : AWS (EC2 Spot · SES · SNS · CloudWatch · SSM)             ║
║  IaC       : Terraform >= 1.5                                            ║
║  Padrão    : Arquitetura Hexagonal (Ports & Adapters)                  ║
║  Budget    : AWS Academy $30 → ~$14-16/mês operacional                 ║
╚══════════════════════════════════════════════════════════════════════════╝
```

Este documento cobre:
- A análise do código Go real gerado (goroutines, interfaces, error wrapping)
- As decisões de design das 5 camadas da arquitetura hexagonal
- A lógica de cada recurso Terraform e por que foi escolhido
- O modelo relacional do PostgreSQL com índices e locking
- Estimativa detalhada de custo por recurso AWS
- Desafios técnicos encontrados e soluções aplicadas

---

## 2. Introdução e Visão Geral

### 2.1 Visão do Projeto

Empresas B2B perdem tempo e dinheiro tentando orquestrar prospecção manual entre múltiplos canais — uma planilha para LinkedIn, outra para email, nenhuma integração entre elas. Ferramentas SaaS como Instantly.ai, Reply.io ou Lemlist resolvem o problema mas custam entre $60-$300/mês, não dão acesso ao código e têm rate limits arbitrários.

O Lead Gen Motor resolve isso com uma engine própria: código aberto, rodando na sua infraestrutura AWS, custando $14/mês no lugar de $150.

### 2.2 Contexto do Problema Técnico

O problema central não é "enviar um email". É **enviar N mensagens por múltiplos canais, com taxas diferentes por canal, de forma concorrente, sem duplicar envios, sem derrubar contas por excesso de requisições, e notificando o time de vendas no momento certo**.

Isso exige:

| Problema | Solução Implementada |
|---|---|
| Rate limiting diferente por canal | Token Bucket (`golang.org/x/time/rate`) por worker por canal |
| Processamento concorrente sem duplicação | `SELECT FOR UPDATE SKIP LOCKED` no PostgreSQL |
| Canais heterogêneos (API REST vs headless browser) | Interface `MessengerPort` — polimorfismo via Go interfaces |
| Custo AWS < $30/mês | Spot Instance `persistent` + sem NAT Gateway |
| Binário pequeno para deploy rápido | Multi-stage Docker build com `scratch` como base final |
| Shutdown sem perda de jobs em andamento | `context.Context` propagado + `sync.WaitGroup` nos workers |

---

## 3. Objetivo e Escopo

### Dentro do Escopo

- API REST para CRUD de leads e campanhas
- Motor de despacho assíncrono (Dispatcher + Workers com goroutines)
- 6 adapters de canal: Email (SES), WhatsApp (Meta API), LinkedIn, Instagram, Facebook (chromedp), SMS (SNS)
- Lead scoring automático com algoritmo configurável via YAML
- Notificação do closer (vendedor) via SMS quando lead fica "quente"
- Infraestrutura AWS completa via Terraform (VPC, EC2 Spot, IAM, CloudWatch)
- Docker Compose para ambiente local e produção na EC2
- Scripts de deploy e destroy one-command

### Fora do Escopo (backlog)

- Autenticação JWT na API (endpoints atualmente abertos)
- Dashboard web com visualização de funil
- Lambda de start/stop da instância EC2
- Pipeline CI/CD (GitHub Actions → ECR → EC2)
- Kubernetes (HPA, Deployments, Ingress) — ver seção 11 para roadmap
- Importação em bulk via CSV
- Score decay automático por inatividade

---

## 4. Stack Tecnológica e Vantagens

### 4.1 Por Que Go

**Performance de concorrência com consumo mínimo de memória.** Cada Worker é uma goroutine — uma abstração sobre threads do SO com stack inicial de apenas 8KB (vs ~1MB para uma thread Java/Python). O projeto roda 5 workers concorrentes em ~50-100MB de RAM total. Em Python ou Node, o mesmo workload exigiria 300-500MB.

**Tipagem estática com zero custo de runtime.** O polimorfismo do `MessengerPort` — que permite ao sistema tratar SES, WhatsApp e chromedp da mesma forma — é resolvido em tempo de compilação. Não há reflection, não há `interface{}` overhead.

**Binário estático.** Com `CGO_ENABLED=0`, o `go build` produz um ELF binário que não depende de nenhuma biblioteca do sistema operacional. Isso permite a imagem Docker em `scratch` — sem shell, sem libc, sem superfície de ataque.

```bash
# Compilação real do projeto — flags usadas no Dockerfile
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build \
  -ldflags="-w -s -X main.versao=1.0.0 -X main.buildTime=$(date -u +%Y%m%d%H%M%S)" \
  -trimpath \
  -o /build/lead-gen-motor \
  ./cmd/server/

# -w: remove DWARF debug info (~20% menor)
# -s: remove symbol table (~10% menor)
# -trimpath: remove caminhos do host do binário (segurança)
# -X: injeta variáveis em tempo de link (versão, build time)
```

### 4.2 Por Que Terraform (não CloudFormation, CDK ou Pulumi)

**Terraform** foi escolhido por três razões:

1. **Provider agnóstico**: o mesmo `terraform destroy` funciona se migrarmos de AWS para GCP amanhã. CloudFormation é lock-in total.
2. **State declarativo**: `terraform plan` mostra exatamente o que será criado/destruído antes de executar. Essencial no ambiente de custo limitado do Academy.
3. **Maturidade do provider AWS**: o `hashicorp/aws ~> 5.0` tem cobertura de 99% dos recursos. O `aws_spot_instance_request` com `wait_for_fulfillment = true` não tem equivalente fácil em CloudFormation.

### 4.3 Por Que PostgreSQL (não DynamoDB ou RDS)

**PostgreSQL tem `SELECT FOR UPDATE SKIP LOCKED`** — uma primitiva de locking a nível de linha que torna o padrão Dispatcher/Worker safe com múltiplos consumers sem necessidade de Redis ou SQS. DynamoDB não oferece esse tipo de locking transacional. RDS custaria ~$15/mês extra no Academy.

### 4.4 Versões Pinadas

```go
// go.mod — versões de produção
module github.com/demarchi/lead-gen-motor

go 1.22

require (
    github.com/aws/aws-sdk-go-v2             v1.27.2
    github.com/aws/aws-sdk-go-v2/config      v1.27.16
    github.com/aws/aws-sdk-go-v2/service/ses v1.22.4
    github.com/aws/aws-sdk-go-v2/service/sns v1.29.4
    github.com/chromedp/chromedp             v0.9.5
    github.com/lib/pq                        v1.10.9
    golang.org/x/time                        v0.5.0
    gopkg.in/yaml.v3                         v3.0.1
)
```

```hcl
# terraform/main.tf — versão pinada do provider
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"  # permite 5.x.x, bloqueia 6.0.0
    }
  }
}
```

---

## 5. Arquitetura e Fluxo de Dados

### 5.1 Arquitetura Hexagonal — As Cinco Camadas

O projeto implementa estritamente a **Arquitetura Hexagonal** (Alistair Cockburn, 2005). A regra de dependência é unidirecional: as camadas externas conhecem as internas, nunca o contrário.

```
                    ┌───────────────────────────────────────────┐
                    │         DRIVERS (entrada)                  │
                    │   HTTP Client · Webhooks · CLI             │
                    └──────────────────┬────────────────────────┘
                                       │
                    ┌──────────────────▼────────────────────────┐
                    │         cmd/server/main.go                 │
                    │   Composição de dependências (DI manual)   │
                    │   HTTP Server · Graceful Shutdown          │
                    └──────────────────┬────────────────────────┘
                                       │  usa
                    ┌──────────────────▼────────────────────────┐
                    │         pkg/core/   (Application)          │
                    │   Dispatcher · Worker · ServicoScoring     │
                    │   Depende apenas de ports/ (interfaces)    │
                    └──────┬─────────────────────────┬──────────┘
                   usa     │                         │     usa
          ┌────────────────▼──────────┐   ┌─────────▼────────────────────┐
          │     pkg/ports/            │   │     pkg/domain/               │
          │  MessengerPort            │   │  Lead · Campanha · Job        │
          │  LeadRepositoryPort       │   │  Canal · StatusLead           │
          │  JobRepositoryPort        │   │  Regras puras de negócio      │
          │  NotificacaoPort          │   │  Zero dependências externas   │
          └────────────────┬──────────┘   └──────────────────────────────┘
                 implementa │
          ┌─────────────────▼───────────────────────────────────────────┐
          │             pkg/adapters/  (Driven/Infrastructure)          │
          │  email/ses.go   whatsapp/webhook.go   social/chromedp.go   │
          │  sms/sns.go     repository/postgres.go                      │
          │  Conhecem: AWS SDK, lib/pq, chromedp, net/http              │
          └─────────────────────────────────────────────────────────────┘
```

**Por que isso importa na prática?** Quando o LinkedIn mudar a estrutura do DOM (quebrando o chromedp adapter), apenas `pkg/adapters/social/chromedp_adapter.go` precisa ser atualizado. O `core/dispatcher.go`, o `core/worker.go` e toda a lógica de negócio ficam intactos.

### 5.2 O Pattern Dispatcher/Worker — Produtor-Consumidor com Backpressure

Este é o coração concorrente do sistema. A implementação usa os primitivos nativos de Go sem nenhuma dependência externa.

```
┌──────────────────────────────────────────────────────────────────┐
│                    POSTGRESQL: tabela jobs                        │
│  status='pendente' · ORDER BY prioridade DESC · SKIP LOCKED      │
└────────────────────────────┬─────────────────────────────────────┘
                             │ a cada 30s: BuscarPendentes(ctx, 50)
                             ▼
┌──────────────────────────────────────────────────────────────────┐
│              DISPATCHER (1 goroutine)                             │
│                                                                   │
│  filaJobs := make(chan *domain.Job, 100)  ← channel buffered      │
│                                                                   │
│  for _, job := range pendentes {                                  │
│      select {                                                     │
│      case filaJobs <- job:                                        │
│          jobRepo.MarcarProcessando(ctx, job.ID)                   │
│      default:                                                     │
│          // backpressure: channel cheio, job fica no banco        │
│      }                                                            │
│  }                                                                │
└────────────────────────────┬─────────────────────────────────────┘
                             │ channel buffered cap=100
            ┌────────────────┼────────────────────────────┐
            ▼                ▼                            ▼
    ┌───────────────┐ ┌───────────────┐          ┌───────────────┐
    │  Worker-1     │ │  Worker-2     │   ...    │  Worker-N     │
    │               │ │               │          │               │
    │ rate.Limiter  │ │ rate.Limiter  │          │ rate.Limiter  │
    │ [email: 50/h] │ │ [wa:  10/h]  │          │ [li:   5/h]  │
    │ [wa:   10/h]  │ │ [li:   5/h]  │          │ [ig:   5/h]  │
    │ ...           │ │ ...           │          │ ...           │
    │               │ │               │          │               │
    │ limiter.Wait()│ │ limiter.Wait()│          │ limiter.Wait()│
    │ messenger     │ │ messenger     │          │ messenger     │
    │ .Enviar()     │ │ .Enviar()     │          │ .Enviar()     │
    └───────────────┘ └───────────────┘          └───────────────┘
```

**Detalhe crítico sobre backpressure:** o `select { case filaJobs <- job: ... default: }` é não-bloqueante. Se o channel estiver cheio (100 jobs já enfileirados), o Dispatcher pula aquele ciclo. Os jobs permanecem no banco como `pendente` e serão pegos no próximo tick de 30 segundos. Isso evita que o Dispatcher acumule jobs em memória indefinidamente.

### 5.3 Fluxo de Dados: De um POST HTTP até o envio pelo SES

```
1. POST /api/v1/campanhas/{id}/despachar
   Body: {"leads_ids": ["uuid1", "uuid2"], "canal": "email"}
   │
   ▼ cmd/server/main.go:despacharCampanha()
   │
2. jobRepo.CriarJobsParaCampanha(ctx, campanhaID, leadsIDs, "email")
   │   └─ INSERT INTO jobs (lead_id, campanha_id, canal, prioridade)
   │       VALUES ($1,$2,$3,5)
   │       ON CONFLICT (lead_id, campanha_id, canal) DO NOTHING
   │   └─ Transação atômica: todos os jobs ou nenhum
   │
   ▼ HTTP 202 Accepted {"mensagem": "jobs criados", "total_leads": 2}
   │
3. Dispatcher tick (30s depois ou imediato se primeiro ciclo)
   │   └─ SELECT ... FROM jobs WHERE status='pendente'
   │       FOR UPDATE OF jobs SKIP LOCKED LIMIT 50
   │   └─ UPDATE jobs SET status='processando', tentativas=tentativas+1
   │   └─ jobs enviados ao channel: filaJobs <- job
   │
4. Worker-2 pega o job do channel
   │   └─ limitadores["email"].Wait(ctx)
   │       → taxa: 50/3600 = 0.0139 tokens/segundo
   │       → burst máx: 5 → primeiros 5 envios são imediatos
   │       → após o burst: espera ~72 segundos entre envios
   │
5. campanha.RenderizarMensagem(lead)
   │   └─ "Olá {{nome}}, CEO da {{empresa}}" 
   │   └─ → "Olá João, CEO da Acme"
   │   └─ substituição via strings.ReplaceAll (sem template engine externa)
   │
6. sesAdapter.Enviar(ctx, MensagemEnvio{Lead, Assunto, Corpo, Canal})
   │   └─ ses.SendEmail(ctx, &ses.SendEmailInput{...})
   │   └─ Sucesso: ResultadoEnvio{Sucesso: true, IDExterno: "0102018e..."}
   │   └─ Falha transiente: ResultadoEnvio{DeveRetentar: true}
   │   └─ Falha permanente: ResultadoEnvio{DeveRetentar: false}
   │
7. Worker atualiza banco e lead
   │   └─ jobRepo.MarcarEnviado(ctx, jobID, idExterno)
   │   └─ leadRepo.Atualizar(ctx, lead) → UltimoContatoEm, Status="contatado"
   │
8. scoring.RecalcularScore(ctx, lead)
       └─ ContarInteracoes(ctx, leadID) → 1 interação
       └─ calcularScore: 4 campos × 5 = 20 + 5 (status contatado) = 25
       └─ 25 < 60 → não é lead quente ainda
       └─ AtualizarPontuacao(ctx, leadID, 25, "contatado")
```

### 5.4 Fluxo de Lead Quente: do Webhook ao SMS do Closer

```
POST /api/v1/webhooks/interacao
{"lead_id": "uuid", "tipo": "respondeu_email", "canal": "email"}
│
▼ scoring.ProcessarInteracao(ctx, leadID, "respondeu_email", "email", dados)
│
├── leadRepo.RegistrarInteracao(ctx, leadID, "respondeu_email", "email", {})
│       └─ INSERT INTO interacoes (lead_id, tipo, canal, dados)
│
├── leadRepo.BuscarPorID(ctx, leadID) → lead atual (pontuacao=25)
│
├── lead.RegistrarResposta()
│       └─ lead.Status = "respondeu"
│       └─ lead.AdicionarPontos(30) → pontuacao = 55
│
└── scoring.RecalcularScore(ctx, lead)
        └─ ContarInteracoes → 1 interação
        └─ calcularScore:
        │   campos preenchidos: 4 × 5 = 20 pontos
        │   status respondeu: +30 pontos
        │   interações extras: 0 pontos (apenas 1 interação)
        │   total: 50... mas lead.Pontuacao já é 55 (AdicionarPontos somou)
        │   → novaScore = 55 (> 60? NÃO, ainda não)
        │
        ▼ (numa segunda resposta, suponha mais 1 interação)
        └─ ContarInteracoes → 2 interações
        └─ calcularScore:
            campos: 20 + respondeu: 30 + extras: (2-1)×10 = 10 → TOTAL = 60
            60 >= threshold(60) → ficouQuente = true
        └─ AtualizarPontuacao(ctx, leadID, 60, "quente")
        └─ notificador.NotificarLeadQuente(ctx, lead, campanha)
                └─ sns.Publish(ctx, &sns.PublishInput{
                       PhoneNumber: aws.String("+5511999..."),  // número do closer
                       Message: "🔥 LEAD QUENTE!\nNome: João Silva\n..."
                   })
```

---

## 6. Detalhamento Técnico: Backend Go

### 6.1 Estrutura de Pacotes e Responsabilidades

```
app/
├── cmd/server/
│   └── main.go              COMPOSIÇÃO: instancia tudo e conecta as peças
│                            • Carrega config → instancia DB → cria repos
│                            • Instancia adapters → cria dispatcher → cria HTTP server
│                            • Inicia goroutines → aguarda shutdown signal
│
├── pkg/domain/              DOMÍNIO: regras de negócio puras
│   ├── lead.go              • tipo Lead struct com 17 campos
│   │                        • Lead.AdicionarPontos() → lógica de scoring inline
│   │                        • Lead.CanaisDisponiveis() → filtro de canais com dados
│   │                        • Lead.RenderizarMensagem() move para campaign.go
│   └── campaign.go          • tipo Campanha struct com máquina de estados
│                            • Campanha.Ativar() → transição com validação
│                            • Campanha.RenderizarMensagem() → substituição de variáveis
│                            • tipo Job struct (unidade de trabalho)
│
├── pkg/ports/               CONTRATOS: interfaces Go puras
│   ├── messenger.go         • MessengerPort{Enviar, Canal, Disponivel}
│   │                        • NotificacaoPort{NotificarLeadQuente}
│   │                        • MensagemEnvio struct (input)
│   │                        • ResultadoEnvio struct (output com DeveRetentar)
│   └── repository.go        • LeadRepositoryPort{Salvar,BuscarPorID,Listar,...}
│                            • CampanhaRepositoryPort{Salvar,BuscarPorID,...}
│                            • JobRepositoryPort{BuscarPendentes,MarcarProcessando,...}
│
├── pkg/adapters/            IMPLEMENTAÇÕES EXTERNAS
│   ├── email/ses.go         • aws-sdk-go-v2/service/ses → SendEmailInput
│   │                        • parse de erros para DeveRetentar boolean
│   ├── whatsapp/webhook.go  • HTTP POST para graph.facebook.com/v18.0/{phone_id}/messages
│   │                        • normalização de telefone para E.164
│   ├── social/              • chromedp.NewRemoteAllocator(ctx, "ws://chromedp:9222")
│   │   └── chromedp.go      • LinkedIn: detecta Connect vs Message button
│   │                        • delays aleatórios 500-2000ms (anti-bot)
│   └── sms/sns.go           • sns.Publish() para lead (canal SMS) E closer (notificação)
│                            • MessageAttributes "AWS.SNS.SMS.SMSType": "Transactional"
│
├── pkg/core/                CASOS DE USO: orquestração
│   ├── dispatcher.go        • Dispatcher struct com chan *domain.Job (buffered 100)
│   │                        • loop: ticker 30s → BuscarPendentes → enviar ao channel
│   │                        • select não-bloqueante → backpressure automático
│   ├── worker.go            • N workers: cada um consome do mesmo channel
│   │                        • rate.NewLimiter(taxa/hora / 3600, burst) por canal
│   │                        • limiter.Wait(ctx) → bloqueia goroutine, não thread
│   │                        • atomic.Int64 para métricas sem mutex
│   └── scoring.go           • calcularScore() → função determinística
│                            • ProcessarInteracao() → ponto de entrada do webhook
│
├── pkg/repository/
│   └── postgres.go          • database/sql com lib/pq driver
│                            • pool: MaxOpenConns=10, MaxIdleConns=5, ConnMaxLifetime=5min
│                            • SELECT FOR UPDATE SKIP LOCKED (anti-duplicate)
│                            • compile-time check: var _ ports.LeadRepositoryPort = (*LeadRepository)(nil)
│
└── config/
    ├── config.go            • carrega ENV vars + YAML em uma única struct Config
    └── limits.yaml          • todos os parâmetros ajustáveis sem recompilar
```

### 6.2 Concorrência em Go — Detalhes de Implementação

#### O Channel como Fila em Memória

```go
// pkg/core/dispatcher.go — linha 36
filaJobs := make(chan *domain.Job, cfg.TamanhoFila)
// cfg.TamanhoFila = 100 (do limits.yaml)

// O channel é o único ponto de comunicação entre Dispatcher e Workers.
// Não há mutex, não há lock. Go garante a segurança do channel internamente.
```

#### Workers com State Isolado

Cada Worker tem seus próprios `rate.Limiter`s. Isso significa que Worker-1 processando um email não interfere no rate limiter de LinkedIn do Worker-3:

```go
// pkg/core/worker.go — NovoWorker()
limitadores := make(map[domain.Canal]*rate.Limiter, len(LimitesDefault))
for canal, limites := range LimitesDefault {
    taxaPorSegundo := rate.Limit(float64(limites.PorHora) / 3600.0)
    limitadores[canal] = rate.NewLimiter(taxaPorSegundo, limites.BurstMax)
}
// Cada worker tem seu próprio mapa — sem compartilhamento, sem race condition
```

#### Métricas Sem Mutex — `sync/atomic`

```go
// pkg/core/worker.go
type Worker struct {
    totalEnviados atomic.Int64   // zero-cost atomic counter
    totalFalhas   atomic.Int64
    ativo         atomic.Bool
}

// Incremento seguro de múltiplas goroutines sem lock:
w.totalEnviados.Add(1)
// vs mutex { mu.Lock(); counter++; mu.Unlock() } — atômico é ~3-5x mais rápido
```

#### Graceful Shutdown — Context Propagation

```go
// cmd/server/main.go — o contexto raiz
ctx, cancelCtx := context.WithCancel(context.Background())
defer cancelCtx()

// Dispatcher recebe ctx e propaga para workers
go func() {
    dispatcher.Iniciar(ctx)  // passa ctx adiante
}()

// Ao receber SIGTERM:
cancelCtx()  // sinaliza para TODOS que usam este ctx

// No Dispatcher (pkg/core/dispatcher.go):
case <-ctx.Done():
    close(filaJobs)   // sinaliza workers: "não haverá mais jobs"
    wg.Wait()         // aguarda todos os workers terminarem jobs em andamento
    return nil

// No Worker (pkg/core/worker.go):
case job, ok := <-filaJobs:
    if !ok {
        return  // channel fechado = shutdown limpo
    }
```

#### `rate.Limiter.Wait()` — O Coração do Rate Limiting

```go
// pkg/core/worker.go — processarJob()
limitador, temLimitador := w.limitadores[job.Canal]
if temLimitador {
    if err := limitador.Wait(ctx); err != nil {
        if ctx.Err() != nil {
            return  // shutdown — não é erro real
        }
        // erro real do limiter (raro)
    }
}
```

`Wait()` bloqueia a **goroutine** (não a thread do SO) até um token estar disponível. Como goroutines são cooperativas e Go tem M:N scheduling, dezenas de workers esperando não consomem threads do SO — o runtime Go reutiliza as mesmas threads para outras goroutines prontas para rodar.

Para o LinkedIn (5/hora = 1 token a cada 720 segundos), um worker esperando pelo token consome ~0 CPU e ~8KB de memória de goroutine. Sem qualquer `time.Sleep` explícito.

### 6.3 Design Patterns Usados

| Pattern | Onde | Implementação |
|---|---|---|
| **Ports & Adapters** | Toda a arquitetura | `ports/messenger.go` define interface; `adapters/` implementa |
| **Producer-Consumer** | `core/dispatcher.go` + `core/worker.go` | Channel Go com goroutines |
| **Token Bucket** | `core/worker.go` | `golang.org/x/time/rate.NewLimiter()` |
| **Factory Method** | `adapters/*/Novo()` | Cada adapter tem função `Novo()` que valida e instancia |
| **Strategy** | `mensageiros map[Canal]MessengerPort` | Seleciona a estratégia de envio pelo tipo do canal |
| **Repository** | `pkg/repository/postgres.go` | Abstrai SQL por trás das interfaces `*RepositoryPort` |
| **Template Method** | `domain/campaign.go:RenderizarMensagem()` | Template fixo, variáveis substituídas pelos dados do lead |
| **Observer** (parcial) | `core/scoring.go:ProcessarInteracao()` | Webhook notifica o scoring, que por sua vez notifica o closer |

### 6.4 Tratamento de Erros

O projeto usa **error wrapping** consistente com `%w`:

```go
// Cada camada adiciona contexto sem perder a causa raiz
func (a *AdaptadorSES) Enviar(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
    resultado, err := a.cliente.SendEmail(ctx, entrada)
    if err != nil {
        return ports.ResultadoEnvio{
            Sucesso:      false,
            Erro:         fmt.Errorf("ses: %w", err),  // wrapping com contexto
            DeveRetentar: ehErrroTransiente(err),       // classificação do erro
        }
    }
}

// O campo DeveRetentar é a chave: distingue erros recuperáveis de permanentes
// Erros permanentes: MessageRejected, InvalidParameterValue → job vira "cancelado"
// Erros transientes: timeout, throttling → job volta para "pendente"
```

### 6.5 Rotas HTTP — `net/http` Go 1.22

```go
// cmd/server/main.go — registrarRotas()
// Go 1.22 adicionou method-based routing e path params nativos no ServeMux

mux.HandleFunc("GET /api/v1/health",                     h.health)
mux.HandleFunc("GET /api/v1/status",                     h.status)
mux.HandleFunc("POST /api/v1/leads",                     h.criarLead)
mux.HandleFunc("GET /api/v1/leads",                      h.listarLeads)
mux.HandleFunc("GET /api/v1/leads/{id}",                 h.buscarLead)
mux.HandleFunc("PUT /api/v1/leads/{id}",                 h.atualizarLead)
mux.HandleFunc("POST /api/v1/campanhas",                 h.criarCampanha)
mux.HandleFunc("GET /api/v1/campanhas",                  h.listarCampanhas)
mux.HandleFunc("GET /api/v1/campanhas/{id}",             h.buscarCampanha)
mux.HandleFunc("PUT /api/v1/campanhas/{id}/ativar",      h.ativarCampanha)
mux.HandleFunc("PUT /api/v1/campanhas/{id}/pausar",      h.pausarCampanha)
mux.HandleFunc("POST /api/v1/campanhas/{id}/despachar",  h.despacharCampanha)
mux.HandleFunc("POST /api/v1/webhooks/interacao",        h.webhookInteracao)

// Extração do path param:
id := r.PathValue("id")  // novo em Go 1.22 — sem biblioteca externa
```

**Por que não Gin/Echo/Fiber?** O `net/http` do Go 1.22 finalmente tem tudo que precisamos: method routing, path params, middleware chain. Cada framework adicionado é mais 2-5MB no binário e mais uma dependência para auditar.

### 6.6 Configuração — Hierarquia e Carregamento

```go
// config/config.go — função Carregar()
// Hierarquia: YAML (valores padrão) → ENV vars (override em runtime)

cfg.Banco.Host = envStr("DB_HOST", "localhost")  // default: localhost
// Em produção Docker: DB_HOST=postgres (nome do container)
// Em desenvolvimento: DB_HOST=localhost

// O YAML de limits controla comportamento sem recompilação:
// limits.yaml → lido na inicialização → struct LimitesConfig
// → passado para NovoWorker() que cria os rate.Limiter com os valores
```

---

## 7. Detalhamento Técnico: Infraestrutura IaC

### 7.1 Terraform — Estrutura de Providers e State

```hcl
# terraform/main.tf
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"  # constraint pessimista: 5.x.x mas não 6.x.x
    }
  }
  # State local (padrão) — arquivo terraform.tfstate na máquina do operador
  # Para equipe: descomente o backend S3:
  # backend "s3" {
  #   bucket = "lead-gen-tfstate-CONTA_ID"
  #   key    = "prod/terraform.tfstate"
  #   region = "us-east-1"
  # }
}

provider "aws" {
  region = var.aws_region  # us-east-1 (padrão Academy)

  default_tags {   # aplicado a TODOS os recursos automaticamente
    tags = {
      Projeto      = "lead-gen-motor"
      Ambiente     = var.ambiente
      Gerenciado   = "terraform"
      Proprietario = "demarchi-mei"
    }
  }
}
```

**Sobre o State local:** para um projeto solo no Academy, o state local é aceitável. Para equipes, o backend S3 com DynamoDB para state locking é o padrão. O código já tem o bloco `backend "s3"` comentado.

### 7.2 Módulo de Networking — VPC Single AZ

```
terraform/networking.tf

aws_vpc "principal"                     10.0.0.0/16
├── aws_subnet "publica"                10.0.1.0/24  (us-east-1a)
│   └── map_public_ip_on_launch = true  → EC2 recebe IP público automaticamente
├── aws_internet_gateway "igw"
├── aws_route_table "publica"           0.0.0.0/0 → igw
├── aws_route_table_association         subnet ↔ route table
└── aws_security_group "lead_gen"
    │
    ├── INGRESS: 22/tcp   ← var.seu_ip_cidr       (SSH restrito)
    ├── INGRESS: 8080/tcp ← 0.0.0.0/0             (API pública)
    ├── EGRESS:  443/tcp  → 0.0.0.0/0             (HTTPS: AWS APIs, Meta)
    ├── EGRESS:  80/tcp   → 0.0.0.0/0             (HTTP: yum updates)
    ├── EGRESS:  587/tcp  → 0.0.0.0/0             (SMTP SES STARTTLS)
    └── EGRESS:  5432/tcp → 10.0.0.0/16           (PostgreSQL interno)
```

**Decisão arquitetural crítica — sem NAT Gateway:** Um NAT Gateway custaria ~$32/mês sozinho — mais que o budget total. A solução é usar Subnet Pública com IP público na EC2. O Security Group restringe o ingress enquanto o egress para as APIs externas funciona diretamente. Os containers Docker (PostgreSQL, Chrome) ficam isolados na rede Docker bridge — nunca expostos diretamente.

### 7.3 Módulo de Compute — Spot Instance

```hcl
# terraform/compute.tf

# Busca dinâmica da AMI mais recente — não hardcoded
data "aws_ami" "amazon_linux_2023" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-*-kernel-*-x86_64"]
  }
}

resource "aws_spot_instance_request" "lead_gen" {
  ami           = data.aws_ami.amazon_linux_2023.id
  instance_type = "t3.medium"    # 2 vCPU, 4GB RAM
  spot_price    = "0.05"         # máximo $0.05/hr (mercado ~$0.012-0.020)

  # CRÍTICO: por que persistent em vez de one-time?
  # persistent: o request Spot permanece ativo após interrupção
  # AWS re-solicita automaticamente quando o preço volta ao normal
  spot_type                      = "persistent"

  # CRÍTICO: por que stop em vez de terminate?
  # stop: EC2 para (não termina) → volume EBS preservado
  # Dados do PostgreSQL sobrevivem a interrupções Spot
  instance_interruption_behavior = "stop"

  wait_for_fulfillment = true  # Terraform aguarda alocação antes de continuar

  root_block_device {
    volume_type           = "gp3"
    volume_size           = 20    # GB
    iops                  = 3000  # inclusos no gp3 sem custo extra
    throughput            = 125   # MiB/s inclusos no gp3
    delete_on_termination = true
    encrypted             = true  # AES-256 em repouso
  }
}
```

**Por que t3.medium?** 2 vCPU + 4GB RAM é suficiente para:
- Go app: ~100MB RAM com 5 workers
- PostgreSQL: ~300MB RAM com config conservadora (shared_buffers=128MB)
- Chrome headless: ~512MB RAM para automação social
- Total: ~1GB usado de 4GB disponíveis — 75% de headroom

### 7.4 Módulo IAM — Princípio do Menor Privilégio

```hcl
# terraform/iam.tf
# A policy segue o princípio: permissão mínima para a função mínima necessária

resource "aws_iam_role_policy" "lead_gen_permissoes" {
  policy = jsonencode({
    Statement = [
      # SES: apenas envio (não gerenciamento)
      { Action   = ["ses:SendEmail", "ses:SendRawEmail",
                    "ses:GetSendQuota", "ses:GetSendStatistics"]
        Resource = "*"  # SES não suporta resource-level policies
      },

      # CloudWatch Logs: apenas o log group da aplicação
      { Action   = ["logs:CreateLogGroup", "logs:CreateLogStream",
                    "logs:PutLogEvents"]
        Resource = "arn:aws:logs:${var.aws_region}:*:log-group:/lead-gen-motor*"
        # Restrito ao prefixo /lead-gen-motor — não acessa outros log groups
      },

      # CloudWatch Metrics: apenas o namespace próprio
      { Action   = ["cloudwatch:PutMetricData"]
        Resource = "*"
        Condition = { StringEquals = {"cloudwatch:namespace" = "LeadGenMotor"}}
        # Condition key restringe ao namespace específico
      },

      # SSM: apenas parâmetros com prefixo /lead-gen/
      { Action   = ["ssm:GetParameter", "ssm:GetParameters",
                    "ssm:GetParametersByPath"]
        Resource = "arn:aws:ssm:${var.aws_region}:*:parameter/lead-gen/*"
      }
    ]
  })
}
```

### 7.5 UserData — Bootstrap da Instância

O script `terraform/scripts/userdata.sh` é passado como `base64encode(templatefile(...))` e executado **uma única vez** pelo `cloud-init` na primeira boot da instância:

```bash
# Sequência de execução:
1. dnf update -y                              # ~2-3 min
2. dnf install -y docker                      # instala daemon
3. systemctl enable docker && start           # inicia e habilita no boot
4. usermod -aG docker ec2-user                # acesso sem sudo
5. instala Docker Compose v2                  # detecta versão mais recente via GitHub API
6. configura /etc/docker/daemon.json          # log rotation: 10MB × 3 arquivos
7. instala amazon-cloudwatch-agent            # envia logs para /lead-gen-motor/app
8. cria /opt/lead-gen-motor/                  # diretório da aplicação
9. cria .env com variáveis base               # interpoladas via templatefile()
10. cria lead-gen-motor.service               # systemd: docker-compose up -d no boot
11. systemctl enable lead-gen-motor           # auto-start após reinicialização
```

**Por que `templatefile()` em vez de `file()`?** O `templatefile()` permite interpolar variáveis Terraform dentro do script shell. Assim, `DB_SENHA` e `aws_region` são passados do Terraform para o `.env` da instância sem precisar de SSH posterior.

### 7.6 Kubernetes — Roadmap (Não Implementado)

> Esta seção documenta a evolução natural para Kubernetes quando o projeto crescer além de uma instância.

Quando a solução precisar escalar para múltiplas instâncias, a migração seria:

**Deployment para a aplicação Go:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: lead-gen-motor
spec:
  replicas: 2
  selector:
    matchLabels:
      app: lead-gen-motor
  template:
    spec:
      containers:
      - name: app
        image: CONTA.dkr.ecr.us-east-1.amazonaws.com/lead-gen-motor:latest
        env:
        - name: DB_SENHA
          valueFrom:
            secretKeyRef:
              name: lead-gen-secrets
              key: db-senha
        - name: DB_HOST
          value: "lead-gen-pg-service"
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "256Mi"
            cpu: "500m"
        readinessProbe:
          httpGet:
            path: /api/v1/health
            port: 8080
```

**HPA (Horizontal Pod Autoscaler):**
```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: lead-gen-motor
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
```

**Desafio de migração:** o Dispatcher/Worker atual usa channel em memória. Com múltiplos pods, cada pod teria seu próprio Dispatcher — o `SELECT FOR UPDATE SKIP LOCKED` ainda funcionaria (é a nível de banco), mas a fila em memória seria distribuída. A solução seria substituir o channel Go por uma fila real (SQS, Redis Streams).

---

## 8. Guia de Execução

### 8.1 Pré-requisitos

```bash
# Verificar versões instaladas
go version        # >= 1.22
terraform version # >= 1.5.0
docker version    # >= 24.0
docker-compose version  # >= 2.0 (ou docker compose)
aws --version     # >= 2.0
ssh-keygen --help # para gerar o par de chaves
```

### 8.2 Desenvolvimento Local — Passo a Passo

```bash
# 1. Configuração inicial
cd app
cp .env.example .env

# Editar o .env — campos obrigatórios para desenvolvimento:
# DB_SENHA=qualquer_senha_local
# APP_AMBIENTE=dev  ← logs em texto (não JSON)

# 2. Sobe as dependências (banco + chrome)
docker-compose up -d postgres chromedp

# Verificar que subiram corretamente:
docker-compose ps
# lead-gen-postgres  Up (healthy)
# lead-gen-chrome    Up (healthy)

# 3. Verifica conexão com o banco
docker-compose exec postgres psql -U leadgen leadgen -c "SELECT version();"

# 4. Roda a aplicação Go
go run ./cmd/server/
# 2024/01/15 14:30:00 INFO lead-gen-motor: iniciando versao=1.0.0
# 2024/01/15 14:30:00 INFO banco de dados conectado host=postgres banco=leadgen
# 2024/01/15 14:30:00 INFO servidor http: iniciando addr=:8080

# 5. Testa
curl http://localhost:8080/api/v1/health
# {"status":"ok","ts":"2024-01-15T14:30:00Z"}

curl http://localhost:8080/api/v1/status
# {"dispatcher":{"rodando":true,"workers_total":5,...}}

# 6. Criar um lead de teste
curl -X POST http://localhost:8080/api/v1/leads \
  -H "Content-Type: application/json" \
  -d '{
    "nome": "Maria Silva",
    "email": "maria@startup.com",
    "empresa": "Startup XYZ",
    "cargo": "CTO",
    "linkedin_url": "https://linkedin.com/in/mariasilva"
  }'
```

### 8.3 Build Docker — Verificando < 50MB

```bash
# Build completo (multi-stage)
docker build -t lead-gen-motor:latest .

# Verificar tamanho (deve ser ~18-25MB)
docker images lead-gen-motor:latest
# REPOSITORY         TAG       IMAGE ID       CREATED          SIZE
# lead-gen-motor     latest    abc123def456   10 seconds ago   21.3MB

# Inspecionar layers
docker history lead-gen-motor:latest
# IMAGE          CREATED         CREATED BY                    SIZE
# ...            10 seconds ago  ENTRYPOINT ["/lead-gen-motor"]  0B
# ...            10 seconds ago  COPY /build/lead-gen-motor    18.2MB ← binário
# ...            10 seconds ago  COPY ca-certificates.crt       204kB
# ...            10 seconds ago  COPY zoneinfo                  2.88MB
```

### 8.4 Deploy AWS — Sequência Completa

```bash
# 1. Credenciais AWS Academy
export AWS_ACCESS_KEY_ID=ASIA...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...

# 2. Variáveis Terraform
export TF_VAR_db_senha="senha_super_forte_producao_2024"
export TF_VAR_seu_ip_cidr="$(curl -s ifconfig.me/ip)/32"  # seu IP atual

# 3. Deploy completo
./scripts/deploy.sh

# OU manualmente:
cd terraform
ssh-keygen -t ed25519 -f lead-gen-key -N "" -C "lead-gen-$(date +%Y%m%d)"
terraform init
terraform plan -out=tfplan    # revisar o que será criado
terraform apply tfplan

# 4. Pós-deploy: copiar aplicação para EC2
IP=$(terraform output -raw instancia_ip_publico)
scp -i lead-gen-key -r ../app/. ec2-user@$IP:/opt/lead-gen-motor/
ssh -i lead-gen-key ec2-user@$IP \
  "cd /opt/lead-gen-motor && docker-compose up -d"

# 5. Verificar
curl http://$IP:8080/api/v1/health
```

### 8.5 Subir com pgAdmin (modo desenvolvimento)

```bash
# O pgAdmin só sobe com o profile "dev" — não consome recursos em produção
docker-compose --profile dev up -d

# Acesso: http://localhost:5050
# Login: admin@leadgen.local / senha do DB_SENHA
# Add server: host=postgres, port=5432, user=leadgen, password=$DB_SENHA
```

---

## 9. Requisitos e Qualidade

### 9.1 Requisitos Funcionais (RF)

| Código | Descrição | Status | Arquivo |
|---|---|---|---|
| RF01.1 | Cadastrar lead com validação multicanal | ✅ | `main.go:criarLead` |
| RF01.2 | Listar leads com paginação e filtros | ✅ | `main.go:listarLeads` |
| RF01.3 | Atualizar lead (status manual pelo closer) | ✅ | `main.go:atualizarLead` |
| RF01.4 | Ciclo de vida automático do lead | ✅ | `domain/lead.go` |
| RF02.1 | Criar campanha com template variável | ✅ | `domain/campaign.go` |
| RF02.2 | Ativar e pausar campanhas | ✅ | `main.go:ativar/pausar` |
| RF02.3 | Despachar mensagens para lista de leads | ✅ | `repository/postgres.go:CriarJobsParaCampanha` |
| RF03.1 | Processamento assíncrono de jobs | ✅ | `core/dispatcher.go` |
| RF03.2 | Rate limiting por canal | ✅ | `core/worker.go:LimitesDefault` |
| RF03.3 | Retry automático em falhas transientes | ✅ | `repository/postgres.go:MarcarFalhado` |
| RF03.4 | Prevenção de processamento duplicado | ✅ | `SELECT FOR UPDATE SKIP LOCKED` |
| RF03.5 | Graceful shutdown | ✅ | `main.go:signal.Notify` |
| RF04.1 | Email via Amazon SES | ✅ | `adapters/email/ses.go` |
| RF04.2 | WhatsApp via Meta Business API | ✅ | `adapters/whatsapp/webhook.go` |
| RF04.3 | LinkedIn via headless browser | ✅ | `adapters/social/chromedp.go` |
| RF04.4 | Instagram via headless browser | ✅ | `adapters/social/chromedp.go` |
| RF04.5 | Facebook via headless browser | ✅ | `adapters/social/chromedp.go` |
| RF04.6 | SMS/Notificação via SNS | ✅ | `adapters/sms/sns.go` |
| RF05.1 | Lead scoring automático (0-100) | ✅ | `core/scoring.go` |
| RF05.2 | Notificação SMS ao closer (lead quente) | ✅ | `core/scoring.go + sns.go` |
| RF05.3 | Webhook de interação para scoring | ✅ | `main.go:webhookInteracao` |

### 9.2 Requisitos Não Funcionais (RNF)

| Código | Requisito | Valor | Como Atendido |
|---|---|---|---|
| RNF01 | Tamanho da imagem Docker | < 50MB | `scratch` base → ~20MB |
| RNF02 | Startup time da aplicação | < 5s | Go init rápido, sem JVM |
| RNF03 | Throughput máximo email | 50/hora | token bucket `rate.Limiter` |
| RNF04 | Disponibilidade infra | > 95% | Spot `persistent` + auto-recovery |
| RNF05 | Shutdown sem perda de dados | 0 jobs perdidos | `WaitGroup` + `SKIP LOCKED` |
| RNF06 | Custo mensal | < $30 AWS Academy | ~$14-16 estimado |
| RNF07 | RAM da aplicação Go | < 256MB | 5 workers ~100MB medido |
| RNF08 | Logs em formato JSON prod | JSON estruturado | `log/slog` JSON handler |
| RNF09 | Sem dependências de SO no binário | true | `CGO_ENABLED=0` |
| RNF10 | Idempotência de jobs | sem duplicatas | `UNIQUE(lead_id,campanha_id,canal)` |

### 9.3 Testes — Estrutura e Execução

```bash
# Rodar todos os testes
cd app && go test ./...

# Com cobertura
go test -cover ./...

# Teste de um pacote específico com verbose
go test -v ./pkg/domain/...
go test -v ./pkg/core/...

# Race detector — detecta data races em concorrência
go test -race ./pkg/core/...

# Benchmark do scoring (útil para validar otimizações)
go test -bench=BenchmarkCalcularScore ./pkg/core/
```

**Estrutura de testes atual (a implementar conforme prioridade):**

```go
// pkg/domain/lead_test.go
func TestAdicionarPontos_ThresholdQuente(t *testing.T) {
    lead := domain.Novo("João", "joao@test.com")
    lead.Status = domain.StatusRespondeu

    ficouQuente := lead.AdicionarPontos(60)

    assert.True(t, ficouQuente)
    assert.Equal(t, domain.StatusQuente, lead.Status)
    assert.Equal(t, 60, lead.Pontuacao)
}

// pkg/core/scoring_test.go — mock do repository
type mockLeadRepo struct{ /* implementa LeadRepositoryPort */ }

func TestRecalcularScore_LeadQuente(t *testing.T) {
    repo := &mockLeadRepo{
        leads: map[string]*domain.Lead{"uuid-1": lead},
        interacoes: map[string]int{"uuid-1": 2},
    }
    srv := core.NovoServicoScoring(repo, core.RegrasDefault, slog.Default())
    ficouQuente, err := srv.RecalcularScore(ctx, lead)
    // ...
}
```

### 9.4 Comunicação Entre Serviços

```
Fluxo de comunicação interno (dentro do Docker Compose):

lead-gen-app ──────────────────► postgres:5432
             database/sql pool   (rede Docker interna lead-gen-net)

lead-gen-app ──────────────────► chromedp:9222
             WebSocket CDP       (ws://chromedp:9222)

Fluxo de comunicação externo (saída para internet via SG):

lead-gen-app ──────────────────► ses.us-east-1.amazonaws.com:443
             HTTPS AWS SDK v2    (SDK resolve endpoint automaticamente)

lead-gen-app ──────────────────► graph.facebook.com:443
             HTTP POST JSON      (Meta Business Cloud API)

lead-gen-app ──────────────────► sns.us-east-1.amazonaws.com:443
             HTTPS AWS SDK v2

lead-gen-app ──────────────────► ssm.us-east-1.amazonaws.com:443
             HTTPS AWS SDK v2    (credenciais via EC2 Instance Role)
```

---

## 10. Operacional e Custos

### 10.1 Modelagem do Banco de Dados

O schema segue estas decisões de design:

**UUIDs como Primary Key (`gen_random_uuid()`):**
- Evita exposição de sequência incremental em URLs
- Seguro para merge de dados de múltiplas origens
- PostgreSQL 16 com `pgcrypto` gera UUIDs v4 aleatórios nativamente

**ENUMs no banco:**
```sql
CREATE TYPE status_lead AS ENUM (
    'novo', 'contatado', 'respondeu', 'quente', 'convertido', 'descartado'
);
```
Garante integridade referencial sem tabela de lookup. O PostgreSQL armazena ENUMs como inteiros internamente (eficiente) mas expõe strings (legível).

**Índice parcial na tabela `jobs`:**
```sql
CREATE INDEX idx_jobs_despacho ON jobs(status, prioridade DESC, criado_em ASC)
    WHERE status = 'pendente';
```
O `WHERE status = 'pendente'` torna este um **índice parcial** — indexa apenas as linhas relevantes para o Dispatcher. Com 10.000 jobs no histórico mas apenas 50 pendentes, o índice é ~200x menor que um índice completo.

**JSONB para dados extras:**
```sql
dados_extras JSONB DEFAULT '{}'   -- tabela jobs: metadados do envio
dados        JSONB DEFAULT '{}'   -- tabela interacoes: dados do evento
```
Flexibilidade para armazenar dados não estruturados (ID externo do SES, URL clicada, etc.) sem alterar o schema.

**Trigger para `atualizado_em`:**
```sql
CREATE OR REPLACE FUNCTION fn_atualizar_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.atualizado_em = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- Aplicado em leads, campanhas, jobs — zero custo no código Go
```

**Diagrama ER:**
```
leads (1) ─────────────────────── (N) jobs ─── (1) campanhas
  │                                    │
  └── (N) interacoes          (N) mensagens
```

**Pool de conexões PostgreSQL:**
```go
// pkg/repository/postgres.go
conn.SetMaxOpenConns(10)        // máx conexões abertas simultâneas
conn.SetMaxIdleConns(5)         // conexões keepalive
conn.SetConnMaxLifetime(5min)   // recria após 5 min (evita TCP stale)
conn.SetConnMaxIdleTime(2min)   // fecha idle após 2 min
```
Com 5 Workers rodando concorrentemente e 1 Dispatcher, `MaxOpenConns=10` oferece 2x de headroom. Para aumentar workers, aumentar proporcionalmente.

### 10.2 Estimativa Detalhada de Custos AWS

| Serviço | Tipo | Config | Custo/hora | Horas/mês | Total/mês |
|---|---|---|---|---|---|
| EC2 Spot t3.medium | Compute | us-east-1, Linux | ~$0.016 | 720 | **$11.52** |
| EBS gp3 | Storage | 20GB, 3000 IOPS | $0.08/GB | — | **$1.60** |
| CloudWatch Logs | Ingestão | ~500MB/mês | $0.50/GB | — | **$0.25** |
| CloudWatch Logs | Armazenamento | 7 dias | $0.03/GB | — | **$0.01** |
| Data Transfer | Egress | ~5GB/mês | $0.09/GB | — | **$0.45** |
| SES | Envios | 6.000 emails/mês | $0.10/1000 | — | **$0.60** |
| SNS | SMS Brasil | ~30 SMS/mês | $0.02/SMS | — | **$0.60** |
| **TOTAL** | | | | | **~$15.03** |

> Spot price histórica do t3.medium em us-east-1: $0.012-0.020/hr. Usando $0.016 como média.  
> SES sandbox: gratuito até 62.000 emails/mês enviados de EC2 para emails verificados.  
> Para 30 dias de uptime 24x7, sobra ~$15 de budget — suficiente para mais 30 dias Spot.

**Economias vs On-Demand:**
- t3.medium On-Demand: $0.0416/hr × 720h = **$30.00/mês**
- t3.medium Spot: $0.016/hr × 720h = **$11.52/mês**
- **Economia: 61%** — o budget de $30 rende o dobro com Spot

### 10.3 Monitoramento e Alertas

```bash
# Logs em tempo real via CloudWatch (sem SSH)
aws logs tail /lead-gen-motor/app --follow --region us-east-1

# Filtrar apenas erros
aws logs filter-log-events \
  --log-group-name /lead-gen-motor/app \
  --filter-pattern '{ $.level = "ERROR" }'

# Métricas da instância EC2
aws cloudwatch get-metric-statistics \
  --namespace AWS/EC2 \
  --metric-name CPUUtilization \
  --dimensions Name=InstanceId,Value=$(cat .ultimo-ip-id) \
  --start-time $(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%S) \
  --end-time $(date -u +%Y-%m-%dT%H:%M:%S) \
  --period 300 \
  --statistics Average

# Status dos containers na EC2
ssh -i terraform/lead-gen-key ec2-user@$(cat .ultimo-ip) \
  "docker stats --no-stream"
```

---

## 11. Encerramento

### 11.1 Artefatos de Entrega

O repositório entrega os seguintes artefatos, organizados por categoria:

```
lead-gen-motor/                               [40 arquivos, 8.811 linhas]
│
├── CÓDIGO FONTE GO (5.035 linhas)
│   ├── cmd/server/main.go                    Entry point, DI, HTTP, shutdown
│   ├── pkg/domain/lead.go + campaign.go      Entidades de domínio puras
│   ├── pkg/ports/messenger.go + repository   Interfaces hexagonais
│   ├── pkg/adapters/ (4 subpacotes)          SES, WhatsApp, Social, SNS
│   ├── pkg/core/ (dispatcher, worker, score) Motor concorrente
│   ├── pkg/repository/postgres.go            Adapter PostgreSQL
│   └── config/config.go + limits.yaml        Configuração
│
├── INFRAESTRUTURA IaC (1.500 linhas)
│   ├── terraform/main.tf                     Provider + tags
│   ├── terraform/variables.tf                Parâmetros com validação
│   ├── terraform/networking.tf               VPC + SG least-privilege
│   ├── terraform/iam.tf                      Role mínima
│   ├── terraform/compute.tf                  Spot Instance + AMI lookup
│   ├── terraform/outputs.tf                  Saídas úteis pós-apply
│   └── terraform/scripts/userdata.sh         Bootstrap da EC2
│
├── CONTAINER (2 arquivos)
│   ├── app/Dockerfile                        Multi-stage → scratch ~20MB
│   └── app/docker-compose.yml                3 serviços + healthchecks
│
├── BANCO DE DADOS (1 arquivo)
│   └── migrations/001_schema_inicial.sql     5 tabelas, 8 índices, 3 triggers
│
├── OPERAÇÃO (2 scripts)
│   ├── scripts/deploy.sh                     One-command deploy
│   └── scripts/destroy.sh                    Backup automático + destroy
│
└── DOCUMENTAÇÃO (11 arquivos .md, 3.428 linhas)
    ├── README.md                             Porta de entrada
    ├── DOCUMENTATION.md                      Este documento
    ├── CLAUDE.md                             Instruções para agentes de IA
    └── docs/ (8 arquivos)                    Arquitetura, API, Banco, etc.
```

### 11.2 Desafios Técnicos e Soluções

**Desafio 1: Spot Instance não propaga tags para a EC2 criada**

O recurso `aws_spot_instance_request` não é a instância EC2 em si — é o *pedido* de instância. Tags aplicadas ao request não são propagadas automaticamente.

*Solução:* `provisioner "local-exec"` que executa `aws ec2 create-tags` com o `spot_instance_id` após o request ser fulfillado. Usa `|| true` para não falhar o apply se a CLI não estiver disponível.

**Desafio 2: Múltiplos Workers processando o mesmo job**

Com 5 goroutines consumindo do banco concorrentemente, sem proteção, o mesmo job poderia ser enviado 5 vezes.

*Solução:* `SELECT FOR UPDATE SKIP LOCKED` — o PostgreSQL trava as linhas selecionadas e workers concorrentes pulam linhas já travadas. O lock é liberado quando a transação termina. Sem Redis, sem Zookeeper, sem código extra.

**Desafio 3: Imagem Docker com chromedp > 50MB**

chromedp requer o Chrome para rodar, que sozinho tem ~300MB.

*Solução:* separar o Chrome em um container dedicado (`chromedp/headless-shell`) e conectar via WebSocket CDP (`ws://chromedp:9222`). O binário Go inclui apenas o cliente chromedp (~5MB no binário), não o Chrome. Imagem final da app: ~20MB.

**Desafio 4: Rate Limiting que não bloqueie o shutdown**

Um Worker esperando pelo rate limiter (ex: LinkedIn aguardando 12 minutos) bloquearia o graceful shutdown indefinidamente.

*Solução:* `rate.Limiter.Wait(ctx)` aceita context. Ao cancelar o contexto no shutdown, o `Wait()` retorna imediatamente com `ctx.Err() != nil`, e o worker encerra limpo:
```go
if err := limitador.Wait(ctx); err != nil {
    if ctx.Err() != nil {
        return  // shutdown limpo, não é erro
    }
    // erro real
}
```

**Desafio 5: `terraform destroy` perdia dados do PostgreSQL**

O volume EBS com `delete_on_termination = true` é deletado junto com o destroy.

*Solução:* o `scripts/destroy.sh` executa `pg_dump` via SSH e `docker-compose exec` antes de chamar `terraform destroy`. O backup é salvo localmente em `backups/backup_YYYYMMDD_HHMMSS.sql.gz`.

**Desafio 6: AWS Academy bloqueando IAM roles com nomes específicos**

O Academy tem políticas de SCP (Service Control Policies) que bloqueiam criação de certas roles.

*Solução:* usar o prefixo `lead-gen-` no nome da role e evitar paths especiais (`/service-role/`, `/aws-service-role/`). Se ainda bloquear, o workaround documentado é usar a `LabRole` pré-existente do Academy.

### 11.3 Backlog Técnico — Próximas Evoluções

**Sprint 1 — Segurança (Alta Prioridade)**

```
[ ] Autenticação JWT
    - Middleware auth em todas as rotas (exceto /health e /webhooks)
    - POST /auth/login → retorna JWT com claims de permissão
    - Verificação HMAC em webhooks externos

[ ] Consentimento LGPD
    - Campo consentimento_obtido + timestamp no Lead
    - Validação no Dispatcher: não cria jobs WhatsApp/SMS sem consentimento
    - API para registrar consentimento: PUT /leads/{id}/consentimento

[ ] Opt-out automático
    - Parser de respostas: "PARE", "STOP", "CANCELAR"
    - Webhook que detecta opt-out e atualiza lead para "descartado"
```

**Sprint 2 — Lambda Start/Stop**

```go
// Lambda para ligar/desligar a EC2 via HTTP — sem SSH
// Útil para: app mobile, Zapier, cronjob de horário comercial
func handler(event StartStopEvent, ctx context.Context) error {
    svc := ec2.New(session.Must(session.NewSession()))
    instanceID := os.Getenv("INSTANCE_ID")
    switch event.Action {
    case "start":
        svc.StartInstances(&ec2.StartInstancesInput{
            InstanceIds: []*string{aws.String(instanceID)},
        })
    case "stop":
        svc.StopInstances(&ec2.StopInstancesInput{
            InstanceIds: []*string{aws.String(instanceID)},
        })
    }
    return nil
}
```

**Sprint 3 — Observabilidade**

```
[ ] Métricas Prometheus no /metrics
    - jobs_processed_total{canal="email", status="enviado"}
    - lead_score_histogram
    - worker_queue_depth_gauge

[ ] Dashboard CloudWatch via Terraform
    - aws_cloudwatch_dashboard com métricas customizadas
    - Alertas SNS para: fila > 80%, taxa de falha > 20%

[ ] Distributed Tracing
    - X-Request-ID propagado por toda a stack
    - Correlação lead_id → job_id → mensagem_id nos logs
```

**Sprint 4 — Escala Horizontal**

```
[ ] Migrar channel para SQS
    - Substituir: filaJobs chan *domain.Job → SQS Queue
    - Permite múltiplos pods Go sem duplicação de jobs
    - Dead Letter Queue para jobs que excedem max_tentativas

[ ] ECS Fargate em vez de EC2 Spot
    - Elimina gerenciamento de sistema operacional
    - Scale-to-zero: paga apenas quando rodando
    - Task Definition via Terraform + ECR

[ ] GitHub Actions CI/CD
    - Push para main → go test → docker build → push ECR → deploy ECS
    - Pull Request → go test -race + go vet + staticcheck
```

---

*Documento gerado com base na análise do código real do projeto Lead Gen Motor v1.0.0.*  
*Demarchi MEI — Go + AWS Academy (FIAP) — 2024*
