# Regras de Negócio — Lead Gen Motor

> Este documento é a fonte de verdade para todas as regras de negócio.
> Antes de implementar qualquer feature nova, verifique aqui se há regra relacionada.
> Ao adicionar nova regra, **documente aqui primeiro**, depois implemente.

---

## 1. Ciclo de Vida do Lead

### 1.1 Estados e Transições

```
                    ┌─────────────────────────────────────┐
                    │                                     │
   [Criado]         ▼          [Mensagem enviada]         │
      novo ──────► contatado ──────────────────► respondeu
       │                                           │
       │           [Descarte manual]               │ [Score >= 60]
       └──────────────────────────────►  quente ◄──┘
                                          │
                                [Closer fecha venda]
                                          │
                                       convertido
```

**Regras de transição:**
- `novo → contatado`: automático quando a primeira mensagem é enviada (método `RegistrarContato()`)
- `contatado → respondeu`: automático quando interação do tipo `respondeu_*` é recebida
- `qualquer → quente`: automático quando `pontuacao >= ThresholdLeadQuente` (padrão: 60)
- `quente → convertido`: **manual** — ação do closer (via API `PUT /leads/{id}`)
- `qualquer → descartado`: **manual** — decisão humana de excluir o lead do funil

**Regra especial:** uma vez `convertido` ou `descartado`, o lead não volta a receber jobs automáticos. O Dispatcher ignora leads nesses estados.

### 1.2 Canal Preferido

O campo `canal_preferido` é atualizado automaticamente pelo Worker após cada envio bem-sucedido. Representa o último canal usado com sucesso. Pode ser usado para priorizar o canal em novas campanhas.

---

## 2. Algoritmo de Lead Scoring

### 2.1 Fórmula Atual

```
SCORE = pontos_completude + pontos_engajamento + pontos_interacoes_adicionais

Onde:
  pontos_completude = campos_preenchidos × 5           (máx: 30)
  pontos_engajamento = {
    30  se status == 'respondeu' ou 'quente'
    5   se status == 'contatado'
    0   se status == 'novo'
  }
  pontos_interacoes_adicionais = (total_interacoes - 1) × 10  (máx: 20)

SCORE = min(100, max(0, SCORE))
```

### 2.2 Configuração dos Pesos

Todos os valores são configuráveis em `config/limits.yaml`:

```yaml
pontuacao:
  threshold_lead_quente: 60
  pontos_resposta: 30
  pontos_clique: 20
  pontos_interacao_adicional: 10
  pontos_campo_preenchido: 5
```

### 2.3 Exemplos Práticos

| Situação | Cálculo | Score | Status |
|---|---|---|---|
| Lead novo, só tem nome | 0 campos × 5 | 0 | novo |
| Lead com email + empresa | 2 × 5 = 10 | 10 | novo |
| Lead completo (6 campos) | 6 × 5 = 30 | 30 | contatado |
| Lead completo + respondeu | 30 + 30 = 60 | 60 | **quente** |
| Lead completo + respondeu + 2 interações extras | 30 + 30 + 20 = 80 | 80 | quente |

### 2.4 Tipos de Interação Reconhecidos

| Tipo | Pontos | Quando Ocorre |
|---|---|---|
| `abriu_email` | 0 (apenas registra) | Pixel de tracking no email |
| `clicou_link` | +20 | Link com UTM rastreável |
| `respondeu_email` | +30 + status=respondeu | Webhook de resposta de email |
| `respondeu_whatsapp` | +30 + status=respondeu | Webhook Meta |
| `respondeu_linkedin` | +30 + status=respondeu | Detecção via chromedp |
| `conectou_linkedin` | +10 | Lead aceitou conexão |
| `seguiu_instagram` | +10 | Lead seguiu o perfil |
| `visitou_perfil` | +5 | Visita detectada |

> **Para adicionar nova interação**: edite `ProcessarInteracao()` em `pkg/core/scoring.go` e documente aqui.

### 2.5 Notificação de Lead Quente

**Quando:** score atinge ou ultrapassa `threshold_lead_quente` pela primeira vez.  
**Quem recebe:** o closer configurado em `SNS_NUMERO_CLOSER`.  
**O que a mensagem contém:** nome, empresa, score, canal preferido, email, telefone, campanha.  
**Ação esperada do closer:** ligar imediatamente para o lead.

---

## 3. Regras de Rate Limiting (Anti-Ban)

### 3.1 Limites por Canal

| Canal | Por Hora | Por Dia | Burst Máx | Intervalo Mín |
|---|---|---|---|---|
| Email | 50 | 200 | 5 | 60s |
| WhatsApp | 10 | 50 | 2 | 5 min |
| LinkedIn | 5 | 20 | 1 | 10 min |
| Instagram | 5 | 20 | 1 | 10 min |
| Facebook | 5 | 20 | 1 | 10 min |
| SMS | 20 | 100 | 3 | 3 min |

### 3.2 Mecanismo de Implementação

O rate limiting usa **token bucket** (`golang.org/x/time/rate`):

```
Por hora: N mensagens → taxa = N/3600 tokens/segundo
Burst: até B mensagens instantâneas (sem esperar)
```

Após consumir os B tokens do burst, o worker espera o tempo necessário para acumular 1 token novo antes de enviar.

**Exemplo LinkedIn:**  
`rate.NewLimiter(5.0/3600, 1)` → 1 token acumulado a cada 720 segundos (12 minutos). Burst de 1 = nunca mais de 1 mensagem instantânea.

### 3.3 Por Que Esses Limites?

