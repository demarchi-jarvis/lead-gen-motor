// Package ports define as interfaces (portas) da arquitetura hexagonal.
// Os adapters externos implementam estas interfaces.
// O core da aplicação depende apenas destas abstrações.
package ports

import (
	"context"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
)

// MensagemEnvio encapsula todos os dados necessários para enviar uma mensagem.
type MensagemEnvio struct {
	Lead    *domain.Lead
	Assunto string
	Corpo   string
	Canal   domain.Canal
	// DadosExtras permite adapters passarem metadados específicos do canal
	DadosExtras map[string]string
}

// ResultadoEnvio contém o retorno de uma tentativa de envio.
type ResultadoEnvio struct {
	Sucesso       bool
	IDExterno     string // ID da mensagem no provider (ex: MessageId do SES)
	Erro          error
	DeveRetentar  bool // falso para erros permanentes (email inválido, bloqueado)
}

// MessengerPort é a interface que todos os adaptadores de canal devem implementar.
// Permite trocar providers sem alterar o core da aplicação.
type MessengerPort interface {
	// Enviar despacha uma mensagem para o lead via o canal específico.
	Enviar(ctx context.Context, msg MensagemEnvio) ResultadoEnvio

	// Canal retorna o identificador do canal implementado.
	Canal() domain.Canal

	// Disponivel verifica se o adapter está configurado e operacional.
	Disponivel() bool
}

// NotificacaoPort é usado para alertas internos (ex: lead quente → SMS para o closer).
type NotificacaoPort interface {
	NotificarLeadQuente(ctx context.Context, lead *domain.Lead, campanha *domain.Campanha) error
}
