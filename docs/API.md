# API REST — Lead Gen Motor

**Base URL:** `http://{ip}:8080/api/v1`  
**Content-Type:** `application/json`  
**Autenticação:** Nenhuma (a ser implementada — ver Backlog)

---

## Endpoints de Sistema

### `GET /health`

Verifica se a aplicação está viva.

**Resposta 200:**
```json
{
  "status": "ok",
  "ts": "2024-01-15T14:30:00Z"
}
```

---

### `GET /status`

Métricas internas do Dispatcher e Workers.

**Resposta 200:**
```json
{
  "dispatcher": {
    "rodando": true,
    "workers_total": 5,
    "workers_ativos": 5,
    "fila_tamanho": 12,
    "fila_capacidade": 100,
    "fila_ocupacao_pct": 12
  },
  "ts": "2024-01-15T14:30:00Z"
}
```

---

## Leads

### `POST /leads`

Cria um novo lead no sistema.

**Body:**
```json
{
  "nome": "João Silva",
  "email": "joao@acme.com",
  "telefone": "+5511999999999",
  "linkedin_url": "https://linkedin.com/in/joaosilva",
  "instagram_handle": "@joaosilva",
  "facebook_url": "https://facebook.com/joaosilva",
  "empresa": "Acme Corp",
  "cargo": "CTO",
  "website": "https://acme.com",
  "localizacao": "São Paulo, SP",
  "notas": "Encontrei no evento TechDay"
}
```

**Campos obrigatórios:** `nome` + pelo menos um de: `email`, `telefone`, `linkedin_url`, `instagram_handle`, `facebook_url`

**Resposta 201:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "nome": "João Silva",
  "email": "joao@acme.com",
  "status": "novo",
  "pontuacao": 0,
  "criado_em": "2024-01-15T14:30:00Z",
  "atualizado_em": "2024-01-15T14:30:00Z"
}
```

**Erros:**
- `400` — nome vazio ou sem canal de contato
- `500` — erro de banco de dados

---

### `GET /leads`

Lista leads com paginação e filtros.

**Query params:**
| Param | Tipo | Padrão | Descrição |
|---|---|---|---|
| `limite` | int | 20 | Máx 100 por página |
| `offset` | int | 0 | Paginação |
| `nome` | string | — | Busca parcial no nome (ILIKE) |

**Resposta 200:**
```json
{
  "dados": [
    {
      "id": "550e8400-...",
      "nome": "João Silva",
      "status": "quente",
      "pontuacao": 75,
      ...
    }
  ],
  "total": 142,
  "limite": 20,
  "offset": 0
}
```

---

### `GET /leads/{id}`

Busca um lead específico por UUID.

**Resposta 200:** objeto Lead completo  
**Resposta 404:** `{"erro": "lead não encontrado"}`

---

### `PUT /leads/{id}`

Atualiza dados de um lead. Usado principalmente pelo closer para atualizar status manualmente.

**Body:** qualquer subconjunto dos campos do Lead (os campos enviados sobrescrevem).

**Casos de uso:**
- Marcar lead como `convertido` após fechamento de venda
- Marcar lead como `descartado`
- Atualizar notas após ligação
- Adicionar tags

**Resposta 200:** objeto Lead atualizado

---

## Campanhas

### `POST /campanhas`

Cria uma nova campanha de prospecção.

**Body:**
```json
{
  "nome": "Prospecção B2B Tech Q1/2025",
  "descricao": "Foco em CTOs de startups Series A",
  "canais": ["email", "linkedin"],
  "template_assunto": "Oportunidade para {{empresa}}",
  "template_corpo": "Olá {{nome}},\n\nSou da Demarchi MEI...\n\nPodemos conversar 15 min?",
  "max_leads": 100
}
```

**Canais válidos:** `email`, `whatsapp`, `linkedin`, `instagram`, `facebook`, `sms`

**Resposta 201:** objeto Campanha com `status: "rascunho"`

---

### `GET /campanhas`

Lista campanhas com paginação.

**Query params:** `limite`, `offset`

**Resposta 200:**
```json
{
  "dados": [...],
  "total": 8
}
```

---

### `GET /campanhas/{id}`

Busca uma campanha específica.

---

### `PUT /campanhas/{id}/ativar`

Ativa uma campanha (rascunho → ativa ou pausada → ativa).

**Sem body necessário.**

**Resposta 200:** objeto Campanha com `status: "ativa"`  
**Resposta 409:** se a campanha não pode ser ativada do status atual

---

### `PUT /campanhas/{id}/pausar`

Pausa uma campanha ativa. Jobs pendentes continuam na fila mas o Dispatcher ignora jobs de campanhas não-ativas.

**Sem body necessário.**

**Resposta 200:** objeto Campanha com `status: "pausada"`

---

### `POST /campanhas/{id}/despachar`

Cria jobs de envio para uma lista de leads em um canal específico. Requer campanha ativa.

**Body:**
```json
{
  "leads_ids": [
    "550e8400-e29b-41d4-a716-446655440000",
    "660e8400-e29b-41d4-a716-446655440001"
  ],
  "canal": "email"
}
```

**Resposta 202:**
```json
{
  "mensagem": "jobs criados com sucesso",
  "campanha_id": "770e8400-...",
  "total_leads": 2,
  "canal": "email"
}
```

**Nota:** jobs são criados com `ON CONFLICT DO NOTHING` — enviar o mesmo `(lead, campanha, canal)` duas vezes não cria duplicatas.

---

## Webhooks

### `POST /webhooks/interacao`

Recebe notificações de engajamento do lead (abertura de email, clique em link, resposta).

**Integrar com:** pixel de tracking de email, webhooks do SES, webhooks Meta.

**Body:**
```json
{
  "lead_id": "550e8400-...",
  "tipo": "respondeu_email",
  "canal": "email",
  "dados": {
    "assunto_resposta": "Re: Oportunidade para Acme",
    "timestamp": "2024-01-15T15:00:00Z"
  }
}
```

**Tipos válidos:**
| Tipo | Efeito no Score |
|---|---|
| `abriu_email` | Registra interação (sem pontos) |
| `clicou_link` | +20 pontos |
| `respondeu_email` | +30 pontos + status=respondeu |
| `respondeu_whatsapp` | +30 pontos + status=respondeu |
| `respondeu_linkedin` | +30 pontos + status=respondeu |
| `conectou_linkedin` | +10 pontos |
| `seguiu_instagram` | +10 pontos |
| `visitou_perfil` | +5 pontos |

**Resposta 200:**
```json
{
  "processado": true,
  "lead_quente": true
}
```

`lead_quente: true` indica que o lead atingiu o threshold e o closer foi notificado via SMS.

---

## Exemplos de Fluxo Completo

### Fluxo 1: Prospecção por Email

```bash
# 1. Criar lead
curl -X POST http://localhost:8080/api/v1/leads \
  -H "Content-Type: application/json" \
  -d '{"nome":"Maria Costa","email":"maria@startup.com","empresa":"Startup XYZ","cargo":"CEO"}'

