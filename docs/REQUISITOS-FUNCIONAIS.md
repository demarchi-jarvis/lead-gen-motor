# Requisitos Funcionais — Lead Gen Motor

> **Documento de referência do produto.**  
> Todo incremento de código deve ser rastreável a um requisito aqui.  
> Status: ✅ Implementado | 🔄 Parcial | 🔲 Backlog | ❌ Descartado

---

## RF01 — Gestão de Leads

### RF01.1 — Cadastro de Lead ✅

**Descrição:** O sistema deve permitir cadastrar um lead com informações de contato para múltiplos canais.

**Campos suportados:**
- Nome (obrigatório)
- Email (opcional — necessário para canal Email)
- Telefone (opcional — necessário para WhatsApp/SMS)
- LinkedIn URL (opcional — necessário para canal LinkedIn)
- Instagram Handle (opcional — necessário para canal Instagram)
- Facebook URL (opcional — necessário para canal Facebook)
- Empresa (opcional — usada em templates)
- Cargo (opcional — usada em templates)
- Website (opcional)
- Localização (opcional)
- Notas internas (opcional — visível apenas para o operador)
- Tags (opcional — lista de strings para categorização)

**Regras de validação:**
- Nome não pode ser vazio
- Pelo menos um canal de contato deve estar preenchido
- Email normalizado para lowercase automaticamente
- Um lead não pode ter o mesmo email de outro lead já existente (deduplicação)

**Endpoint:** `POST /api/v1/leads`  
**Implementado em:** `cmd/server/main.go` → `criarLead()`

---

### RF01.2 — Listagem de Leads ✅

**Descrição:** Listar leads com paginação e filtros.

**Filtros disponíveis:**
- Por status (`novo`, `contatado`, `respondeu`, `quente`, `convertido`, `descartado`)
- Por canal preferido
- Por pontuação mínima
- Busca parcial por nome (case-insensitive)

**Paginação:** `limite` (máx 100) + `offset`

**Ordenação padrão:** por pontuação decrescente, depois por data de criação decrescente

**Endpoint:** `GET /api/v1/leads`

---

### RF01.3 — Atualização de Lead ✅

**Descrição:** Atualizar qualquer campo de um lead existente.

**Casos de uso principais:**
- Closer atualiza status para `convertido` após fechar venda
- Operador marca como `descartado` (sem interesse)
- Adicionar notas após ligação
- Atualizar dados após enriquecimento manual

**Endpoint:** `PUT /api/v1/leads/{id}`

---

### RF01.4 — Ciclo de Vida do Lead ✅

**Descrição:** O status do lead deve ser gerenciado automaticamente pelo sistema conforme o engajamento, com possibilidade de override manual.

**Transições automáticas:**
- `novo → contatado`: quando a primeira mensagem é enviada com sucesso
- `contatado → respondeu`: quando interação do tipo `respondeu_*` é recebida
- `qualquer → quente`: quando pontuação atinge ou ultrapassa 60 pontos

**Transições manuais (via API):**
- `quente → convertido`: ação do closer após fechamento
- `qualquer → descartado`: decisão de excluir do funil

**Regra:** leads com status `convertido` ou `descartado` não recebem novos jobs automáticos.

---

### RF01.5 — Deduplicação de Leads 🔄

**Descrição:** O sistema deve impedir cadastro duplicado pelo mesmo email.

**Implementado:** Constraint UNIQUE no banco de dados para email  
**Pendente:** Handler na API que verifica existência antes de inserir e retorna 409

---

### RF01.6 — Importação em Lote 🔲

**Descrição:** Importar lista de leads via arquivo CSV ou JSON array.

**Requisitos:**
- Suporte a CSV com cabeçalho (colunas mapeadas para campos do Lead)
- Suporte a JSON array
- Relatório de resultado: total importados / duplicatas ignoradas / erros
- Máximo 1.000 leads por request
- Validação de todos os leads antes de persistir (transação atômica)

**Endpoint:** `POST /api/v1/leads/importar`

