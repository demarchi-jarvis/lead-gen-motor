package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// RegrasScore define os pesos de pontuação do algoritmo.
type RegrasScore struct {
	PontosResposta           int
	PontosClique             int
	PontosInteracaoAdicional int
	PontosCampoPreenchido    int
	ThresholdLeadQuente      int
}

// RegrasDefault são as regras padrão de pontuação.
var RegrasDefault = RegrasScore{
	PontosResposta:           domain.PontuacaoResposta,
	PontosClique:             domain.PontuacaoCliqueLink,
	PontosInteracaoAdicional: domain.PontuacaoInteracaoAdicional,
	PontosCampoPreenchido:    domain.PontuacaoCampoPreenchido,
	ThresholdLeadQuente:      domain.ThresholdLeadQuente,
}

// ServicoScoring calcula e atualiza a pontuação dos leads.
type ServicoScoring struct {
	leadRepo ports.LeadRepositoryPort
	regras   RegrasScore
	logger   *slog.Logger
}

// NovoServicoScoring cria o serviço de scoring com as regras configuradas.
func NovoServicoScoring(leadRepo ports.LeadRepositoryPort, regras RegrasScore, logger *slog.Logger) *ServicoScoring {
	return &ServicoScoring{
		leadRepo: leadRepo,
		regras:   regras,
		logger:   logger,
	}
}

// RecalcularScore recalcula a pontuação de um lead com base nas suas interações.
// Retorna true se o lead passou a ser "quente" neste recálculo.
func (s *ServicoScoring) RecalcularScore(ctx context.Context, lead *domain.Lead) (bool, error) {
	totalInteracoes, err := s.leadRepo.ContarInteracoes(ctx, lead.ID)
	if err != nil {
		return false, fmt.Errorf("scoring: falha ao contar interações do lead '%s': %w", lead.ID, err)
	}

	pontuacaoAnterior := lead.Pontuacao
	novaScore := s.calcularScore(lead, totalInteracoes)

	ficouQuente := novaScore >= s.regras.ThresholdLeadQuente &&
		pontuacaoAnterior < s.regras.ThresholdLeadQuente

	novoStatus := lead.Status
	if ficouQuente {
		novoStatus = domain.StatusQuente
		s.logger.Info("scoring: lead ficou QUENTE",
			"lead_id", lead.ID,
			"lead_nome", lead.Nome,
			"pontuacao_anterior", pontuacaoAnterior,
			"pontuacao_nova", novaScore,
		)
	}

	// Persiste a nova pontuação
	if err := s.leadRepo.AtualizarPontuacao(ctx, lead.ID, novaScore, novoStatus); err != nil {
		return false, fmt.Errorf("scoring: falha ao persistir score do lead '%s': %w", lead.ID, err)
	}

	lead.Pontuacao = novaScore
	lead.Status = novoStatus

	return ficouQuente, nil
}

// calcularScore aplica as regras de pontuação ao lead.
// Algoritmo simples e determinístico — fácil de auditar e ajustar.
func (s *ServicoScoring) calcularScore(lead *domain.Lead, totalInteracoes int) int {
	score := 0

	// Pontos por completude do perfil (máximo: 6 campos * 5 = 30 pontos)
	score += lead.CamposPreenchidos() * s.regras.PontosCampoPreenchido

	// Pontos por engajamento com base no status atual
	switch lead.Status {
	case domain.StatusRespondeu, domain.StatusQuente:
		score += s.regras.PontosResposta
	case domain.StatusContatado:
		// Lead foi contatado mas não respondeu — pequena pontuação
		score += 5
	}

	// Pontos por interações adicionais (máximo: 20 pontos extras)
	if totalInteracoes > 1 {
		pontosExtra := (totalInteracoes - 1) * s.regras.PontosInteracaoAdicional
		const maxPontosExtra = 20
		if pontosExtra > maxPontosExtra {
			pontosExtra = maxPontosExtra
		}
		score += pontosExtra
	}

	// Garante range 0-100
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

// ProcessarInteracao registra uma interação do lead e recalcula seu score.
// É o ponto de entrada para webhooks de "abriu email", "clicou link", etc.
func (s *ServicoScoring) ProcessarInteracao(
	ctx context.Context,
	leadID string,
	tipoInteracao string,
	canal domain.Canal,
	dados map[string]any,
) (bool, error) {
	if err := s.leadRepo.RegistrarInteracao(ctx, leadID, tipoInteracao, canal, dados); err != nil {
		return false, fmt.Errorf("scoring: falha ao registrar interação: %w", err)
	}

	lead, err := s.leadRepo.BuscarPorID(ctx, leadID)
	if err != nil {
		return false, fmt.Errorf("scoring: lead '%s' não encontrado: %w", leadID, err)
	}

	// "respondeu" eleva o status imediatamente antes de recalcular
	if tipoInteracao == "respondeu_email" || tipoInteracao == "respondeu_whatsapp" ||
		tipoInteracao == "respondeu_linkedin" {
		lead.RegistrarResposta()
		if err := s.leadRepo.AtualizarStatus(ctx, lead.ID, lead.Status); err != nil {
			s.logger.Warn("scoring: falha ao atualizar status de resposta",
				"lead_id", leadID,
				"erro", err.Error(),
			)
		}
	}

	return s.RecalcularScore(ctx, lead)
}