- **LinkedIn**: plataforma mais agressiva na detecção. Contas com >30 conexões/dia são frequentemente restritas. Recomendação interna: máx 20/dia sendo conservador.
- **WhatsApp**: Meta limita contas novas. Contas Business verificadas têm limites maiores. Aumente gradualmente conforme seu tier sobe.
- **Email SES**: o sandbox tem limite de 200/dia para todos. Em produção (fora do sandbox), o limite é 50.000/dia mas você não quer ser marcado como spam.
- **Instagram**: similar ao LinkedIn — detecção de automação é ativa.

### 3.4 Como Aumentar Limites

**Para canais sociais**: aumente **gradualmente** ao longo de semanas. Limite sugerido de incremento: 20% por semana.

**Para SES**: solicite saída do sandbox no console AWS → Service Quotas → SES.

---

## 4. Regras de Campanha

### 4.1 Criação de Jobs

Ao chamar `POST /api/v1/campanhas/{id}/despachar`, o sistema:
1. Verifica se a campanha está com status `ativa`
2. Cria um job para cada `(lead, canal)` da lista
3. Usa `ON CONFLICT DO NOTHING` — um lead não recebe o mesmo job duas vezes na mesma campanha/canal
4. Jobs são criados com prioridade `5` (padrão)

### 4.2 Limite de Leads por Campanha

Se `max_leads > 0`, a campanha para automaticamente quando `leads_processados >= max_leads`. Útil para testes A/B ou limitar gastos.

### 4.3 Tentativas de Reenvio

| Tipo de Falha | Comportamento |
|---|---|
| Erro transiente (timeout, throttling) | Job volta para `pendente`, `tentativas++` |
| Erro permanente (email inválido, número não existe) | Job vai para `cancelado`, não reenvia |
| Máximo de tentativas atingido (`tentativas >= max_tentativas`) | Job vai para `cancelado` |

**Máximo de tentativas padrão**: 3. Configurável por job ao criar.

---

## 5. Regras de Qualidade de Dados do Lead

### 5.1 Validação na Criação

- `nome` é obrigatório e não pode ser vazio
- Pelo menos um canal de contato deve estar preenchido (email OU telefone OU linkedin OU instagram OU facebook)
- Email é normalizado para lowercase na criação

### 5.2 Deduplicação

- Se um lead com o mesmo email já existe, a API retorna erro 409 (Conflict) — **a ser implementado**: `BuscarPorEmail()` antes do `Salvar()`
- Chave única no banco: `email` (quando não nulo)
- Jobs têm constraint `UNIQUE(lead_id, campanha_id, canal)` — previne duplicação

### 5.3 Enriquecimento Futuro

> Seção reservada para regras de enriquecimento automático de dados.
> Exemplo: buscar empresa via LinkedIn URL, completar cargo via scraping, etc.

---

## 6. Regras de Segurança

### 6.1 Consentimento LGPD/GDPR

**Regra de negócio crítica:** antes de enviar mensagens, especialmente via WhatsApp e SMS, é responsabilidade do operador garantir que o lead deu consentimento para receber comunicações.

**A ser implementado:** campo `consentimento_lgpd` (boolean + timestamp) na entidade Lead, com validação no Dispatcher antes de criar jobs para WhatsApp/SMS.

### 6.2 Honrar Opt-outs

Se um lead responde com "PARE", "STOP", "CANCELAR" ou similar, seu status deve ser alterado para `descartado` e nenhum novo job deve ser criado.

**A ser implementado:** parsing de respostas nos webhooks + lógica de opt-out.

### 6.3 Uso de Automação Social

A automação via chromedp deve ser usada **exclusivamente** em contas próprias e para fins legais. O sistema inclui delays humanos e limites conservadores, mas a responsabilidade legal é do operador.

---

## 7. Regras de Notificação

### 7.1 Quando Notificar o Closer

Condições para disparar SMS via SNS:
1. Score do lead passa de < 60 para >= 60 (primeira vez)
2. Lead responde mensagem de forma explícita (webhook `respondeu_*`)
3. **Não notifica** se o lead já estava com status `quente` anteriormente

### 7.2 Conteúdo da Notificação

Campos incluídos no SMS do closer:
- Nome do lead
- Empresa
- Score atual (ex: "73/100")
- Canal preferido
- Email e telefone para contato imediato
- Nome da campanha que gerou o engajamento

---

## 8. Backlog de Regras a Implementar

> Estas regras estão definidas mas ainda não implementadas. São os **próximos incrementos** prioritários.

| Prioridade | Regra | Descrição |
|---|---|---|
| 🔴 Alta | Opt-out automático | Detectar respostas negativas e bloquear lead |
| 🔴 Alta | Deduplicação de leads | Verificar email existente antes de criar |
| 🔴 Alta | Consentimento LGPD | Campo de consentimento + validação no dispatcher |
| 🟡 Média | Lead nurturing | Re-engajar leads frios após N dias sem resposta |
| 🟡 Média | A/B testing de templates | Testar 2 versões do template por campanha |
| 🟡 Média | Agendamento de envio | Não enviar fora do horário comercial (9h-18h) |
| 🟡 Média | Score decay | Reduzir score de leads sem interação há X dias |
| 🟢 Baixa | Sequência de follow-up | Enviar série de mensagens com intervalos configurados |
| 🟢 Baixa | Segmentação por tag | Filtrar leads por tags ao criar jobs |
| 🟢 Baixa | Webhook de rastreamento | Pixel para rastrear abertura de email |
