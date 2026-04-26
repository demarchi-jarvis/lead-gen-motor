package domain

import (
	"fmt"
	"strings"
	"time"
)

// StatusCampanha representa o ciclo de vida de uma campanha.
type StatusCampanha string

const (
	StatusRascunho  StatusCampanha = "rascunho"
	StatusAtiva     StatusCampanha = "ativa"
	StatusPausada   StatusCampanha = "pausada"
	StatusConcluida StatusCampanha = "concluida"
	StatusArquivada StatusCampanha = "arquivada"
)

// Campanha define uma campanha de prospecção multicanal.
type Campanha struct {
	ID               string
	Nome             string
	Descricao        string
	Status           StatusCampanha
	Canais           []Canal
	TemplateAssunto  string
	TemplateCorpo    string
	MaxLeads         int
	LeadsProcessados int
	CriadoEm        time.Time
	AtualizadoEm    time.Time
	IniciadoEm      *time.Time
	FinalizadoEm    *time.Time
}

// NovaCampanha cria uma campanha com valores padrão.
func NovaCampanha(nome, templateCorpo string, canais []Canal) *Campanha {
	agora := time.Now().UTC()
	return &Campanha{
		Nome:         strings.TrimSpace(nome),
		TemplateCorpo: templateCorpo,
		Canais:        canais,
		Status:        StatusRascunho,
		CriadoEm:     agora,
		AtualizadoEm: agora,
	}
}

// Ativar inicia a campanha. Retorna erro se transição for inválida.
func (c *Campanha) Ativar() error {
	if c.Status != StatusRascunho && c.Status != StatusPausada {
		return fmt.Errorf("campanha '%s': não é possível ativar com status '%s'", c.Nome, c.Status)
	}
	agora := time.Now().UTC()
	c.Status = StatusAtiva
	c.IniciadoEm = &agora
	c.AtualizadoEm = agora
	return nil
}

// Pausar suspende temporariamente a campanha.
func (c *Campanha) Pausar() error {
	if c.Status != StatusAtiva {
		return fmt.Errorf("campanha '%s': apenas campanhas ativas podem ser pausadas", c.Nome)
	}
	c.Status = StatusPausada
	c.AtualizadoEm = time.Now().UTC()
	return nil
}

// Concluir finaliza a campanha.
func (c *Campanha) Concluir() {
	agora := time.Now().UTC()
	c.Status = StatusConcluida
	c.FinalizadoEm = &agora
	c.AtualizadoEm = agora
}

// EstaAtiva verifica se a campanha aceita novos envios.
func (c *Campanha) EstaAtiva() bool {
	return c.Status == StatusAtiva
}

// AtingiuLimite verifica se o limite de leads foi atingido.
func (c *Campanha) AtingiuLimite() bool {
	return c.MaxLeads > 0 && c.LeadsProcessados >= c.MaxLeads
}

// TemCanal verifica se um canal está habilitado na campanha.
func (c *Campanha) TemCanal(canal Canal) bool {
	for _, c := range c.Canais {
		if c == canal {
			return true
		}
	}
	return false
}

// RenderizarMensagem substitui as variáveis do template pelos dados do lead.
func (c *Campanha) RenderizarMensagem(lead *Lead) (assunto, corpo string) {
	substituicoes := map[string]string{
		"{{nome}}":     lead.Nome,
		"{{empresa}}":  lead.Empresa,
		"{{cargo}}":    lead.Cargo,
		"{{email}}":    lead.Email,
		"{{telefone}}": lead.Telefone,
	}

	assunto = c.TemplateAssunto
	corpo = c.TemplateCorpo
	for chave, valor := range substituicoes {
		assunto = strings.ReplaceAll(assunto, chave, valor)
		corpo = strings.ReplaceAll(corpo, chave, valor)
	}
	return assunto, corpo
}

// Job representa uma tarefa de envio na fila do Dispatcher.
type Job struct {
	ID             string
	Lead           *Lead
	Campanha       *Campanha
	Canal          Canal
	Prioridade     int
	Tentativas     int
	MaxTentativas  int
	Erro           string
	CriadoEm       time.Time
	ProcessadoEm   *time.Time
}

// PodeRetentar verifica se o job pode ser reprocessado após falha.
func (j *Job) PodeRetentar() bool {
	return j.Tentativas < j.MaxTentativas
}

// Mensagem representa um envio individual rastreável.
type Mensagem struct {
	ID         string
	JobID      string
	LeadID     string
	CampanhaID string
	Canal      Canal
	Assunto    string
	Corpo      string
	Status     string
	CriadoEm  time.Time
	EnviadoEm  *time.Time
}
