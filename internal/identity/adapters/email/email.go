package email

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/resend/resend-go/v2"
	"github.com/wneessen/go-mail"
)

type DevLog struct{ Log *slog.Logger }

func (d DevLog) SendVerificationCode(_ context.Context, to, code string) error {
	d.Log.Info("email(verification code)", "to", to, "code", code)
	return nil
}

func (d DevLog) SendInvitation(_ context.Context, to, ws, inviter string) error {
	d.Log.Info("email(invitation)", "to", to, "workspace", ws, "inviter", inviter)
	return nil
}

type SMTP struct {
	Host string
	Port int
	User string
	Pass string
	From string
	TLS  bool
}

func (s SMTP) client() (*mail.Client, error) {
	opts := []mail.Option{
		mail.WithPort(s.Port),
		mail.WithTLSPolicy(mail.TLSMandatory),
	}
	if s.TLS {
		opts = append(opts, mail.WithSSLPort(false))
	}
	if s.User != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthPlain), mail.WithUsername(s.User), mail.WithPassword(s.Pass))
	}
	return mail.NewClient(s.Host, opts...)
}

func (s SMTP) send(to, subject, body string) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	m := mail.NewMsg()
	if err := m.From(s.From); err != nil {
		return err
	}
	if err := m.To(to); err != nil {
		return err
	}
	m.Subject(subject)
	m.SetBodyString(mail.TypeTextPlain, body)
	return c.DialAndSend(m)
}

func (s SMTP) SendVerificationCode(_ context.Context, to, code string) error {
	return s.send(to, "Your Orkome verification code",
		fmt.Sprintf("Your verification code is: %s\nIt expires in 10 minutes.\n\nIf you did not request this, ignore this email.", code))
}

func (s SMTP) SendInvitation(_ context.Context, to, ws, inviter string) error {
	return s.send(to, "You are invited to an Orkome workspace",
		fmt.Sprintf("%s invited you to join workspace %q.\nSign in with this email and accept the invitation in Orkome.", inviter, ws))
}

type Resend struct {
	Client *resend.Client
	From   string
}

func (r Resend) SendVerificationCode(_ context.Context, to, code string) error {
	_, err := r.Client.Emails.Send(&resend.SendEmailRequest{
		From:    r.From,
		To:      []string{to},
		Subject: "Your Orkome verification code",
		Text:    fmt.Sprintf("Your verification code is: %s\nIt expires in 10 minutes.", code),
	})
	return err
}

func (r Resend) SendInvitation(_ context.Context, to, ws, inviter string) error {
	_, err := r.Client.Emails.Send(&resend.SendEmailRequest{
		From:    r.From,
		To:      []string{to},
		Subject: "You are invited to an Orkome workspace",
		Text:    fmt.Sprintf("%s invited you to join workspace %q.", inviter, ws),
	})
	return err
}