---

## RF02 — Gestão de Campanhas

### RF02.1 — Criação de Campanha ✅

**Descrição:** Criar uma campanha de prospecção com template e canais.

**Campos:**
- Nome (obrigatório)
- Descrição (opcional)
- Canais habilitados (array — mínimo 1)
- Template do assunto (para email — suporta variáveis)
- Template do corpo (obrigatório — suporta variáveis)
- Máximo de leads (0 = sem limite)

**Variáveis de template:**
- `{{nome}}`, `{{empresa}}`, `{{cargo}}`, `{{email}}`, `{{telefone}}`

**Estado inicial:** `rascunho` (não despacha jobs)

**Endpoint:** `POST /api/v1/campanhas`

---

### RF02.2 — Ativação e Pausa de Campanha ✅

**Descrição:** Controlar o fluxo de execução de uma campanha.

**Ativar:** `rascunho → ativa` ou `pausada → ativa`  
**Pausar:** `ativa → pausada`  
**Efeito:** o Dispatcher ignora jobs de campanhas não-ativas

**Endpoints:**  
`PUT /api/v1/campanhas/{id}/ativar`  
`PUT /api/v1/campanhas/{id}/pausar`

---

### RF02.3 — Despacho de Mensagens ✅

**Descrição:** Criar jobs de envio para uma lista de leads em um canal específico.

**Regras:**
- A campanha deve estar ativa
- Um lead não recebe o mesmo `(lead, campanha, canal)` duas vezes (idempotente)
- Jobs criados com prioridade padrão 5 (escala 1-10)
- O Dispatcher processa os jobs de forma assíncrona

**Endpoint:** `POST /api/v1/campanhas/{id}/despachar`

---

### RF02.4 — Templates com Variáveis ✅

**Descrição:** O corpo e assunto das mensagens devem suportar variáveis substituídas pelos dados do lead.

**Implementado:** substituição via `strings.ReplaceAll` no método `RenderizarMensagem()`

**Exemplo:**
```
Template: "Olá {{nome}}, CEO da {{empresa}}, ..."
Resultado: "Olá João, CEO da Acme, ..."
```

---

### RF02.5 — Limite de Leads por Campanha 🔄

**Descrição:** Campanhas com `max_leads > 0` devem parar automaticamente ao atingir o limite.

**Implementado:** campo `max_leads` e `leads_processados` no banco  
**Pendente:** lógica no Dispatcher para verificar e pausar automaticamente

---

### RF02.6 — A/B Testing de Templates 🔲

**Descrição:** Testar duas versões de template em uma campanha e comparar taxas de resposta.

**Requisitos:**
- Campanha com 2 variantes (A e B)
- Distribuição configurável (ex: 50/50 ou 80/20)
- Relatório comparativo: taxa de abertura, resposta, conversão por variante

---

### RF02.7 — Agendamento de Envio 🔲

**Descrição:** Configurar horário de envio para não disparar mensagens fora do horário comercial.

**Requisitos:**
- Configurar janela de envio por campanha (ex: seg-sex, 9h-18h)
- Respeitar fuso horário configurado
- Jobs criados fora da janela ficam pendentes até a próxima abertura
- Configuração de timezone por lead (campo `localizacao`)

---

## RF03 — Motor de Envio (Dispatcher/Worker)

### RF03.1 — Processamento Assíncrono ✅

**Descrição:** Envios são processados em background sem bloquear a API.

**Implementado via:** goroutines com channel buffered (produtor-consumidor)

---

### RF03.2 — Rate Limiting por Canal ✅

**Descrição:** Cada canal tem limite máximo de mensagens por hora para evitar banimentos.

**Implementado via:** token bucket (`golang.org/x/time/rate`) por worker por canal

**Limites configuráveis em `config/limits.yaml`:**
| Canal | Padrão/hora |
|---|---|
| Email | 50 |
| WhatsApp | 10 |
| LinkedIn | 5 |
| Instagram | 5 |
| Facebook | 5 |
| SMS | 20 |