# 2. Criar campanha
curl -X POST http://localhost:8080/api/v1/campanhas \
  -H "Content-Type: application/json" \
  -d '{
    "nome":"Email Prospecção CEO",
    "canais":["email"],
    "template_assunto":"Parceria para {{empresa}}",
    "template_corpo":"Olá {{nome}}, CEO da {{empresa}}..."
  }'

# 3. Ativar campanha
curl -X PUT http://localhost:8080/api/v1/campanhas/{campanha_id}/ativar

# 4. Despachar para o lead
curl -X POST http://localhost:8080/api/v1/campanhas/{campanha_id}/despachar \
  -H "Content-Type: application/json" \
  -d '{"leads_ids":["{lead_id}"],"canal":"email"}'

# 5. Simular resposta (webhook)
curl -X POST http://localhost:8080/api/v1/webhooks/interacao \
  -H "Content-Type: application/json" \
  -d '{"lead_id":"{lead_id}","tipo":"respondeu_email","canal":"email"}'

# 6. Verificar que o lead ficou quente
curl http://localhost:8080/api/v1/leads/{lead_id}
# → "status": "quente", "pontuacao": 65
```

---

## Backlog de Endpoints (A Implementar)

| Método | Rota | Descrição |
|---|---|---|
| `DELETE` | `/leads/{id}` | Soft delete (marca como descartado) |
| `POST` | `/leads/importar` | Importação em lote via CSV/JSON |
| `GET` | `/leads/{id}/historico` | Histórico de mensagens e interações |
| `GET` | `/campanhas/{id}/metricas` | Taxa de envio, abertura, resposta |
| `PUT` | `/campanhas/{id}/concluir` | Finaliza campanha manualmente |
| `POST` | `/campanhas/{id}/duplicar` | Clona uma campanha |
| `GET` | `/metricas/dashboard` | Visão geral: leads/status, taxa quente |
| `POST` | `/auth/token` | Autenticação JWT (a implementar) |
