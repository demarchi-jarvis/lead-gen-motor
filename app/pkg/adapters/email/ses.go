// Package email implementa o adapter de envio via Amazon SES.
package email

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// AdaptadorSES implementa MessengerPort usando Amazon Simple Email Service.
type AdaptadorSES struct {
	cliente        *ses.Client
	emailRemetente string
	nomeRemetente  string
	logger         *slog.Logger
	disponivel     bool
}

// ConfigSES contém as configurações necessárias para o SES.
type ConfigSES struct {
	EmailRemetente string
	NomeRemetente  string
	AWSRegiao      string
}

// Novo cria e valida uma instância do AdaptadorSES.
func Novo(cfg ConfigSES, logger *slog.Logger) (*AdaptadorSES, error) {
	if cfg.EmailRemetente == "" {
		return nil, fmt.Errorf("ses: email remetente não configurado")
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.AWSRegiao),
	)
	if err != nil {
		return nil, fmt.Errorf("ses: falha ao carregar config AWS: %w", err)
	}

	adaptador := &AdaptadorSES{
		cliente:        ses.NewFromConfig(awsCfg),
		emailRemetente: cfg.EmailRemetente,
		nomeRemetente:  cfg.NomeRemetente,
		logger:         logger,
		disponivel:     true,
	}

	return adaptador, nil
}

// Canal retorna o identificador deste canal.
func (a *AdaptadorSES) Canal() domain.Canal {
	return domain.CanalEmail
}

// Disponivel verifica se o adapter está configurado corretamente.
func (a *AdaptadorSES) Disponivel() bool {
	return a.disponivel && a.emailRemetente != ""
}

// Enviar despacha um email via SES com retry automático em transientes.
func (a *AdaptadorSES) Enviar(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
	if msg.Lead.Email == "" {
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("ses: lead '%s' não tem email cadastrado", msg.Lead.Nome),
			DeveRetentar: false,
		}
	}

	remetente := fmt.Sprintf("%s <%s>", a.nomeRemetente, a.emailRemetente)
	if a.nomeRemetente == "" {
		remetente = a.emailRemetente
	}

	entrada := &ses.SendEmailInput{
		Source: aws.String(remetente),
		Destination: &types.Destination{
			ToAddresses: []string{msg.Lead.Email},
		},
		Message: &types.Message{
			Subject: &types.Content{
				Data:    aws.String(msg.Assunto),
				Charset: aws.String("UTF-8"),
			},
			Body: &types.Body{
				Text: &types.Content{
					Data:    aws.String(msg.Corpo),
					Charset: aws.String("UTF-8"),
				},
				Html: &types.Content{
					Data:    aws.String(corpoParaHTML(msg.Corpo)),
					Charset: aws.String("UTF-8"),
				},
			},
		},
	}

	resultado, err := a.cliente.SendEmail(ctx, entrada)
	if err != nil {
		deveRetentar := ehErrroTransiente(err)
		a.logger.Error("ses: falha ao enviar email",
			"lead_id", msg.Lead.ID,
			"email", msg.Lead.Email,
			"deve_retentar", deveRetentar,
			"erro", err.Error(),
		)
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("ses: %w", err),
			DeveRetentar: deveRetentar,
		}
	}

	a.logger.Info("ses: email enviado com sucesso",
		"lead_id", msg.Lead.ID,
		"email", msg.Lead.Email,
		"message_id", aws.ToString(resultado.MessageId),
	)

	return ports.ResultadoEnvio{
		Sucesso:   true,
		IDExterno: aws.ToString(resultado.MessageId),
	}
}

// corpoParaHTML envolve texto simples em HTML básico para melhor compatibilidade.
func corpoParaHTML(texto string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
<pre style="white-space: pre-wrap; font-family: inherit;">%s</pre>
</body>
</html>`, texto)
}

// ehErrroTransiente determina se o erro SES permite nova tentativa.
// Erros permanentes (ex: endereço inválido) não devem ser retentados.
func ehErrroTransiente(err error) bool {
	msg := err.Error()
	// Erros permanentes — não retentar
	errosPermanentes := []string{
		"MessageRejected",
		"InvalidParameterValue",
		"MailFromDomainNotVerified",
		"EmailAddressNotVerifiedError",
	}
	for _, ep := range errosPermanentes {
		if contains(msg, ep) {
			return false
		}
	}
	// Todos os outros são tratados como transientes (throttling, timeout, etc.)
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