---

### RF03.3 — Retry Automático ✅

**Descrição:** Jobs com falha transiente devem ser automaticamente reagendados.

**Implementado:**
- Falha transiente (timeout, throttling): job volta para `pendente`
- Falha permanente (email inválido): job vai para `cancelado`
- Máximo de 3 tentativas por padrão

---

### RF03.4 — Prevenção de Processamento Duplicado ✅

**Descrição:** Mesmo com múltiplos workers, nenhum job deve ser processado duas vezes.

**Implementado via:** `SELECT FOR UPDATE SKIP LOCKED` no PostgreSQL

---

### RF03.5 — Graceful Shutdown ✅

**Descrição:** Ao receber sinal de encerramento, o sistema deve terminar os jobs em andamento antes de fechar.

**Implementado:** SIGTERM/SIGINT → cancela contexto → workers terminam → servidor HTTP fecha

---

### RF03.6 — Prioridade de Jobs 🔄

**Descrição:** Jobs com prioridade maior devem ser processados antes.

**Implementado:** campo `prioridade` (1-10) + ORDER BY no banco  
**Pendente:** API para criar jobs com prioridade customizada e lógica de leads quentes com prioridade 10

---

## RF04 — Canais de Comunicação

### RF04.1 — Email via Amazon SES ✅

**Descrição:** Enviar emails HTML e texto via SES com tracking.

**Requisitos atendidos:**
- Envio de email com assunto personalizado
- Corpo texto puro + HTML gerado automaticamente
- Tratamento de erros permanentes (email inválido) vs transientes
- Suporte a nome do remetente

**Pré-requisito:** Email verificado no SES + saída do sandbox para produção

---

### RF04.2 — WhatsApp via Meta Business API ✅

**Descrição:** Enviar mensagens de texto para leads via WhatsApp Business.

**Requisitos atendidos:**
- Envio via Graph API v18.0+
- Normalização automática de telefone para E.164
- Tratamento de erros Meta (número inexistente, bloqueado)

**Limitação:** usuário deve ter enviado mensagem nas últimas 24h OU usar template aprovado

---

### RF04.3 — LinkedIn via Headless Browser ✅

**Descrição:** Automatizar envio de convite de conexão com nota ou mensagem direta.

**Requisitos atendidos:**
- Login automático com credenciais configuradas
- Detecção de botão "Conectar" vs "Mensagem"
- Envio de nota personalizada no convite (máx 300 chars)
- Delays aleatórios anti-detecção
- Chrome remoto via container Docker

---

### RF04.4 — Instagram via Headless Browser ✅

**Descrição:** Automatizar envio de DM no Instagram.

**Requisitos atendidos:**
- Login automático
- Navegação para perfil do lead
- Envio de mensagem direta

---

### RF04.5 — Facebook via Headless Browser ✅

**Descrição:** Automatizar envio de mensagem no Facebook.

**Requisitos atendidos:**
- Login automático
- Navegação para perfil
- Envio de mensagem via Messenger embutido no perfil

---

### RF04.6 — SMS/Notificação via Amazon SNS ✅

**Descrição:** Enviar SMS para leads quentes e notificações para o closer.

**Casos de uso:**
1. Envio direto para o lead (canal SMS)
2. Notificação para o closer quando lead fica quente

**Tipo de SMS:** Transactional (entrega garantida)

---

### RF04.7 — Telegram 🔲

**Descrição:** Enviar mensagens via Telegram Bot API.

**Requisitos:**
- Criar Bot via @BotFather
- Campo `telegram_username` no Lead
- Limite: 30 mensagens/segundo (muito liberal vs outros canais)

---

## RF05 — Lead Scoring

### RF05.1 — Cálculo Automático de Score ✅

**Descrição:** Pontuação calculada automaticamente após cada interação.

