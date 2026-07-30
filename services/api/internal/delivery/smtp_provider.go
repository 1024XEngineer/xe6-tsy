package delivery

import (
	"context"
	"fmt"
	"strings"
)

// SMTPProvider sends outbound email snapshots through SMTP.
type SMTPProvider struct {
	mailer *SMTPMailer
}

func NewSMTPProvider(mailer *SMTPMailer) (*SMTPProvider, error) {
	if mailer == nil {
		return nil, fmt.Errorf("smtp mailer is required")
	}
	return &SMTPProvider{mailer: mailer}, nil
}

func (p *SMTPProvider) Send(ctx context.Context, request SendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateFakeEmailRequest(request); err != nil {
		return err
	}
	subject := fmt.Sprintf("Lingow transcript delivery (%s)", request.Message.ID)
	body := formatEmailDeliveryBody(request)
	return p.mailer.sendPlainText(ctx, request.Destination.ProviderTarget, subject, body, request.ProviderIdempotencyKey)
}

func (p *SMTPProvider) SupportsProviderIdempotency() bool {
	// SMTP does not offer a crash-safe replay boundary across process restarts.
	return false
}

func formatEmailDeliveryBody(request SendRequest) string {
	var builder strings.Builder
	builder.WriteString("Your Lingow transcript delivery is attached below.\n\n")
	for index, turn := range request.Message.Turns {
		if index > 0 {
			builder.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&builder, "Turn: %s\n", turn.TurnID)
		if turn.SourceText != "" {
			fmt.Fprintf(&builder, "Source: %s\n", turn.SourceText)
		}
		if turn.TranslatedText != "" {
			fmt.Fprintf(&builder, "Translation: %s\n", turn.TranslatedText)
		}
	}
	builder.WriteString("\n")
	return builder.String()
}

var _ Provider = (*SMTPProvider)(nil)
var _ IdempotentProvider = (*SMTPProvider)(nil)
