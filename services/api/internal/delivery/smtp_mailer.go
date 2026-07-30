package delivery

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig configures a shared SMTP client for delivery and bind verification mail.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	UseTLS   bool
}

// SMTPMailer sends plain-text messages through SMTP with optional STARTTLS.
type SMTPMailer struct {
	config SMTPConfig
}

func NewSMTPMailer(config SMTPConfig) (*SMTPMailer, error) {
	config.Host = strings.TrimSpace(config.Host)
	config.From = strings.TrimSpace(config.From)
	if config.Host == "" || config.From == "" {
		return nil, fmt.Errorf("smtp host and from address are required")
	}
	if config.Port <= 0 {
		config.Port = 587
	}
	return &SMTPMailer{config: config}, nil
}

func (m *SMTPMailer) SendPlainText(ctx context.Context, to, subject, body string) error {
	return m.sendPlainText(ctx, to, subject, body, "")
}

func (m *SMTPMailer) sendPlainText(ctx context.Context, to, subject, body, messageID string) error {
	if m == nil {
		return fmt.Errorf("smtp mailer is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	to, err := validateBindEmail(to)
	if err != nil {
		return fmt.Errorf("smtp recipient is invalid")
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		messageID = fmt.Sprintf("lingow-%d", time.Now().UnixNano())
	}
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "From: %s\r\n", m.config.From)
	fmt.Fprintf(&buffer, "To: %s\r\n", to)
	fmt.Fprintf(&buffer, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buffer, "Message-ID: <%s@lingow>\r\n", messageID)
	buffer.WriteString("MIME-Version: 1.0\r\n")
	buffer.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	buffer.WriteString("\r\n")
	buffer.WriteString(body)

	addr := net.JoinHostPort(m.config.Host, strconv.Itoa(m.config.Port))
	auth := smtpAuth(m.config)
	if m.config.UseTLS {
		return sendMailSTARTTLS(ctx, addr, m.config.From, []string{to}, buffer.Bytes(), auth, m.config.Host, startTLSConfig(m.config.Host))
	}
	return smtp.SendMail(addr, auth, m.config.From, []string{to}, buffer.Bytes())
}

func smtpAuth(config SMTPConfig) smtp.Auth {
	if strings.TrimSpace(config.Username) == "" {
		return nil
	}
	return smtp.PlainAuth("", config.Username, config.Password, config.Host)
}

func startTLSConfig(serverName string) *tls.Config {
	return &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
}

func sendMailSTARTTLS(ctx context.Context, addr, from string, to []string, msg []byte, auth smtp.Auth, serverName string, tlsConfig *tls.Config) error {
	if tlsConfig == nil {
		tlsConfig = startTLSConfig(serverName)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect smtp: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, serverName)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer client.Close()

	if err := client.Hello("localhost"); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	ok, _ := client.Extension("STARTTLS")
	if !ok {
		return fmt.Errorf("smtp starttls: server does not support STARTTLS")
	}
	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt: %w", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}

// SMTPEmailBindSender sends bind verification tokens through SMTP.
type SMTPEmailBindSender struct {
	mailer *SMTPMailer
}

func NewSMTPEmailBindSender(mailer *SMTPMailer) *SMTPEmailBindSender {
	return &SMTPEmailBindSender{mailer: mailer}
}

func (s *SMTPEmailBindSender) SendBindToken(ctx context.Context, email, destinationRef, token string) error {
	if s == nil || s.mailer == nil {
		return fmt.Errorf("smtp email bind sender is not configured")
	}
	subject := "Verify your Lingow email destination"
	body := fmt.Sprintf(
		"Use this one-time token to bind destination %q:\n\n%s\n\nThe token expires in %d minutes.\n",
		destinationRef,
		token,
		int(emailBindChallengeTTL.Minutes()),
	)
	return s.mailer.SendPlainText(ctx, email, subject, body)
}