**Algoritmo:** ver [docs/REGRAS-NEGOCIO.md](REGRAS-NEGOCIO.md#2-algoritmo-de-lead-scoring)

**Range:** 0-100 pontos

---

### RF05.2 — Notificação de Lead Quente ✅

**Descrição:** Notificar o closer via SMS quando lead atinge threshold.

**Gatilho:** score cruzar threshold (padrão 60) para cima, pela primeira vez

**Conteúdo da notificação:** nome, empresa, score, canal, email, telefone, campanha

---

### RF05.3 — Webhook de Interação ✅

**Descrição:** Endpoint para registrar engajamentos externos (pixel de email, webhooks Meta).

**Endpoint:** `POST /api/v1/webhooks/interacao`

**Tipos suportados:** ver [docs/API.md](API.md#webhooks)

---

### RF05.4 — Score Decay 🔲

**Descrição:** Reduzir score de leads sem interação após período configurável.

**Regra proposta:** -5 pontos a cada 7 dias sem interação (mínimo 0)

**Implementação:** job agendado (cron ou Lambda) que roda diariamente

---

### RF05.5 — Segmentação por Score 🔲

**Descrição:** Filtrar e segmentar leads por faixa de score para campanhas específicas.

**Exemplo:**
- Score 0-30: "leads frios" → campanha de awareness
- Score 30-59: "leads mornos" → campanha de nurturing
- Score 60+: "leads quentes" → ação direta do closer

---

## RF06 — Observabilidade

### RF06.1 — Health Check ✅

**Endpoint:** `GET /api/v1/health`

---

### RF06.2 — Status do Motor ✅

**Endpoint:** `GET /api/v1/status`  
Retorna: workers ativos, fila atual, capacidade

---

### RF06.3 — Logs Estruturados ✅

**Implementado:** `log/slog` com JSON em produção, texto em dev  
**Destino:** stdout → Docker → CloudWatch Logs  
**Retenção:** 7 dias (configurável)

---

### RF06.4 — Métricas CloudWatch 🔄

**Implementado:** permissão IAM para `cloudwatch:PutMetricData`  
**Pendente:** chamadas no código para registrar:
- Jobs processados por hora por canal
- Taxa de sucesso/falha
- Score médio dos leads
- Leads quentes por dia

---

### RF06.5 — Dashboard 🔲

**Descrição:** Endpoint ou painel visual com métricas consolidadas.

**Métricas desejadas:**
- Total de leads por status
- Jobs por canal hoje (enviados vs falhados)
- Score médio da base
- Conversão: novo → quente → convertido
- Custo estimado de mensagens (SES, SNS, API)

---

## RF07 — Infraestrutura

### RF07.1 — Deploy One-Command ✅

**Implementado:** `./scripts/deploy.sh` — Terraform + SSH + Docker Compose

---

### RF07.2 — Destroy One-Command ✅

**Implementado:** `./scripts/destroy.sh` — backup automático + Terraform destroy

---

### RF07.3 — Spot Instance com Recuperação Automática ✅

**Implementado:** `spot_type=persistent` + `instance_interruption_behavior=stop`  
Se a AWS interromper a instância Spot, ela volta automaticamente.

---

### RF07.4 — Lambda Start/Stop 🔲

**Descrição:** Função Lambda que permite ligar e desligar a instância EC2 via HTTP sem precisar de acesso SSH.

**Endpoint Lambda:** `POST /startStop {"action": "start" | "stop"}`

**Casos de uso:**
- Botão num app mobile para ligar o servidor
- Cronjob para ligar de segunda a sexta, desligar no fim de semana
- Integração com Zapier/Make para ligar via notificação

**Implementação:**
```python
import boto3

def handler(event, context):
    ec2 = boto3.client('ec2')
    action = event['action']
    instance_id = os.environ['INSTANCE_ID']
    
    if action == 'start':
        ec2.start_instances(InstanceIds=[instance_id])
    elif action == 'stop':
        ec2.stop_instances(InstanceIds=[instance_id])
    
    return {'status': 'ok', 'action': action}
```

---

### RF07.5 — Backup Automático 🔲

**Descrição:** Backup diário automático do PostgreSQL para S3.

**Implementação sugerida:**
- Cron no host EC2 (ou Lambda) que executa `pg_dump`
- Upload para S3 com lifecycle de 30 dias
- Terraform para criar o bucket + lifecycle policy

---

## RF08 — Segurança

### RF08.1 — Autenticação na API 🔲

**Descrição:** Proteger todos os endpoints com autenticação.

**Proposta:** JWT (JSON Web Token) com header `Authorization: Bearer <token>`

**Endpoints públicos (sem auth):**
- `GET /api/v1/health`
- `POST /api/v1/webhooks/interacao` (usar HMAC signature de verificação)

**Implementação:**
- `POST /api/v1/auth/login` → retorna JWT
- Middleware `auth` aplicado em todas as rotas privadas

---

### RF08.2 — Consentimento LGPD/GDPR 🔲

**Descrição:** Garantir que leads consentiram em receber comunicações.

**Campos a adicionar no Lead:**
```sql
consentimento_obtido     BOOLEAN DEFAULT FALSE,
consentimento_em         TIMESTAMPTZ,
consentimento_origem     VARCHAR(100), -- 'formulario', 'evento', 'manual'
```

**Regra:** Dispatcher não cria jobs para WhatsApp/SMS/Social sem `consentimento_obtido=true`

---

### RF08.3 — Opt-out Automático 🔲

**Descrição:** Detectar respostas de cancelamento e bloquear o lead automaticamente.

**Palavras-chave de opt-out:** "PARE", "STOP", "CANCELAR", "DESCADASTRAR", "REMOVER"

**Ação:** status → `descartado`, campo `optout_em` atualizado, nenhum job futuro criado

---

### RF08.4 — Segredos em SSM Parameter Store 🔄

**Implementado:** permissão IAM para SSM  
**Pendente:** código para buscar credenciais de `ssm:GetParameter` em vez de ENV vars

---

## Matriz de Rastreabilidade

| Requisito | Arquivo Código | Arquivo Teste | Documento |
|---|---|---|---|
| RF01.1 | main.go:criarLead | — | API.md#POST-leads |
| RF01.2 | main.go:listarLeads | — | API.md#GET-leads |
| RF01.4 | domain/lead.go | — | REGRAS-NEGOCIO.md#1 |
| RF02.1 | main.go:criarCampanha | — | API.md#POST-campanhas |
| RF02.3 | main.go:despacharCampanha | — | API.md#POST-despachar |
| RF02.4 | domain/campaign.go:RenderizarMensagem | — | BACKEND.md |
| RF03.1 | core/dispatcher.go | — | ARQUITETURA.md |
| RF03.2 | core/worker.go:LimitesDefault | — | REGRAS-NEGOCIO.md#3 |
| RF03.3 | repository/postgres.go:MarcarFalhado | — | BACKEND.md |
| RF03.4 | repository/postgres.go:BuscarPendentes | — | ARQUITETURA.md |
| RF03.5 | cmd/server/main.go:shutdown | — | BACKEND.md |
| RF04.1 | adapters/email/ses.go | — | BACKEND.md |
| RF04.2 | adapters/whatsapp/webhook.go | — | BACKEND.md |
| RF04.3-5 | adapters/social/chromedp.go | — | BACKEND.md |
| RF04.6 | adapters/sms/sns.go | — | BACKEND.md |
| RF05.1 | core/scoring.go:calcularScore | — | REGRAS-NEGOCIO.md#2 |
| RF05.2 | core/scoring.go + adapters/sms/sns.go | — | REGRAS-NEGOCIO.md#7 |
| RF05.3 | main.go:webhookInteracao | — | API.md#webhooks |
| RF06.1-2 | main.go:health + status | — | API.md |
| RF06.3 | main.go:configurarLogger | — | BACKEND.md |
| RF07.1-2 | scripts/deploy.sh + destroy.sh | — | INFRAESTRUTURA.md |
| RF07.3 | terraform/compute.tf | — | INFRAESTRUTURA.md |
