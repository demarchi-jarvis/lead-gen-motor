// Package domain contém as entidades e regras de negócio centrais.
// Não tem dependência de frameworks externos — puro Go.
package domain

import (
	"strings"
	"time"
)

// Canal representa o meio de comunicação com o lead.
type Canal string

const (
	CanalEmail     Canal = "email"
	CanalWhatsApp  Canal = "whatsapp"
	CanalLinkedIn  Canal = "linkedin"
	CanalInstagram Canal = "instagram"
	CanalFacebook  Canal = "facebook"
	CanalSMS       Canal = "sms"
)

// StatusLead representa o estado do lead no funil de prospecção.
type StatusLead string

const (
	StatusNovo       StatusLead = "novo"
	StatusContatado  StatusLead = "contatado"
	StatusRespondeu  StatusLead = "respondeu"
	StatusQuente     StatusLead = "quente"
	StatusConvertido StatusLead = "convertido"
	StatusDescartado StatusLead = "descartado"
)

// Constantes de pontuação para o algoritmo de lead scoring.
const (
	PontuacaoResposta            = 30
	PontuacaoCliqueLink          = 20
	PontuacaoInteracaoAdicional  = 10
	PontuacaoCampoPreenchido     = 5
	ThresholdLeadQuente          = 60
)

// Lead é a entidade central do sistema.
// Representa um potencial cliente em qualquer canal de prospecção.
type Lead struct {
	ID              string
	Nome            string
	Email           string
	Telefone        string
	LinkedInURL     string
	InstagramHandle string
	FacebookURL     string
	Empresa         string
	Cargo           string
	Website         string
	Localizacao     string

	Status          StatusLead
	Pontuacao       int
	CanalPreferido  Canal
	Notas           string
	Tags            []string

	CriadoEm        time.Time
	AtualizadoEm    time.Time
	UltimoContatoEm *time.Time
}

// Novo cria um Lead com valores padrão.
func Novo(nome, email string) *Lead {
	agora := time.Now().UTC()
	return &Lead{
		Nome:      strings.TrimSpace(nome),
		Email:     strings.TrimSpace(strings.ToLower(email)),
		Status:    StatusNovo,
		Pontuacao: 0,
		Tags:      []string{},
		CriadoEm: agora,
		AtualizadoEm: agora,
	}
}

// EhValido verifica se o lead tem pelo menos um canal de contato.
func (l *Lead) EhValido() bool {
	return l.Nome != "" && (
		l.Email != "" ||
		l.Telefone != "" ||
		l.LinkedInURL != "" ||
		l.InstagramHandle != "" ||
		l.FacebookURL != "")
}

// CanaisDisponiveis retorna os canais que têm dados suficientes para envio.
func (l *Lead) CanaisDisponiveis() []Canal {
	canais := make([]Canal, 0, 4)
	if l.Email != "" {
		canais = append(canais, CanalEmail)
	}
	if l.Telefone != "" {
		canais = append(canais, CanalWhatsApp)
		canais = append(canais, CanalSMS)
	}
	if l.LinkedInURL != "" {
		canais = append(canais, CanalLinkedIn)
	}
	if l.InstagramHandle != "" {
		canais = append(canais, CanalInstagram)
	}
	if l.FacebookURL != "" {
		canais = append(canais, CanalFacebook)
	}
	return canais
}

// AdicionarPontos incrementa a pontuação e atualiza status se necessário.
// Retorna true se o lead passou a ser "quente" nesta atualização.
func (l *Lead) AdicionarPontos(pontos int) bool {
	eraQuenteAntes := l.Pontuacao >= ThresholdLeadQuente
	l.Pontuacao = min(100, l.Pontuacao+pontos)
	l.AtualizadoEm = time.Now().UTC()

	if l.Pontuacao >= ThresholdLeadQuente && !eraQuenteAntes {
		l.Status = StatusQuente
		return true // novo lead quente — disparar alerta para o closer
	}
	return false
}

// RegistrarContato atualiza o timestamp do último contato e o status.
func (l *Lead) RegistrarContato() {
	agora := time.Now().UTC()
	l.UltimoContatoEm = &agora
	l.AtualizadoEm = agora
	if l.Status == StatusNovo {
		l.Status = StatusContatado
	}
}

// RegistrarResposta marca o lead como quem respondeu e adiciona pontuação.
func (l *Lead) RegistrarResposta() bool {
	if l.Status == StatusContatado || l.Status == StatusNovo {
		l.Status = StatusRespondeu
	}
	return l.AdicionarPontos(PontuacaoResposta)
}

// CamposPreenchidos conta quantos campos de perfil estão preenchidos.
// Usado para calcular pontuação de completude.
func (l *Lead) CamposPreenchidos() int {
	contador := 0
	campos := []string{l.Email, l.Telefone, l.LinkedInURL, l.InstagramHandle, l.Empresa, l.Cargo}
	for _, campo := range campos {
		if campo != "" {
			contador++
		}
	}
	return contador
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
