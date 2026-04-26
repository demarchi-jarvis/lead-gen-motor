package ports

import (
	"context"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
)

// FiltroLead define critérios de busca para listagem de leads.
type FiltroLead struct {
	Status    *domain.StatusLead
	Canal     *domain.Canal
	PontuacaoMin int
	Limite    int
	Offset    int
	BuscaNome string
}

// LeadRepositoryPort define as operações de persistência para leads.
type LeadRepositoryPort interface {
	Salvar(ctx context.Context, lead *domain.Lead) error
	BuscarPorID(ctx context.Context, id string) (*domain.Lead, error)
	BuscarPorEmail(ctx context.Context, email string) (*domain.Lead, error)
	Listar(ctx context.Context, filtro FiltroLead) ([]*domain.Lead, int, error)
	Atualizar(ctx context.Context, lead *domain.Lead) error
	AtualizarStatus(ctx context.Context, id string, status domain.StatusLead) error
	AtualizarPontuacao(ctx context.Context, id string, pontuacao int, status domain.StatusLead) error
	RegistrarInteracao(ctx context.Context, leadID, tipo string, canal domain.Canal, dados map[string]any) error
	ContarInteracoes(ctx context.Context, leadID string) (int, error)
}

// FiltroCampanha define critérios de busca para campanhas.
type FiltroCampanha struct {
	Status *domain.StatusCampanha
	Limite int
	Offset int
}

// CampanhaRepositoryPort define as operações de persistência para campanhas.
type CampanhaRepositoryPort interface {
	Salvar(ctx context.Context, campanha *domain.Campanha) error
	BuscarPorID(ctx context.Context, id string) (*domain.Campanha, error)
	Listar(ctx context.Context, filtro FiltroCampanha) ([]*domain.Campanha, int, error)
	Atualizar(ctx context.Context, campanha *domain.Campanha) error
	AtualizarStatus(ctx context.Context, id string, status domain.StatusCampanha) error
}

// JobRepositoryPort gerencia a fila de trabalho do Dispatcher.
type JobRepositoryPort interface {
	Salvar(ctx context.Context, job *domain.Job) error
	BuscarPendentes(ctx context.Context, limite int) ([]*domain.Job, error)
	MarcarProcessando(ctx context.Context, jobID string) error
	MarcarEnviado(ctx context.Context, jobID string, idExterno string) error
	MarcarFalhado(ctx context.Context, jobID string, erro string, podeRetentar bool) error
	CriarJobsParaCampanha(ctx context.Context, campanhaID string, leadsIDs []string, canal domain.Canal) error
}

// MensagemRepositoryPort persiste o histórico de mensagens enviadas.
type MensagemRepositoryPort interface {
	Salvar(ctx context.Context, msg *domain.Mensagem) error
	ListarPorLead(ctx context.Context, leadID string, limite, offset int) ([]*domain.Mensagem, error)
	AtualizarStatus(ctx context.Context, id string, status string) error
}
