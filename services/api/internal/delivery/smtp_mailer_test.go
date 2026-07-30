package delivery

import (
	"bufio"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestNewSMTPMailerRequiresHostAndFrom(t *testing.T) {
	if _, err := NewSMTPMailer(SMTPConfig{}); err == nil {
		t.Fatal("NewSMTPMailer() error = nil, want validation error")
	}
}

func TestLogEmailBindSenderSendBindToken(t *testing.T) {
	if err := (LogEmailBindSender{}).SendBindToken(t.Context(), "user@example.test", "primary-email", "token-1"); err != nil {
		t.Fatalf("SendBindToken() error = %v", err)
	}
	if err := (LogEmailBindSender{}).SendBindToken(t.Context(), "", "primary-email", "token-1"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("SendBindToken() error = %v, want invalid argument", err)
	}
}

func TestSMTPMailerSendPlainTextUsesFakeServer(t *testing.T) {
	host, port, cleanup := startFakeSMTPServer(t)
	defer cleanup()

	mailer, err := NewSMTPMailer(SMTPConfig{
		Host:   host,
		Port:   port,
		From:   "noreply@example.test",
		UseTLS: false,
	})
	if err != nil {
		t.Fatalf("NewSMTPMailer() error = %v", err)
	}
	if err := mailer.SendPlainText(t.Context(), "user@example.test", "subject", "body"); err != nil {
		t.Fatalf("SendPlainText() error = %v", err)
	}
}

func TestSMTPMailerSendPlainTextSupportsAuth(t *testing.T) {
	host, port, cleanup := startFakeSMTPServer(t)
	defer cleanup()

	mailer, err := NewSMTPMailer(SMTPConfig{
		Host:     host,
		Port:     port,
		From:     "noreply@example.test",
		Username: "smtp-user",
		Password: "smtp-pass",
		UseTLS:   false,
	})
	if err != nil {
		t.Fatalf("NewSMTPMailer() error = %v", err)
	}
	if err := mailer.SendPlainText(t.Context(), "user@example.test", "subject", "body"); err != nil {
		t.Fatalf("SendPlainText() error = %v", err)
	}
}

func TestSMTPEmailBindSenderSendBindToken(t *testing.T) {
	host, port, cleanup := startFakeSMTPServer(t)
	defer cleanup()

	mailer, err := NewSMTPMailer(SMTPConfig{Host: host, Port: port, From: "noreply@example.test"})
	if err != nil {
		t.Fatalf("NewSMTPMailer() error = %v", err)
	}
	sender := NewSMTPEmailBindSender(mailer)
	if err := sender.SendBindToken(t.Context(), "user@example.test", "primary-email", "bind-token"); err != nil {
		t.Fatalf("SendBindToken() error = %v", err)
	}
}

func TestSMTPProviderSendDeliversSnapshot(t *testing.T) {
	host, port, cleanup := startFakeSMTPServer(t)
	defer cleanup()

	mailer, err := NewSMTPMailer(SMTPConfig{Host: host, Port: port, From: "noreply@example.test"})
	if err != nil {
		t.Fatalf("NewSMTPMailer() error = %v", err)
	}
	provider, err := NewSMTPProvider(mailer)
	if err != nil {
		t.Fatalf("NewSMTPProvider() error = %v", err)
	}
	if provider.SupportsProviderIdempotency() {
		t.Fatal("SMTPProvider must not claim crash-safe idempotency")
	}
	request := validFakeRequest()
	if err := provider.Send(t.Context(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestSMTPMailerSendPlainTextRejectsMissingRecipient(t *testing.T) {
	mailer, err := NewSMTPMailer(SMTPConfig{Host: "smtp.example.test", From: "noreply@example.test"})
	if err != nil {
		t.Fatalf("NewSMTPMailer() error = %v", err)
	}
	if err := mailer.SendPlainText(t.Context(), " ", "subject", "body"); err == nil {
		t.Fatal("SendPlainText() error = nil, want recipient error")
	}
}

func startFakeSMTPServer(t *testing.T) (host string, port int, cleanup func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	host, portString, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port = atoiOrFail(t, portString)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go handleFakeSMTPConnection(conn)
		}
	}()

	return host, port, func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}

func handleFakeSMTPConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_, _ = conn.Write([]byte("220 fake.test ESMTP\r\n"))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			_, _ = conn.Write([]byte("250-fake.test\r\n250 AUTH PLAIN\r\n"))
		case strings.HasPrefix(command, "AUTH"):
			_, _ = conn.Write([]byte("235 Authentication successful\r\n"))
		case strings.HasPrefix(command, "MAIL FROM"):
			_, _ = conn.Write([]byte("250 OK\r\n"))
		case strings.HasPrefix(command, "RCPT TO"):
			_, _ = conn.Write([]byte("250 OK\r\n"))
		case command == "DATA":
			_, _ = conn.Write([]byte("354 End data with <CR><LF>.<CR><LF>\r\n"))
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if strings.TrimSpace(dataLine) == "." {
					break
				}
			}
			_, _ = conn.Write([]byte("250 OK\r\n"))
		case strings.HasPrefix(command, "QUIT"):
			_, _ = conn.Write([]byte("221 Bye\r\n"))
			return
		default:
			_, _ = conn.Write([]byte("250 OK\r\n"))
		}
	}
}

func atoiOrFail(t *testing.T, value string) int {
	t.Helper()
	port := 0
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			t.Fatalf("invalid port %q", value)
		}
		port = port*10 + int(digit-'0')
	}
	return port
}
