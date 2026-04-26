// Package sms implementa notificação via Amazon SNS para leads quentes.
// Uso: alertar o sales closer quando um lead atingir pontuação >= threshold.
package sms

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/demarchi/lead-gen-motor/pkg/domain"
	"github.com/demarchi/lead-gen-motor/pkg/ports"
)

// ConfigSNS contém configurações do Amazon SNS.
type ConfigSNS struct {
	AWSRegiao       string
	NumeroDestino   string // número do closer: +5511999999999
	NomeRemetente   string // aparece como remetente no SMS (máx 11 chars)
}

// AdaptadorSNS implementa MessengerPort + NotificacaoPort via Amazon SNS.
type AdaptadorSNS struct {
	cliente       *sns.Client
	cfg           ConfigSNS
	logger        *slog.Logger
}

// Novo cria uma instância do AdaptadorSNS.
func Novo(cfg ConfigSNS, logger *slog.Logger) (*AdaptadorSNS, error) {
	if cfg.NumeroDestino == "" {
		return nil, fmt.Errorf("sns: número de destino (closer) não configurado")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.AWSRegiao),
	)
	if err != nil {
		return nil, fmt.Errorf("sns: falha ao carregar config AWS: %w", err)
	}

	return &AdaptadorSNS{
		cliente: sns.NewFromConfig(awsCfg),
		cfg:     cfg,
		logger:  logger,
	}, nil
}

// Canal retorna o identificador do canal.
func (a *AdaptadorSNS) Canal() domain.Canal {
	return domain.CanalSMS
}

// Disponivel verifica se o adapter está configurado.
func (a *AdaptadorSNS) Disponivel() bool {
	return a.cfg.NumeroDestino != ""
}

// Enviar envia SMS para o lead via SNS (usado para leads quentes diretos).
func (a *AdaptadorSNS) Enviar(ctx context.Context, msg ports.MensagemEnvio) ports.ResultadoEnvio {
	telefone := msg.Lead.Telefone
	if telefone == "" {
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("sns: lead '%s' não tem telefone", msg.Lead.Nome),
			DeveRetentar: false,
		}
	}

	corpo := msg.Corpo
	if len(corpo) > 160 {
		corpo = corpo[:157] + "..."
	}

	attrs := map[string]sns.MessageAttributeValue{}
	if a.cfg.NomeRemetente != "" {
		nome := a.cfg.NomeRemetente
		if len(nome) > 11 {
			nome = nome[:11]
		}
		attrs["AWS.SNS.SMS.SenderID"] = sns.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(nome),
		}
	}
	// Transacional = entrega garantida (mais caro que Promotional)
	attrs["AWS.SNS.SMS.SMSType"] = sns.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String("Transactional"),
	}

	resultado, err := a.cliente.Publish(ctx, &sns.PublishInput{
		PhoneNumber:       aws.String(telefone),
		Message:           aws.String(corpo),
		MessageAttributes: attrs,
	})
	if err != nil {
		a.logger.Error("sns: falha ao enviar SMS",
			"lead_id", msg.Lead.ID,
			"telefone", telefone,
			"erro", err.Error(),
		)
		return ports.ResultadoEnvio{
			Sucesso:      false,
			Erro:         fmt.Errorf("sns: %w", err),
			DeveRetentar: true,
		}
	}

	a.logger.Info("sns: SMS enviado",
		"lead_id", msg.Lead.ID,
		"message_id", aws.ToString(resultado.MessageId),
	)

	return ports.ResultadoEnvio{
		Sucesso:   true,
		IDExterno: aws.ToString(resultado.MessageId),
	}
}

// NotificarLeadQuente envia SMS para o closer quando um lead atinge pontuação alta.
// Este é o gatilho de "ação humana necessária" — o closer liga para o lead.
func (a *AdaptadorSNS) NotificarLeadQuente(ctx context.Context, lead *domain.Lead, campanha *domain.Campanha) error {
	mensagem := fmt.Sprintf(
		"🔥 LEAD QUENTE!\n"+
			"Nome: %s\n"+
			"Empresa: %s\n"+
			"Score: %d/100\n"+
			"Canal: %s\n"+
			"Campanha: %s\n"+
			"Contato: %s / %s\n"+
			"Ação: Ligar agora!",
		lead.Nome,
		lead.Empresa,
		lead.Pontuacao,
		lead.CanalPreferido,
		campanha.Nome,
		lead.Email,
		lead.Telefone,
	)

	// Limita a 480 chars (3 SMS concatenados)
	if len(mensagem) > 480 {
		mensagem = mensagem[:477] + "..."
	}

	attrs := map[string]sns.MessageAttributeValue{
		"AWS.SNS.SMS.SMSType": {
			DataType:    aws.String("String"),
			StringValue: aws.String("Transactional"),
		},
	}

	_, err := a.cliente.Publish(ctx, &sns.PublishInput{
		PhoneNumber:       aws.String(a.cfg.NumeroDestino),
		Message:           aws.String(mensagem),
		MessageAttributes: attrs,
	})
	if err != nil {
		return fmt.Errorf("sns: falha ao notificar closer sobre lead quente '%s': %w", lead.Nome, err)
	}

	a.logger.Info("sns: closer notificado sobre lead quente",
		"lead_id", lead.ID,
		"lead_nome", lead.Nome,
		"pontuacao", lead.Pontuacao,
		"destino", a.cfg.NumeroDestino,
	)

	return nil
}
