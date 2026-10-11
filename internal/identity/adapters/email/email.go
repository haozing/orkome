// Package email adapts orkome's outbound mail channels (Resend, SMTP, dev
// log) to the framework identity app.EmailSender port: login codes,
// workspace invitations, password-reset links and email-verification links.
package email

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/resend/resend-go/v2"
	"github.com/wneessen/go-mail"

	fwapp "github.com/haozing/ploykit/identity/app"
)

// compile-time port checks: every backend satisfies the framework sender.
var (
	_ fwapp.EmailSender = DevLog{}
	_ fwapp.EmailSender = SMTP{}
	_ fwapp.EmailSender = Resend{}
)

// DevLog logs instead of sending — the zero-config local channel.
type DevLog struct{ Log *slog.Logger }

func (d DevLog) SendLoginCode(_ context.Context, to, code string) error {
	d.Log.Info("email(login code)", "to", to, "code", code)
	return nil
}

func (d DevLog) SendInvite(_ context.Context, to, workspace, invitationID string) error {
	d.Log.Info("email(invitation)", "to", to, "workspace", workspace, "invitation_id", invitationID)
	return nil
}

func (d DevLog) SendPasswordReset(_ context.Context, to, link string) error {
	d.Log.Info("email(password reset)", "to", to, "link", link)
	return nil
}

func (d DevLog) SendEmailVerification(_ context.Context, to, link string) error {
	d.Log.Info("email(email verification)", "to", to, "link", link)
	return nil
}

// SMTP sends through a plain SMTP relay.
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

func (s SMTP) SendLoginCode(_ context.Context, to, code string) error {
	return s.send(to, "Your Orkome verification code",
		fmt.Sprintf("Your verification code is: %s\nIt expires in 10 minutes.\n\nIf you did not request this, ignore this email.", code))
}

func (s SMTP) SendInvite(_ context.Context, to, workspace, invitationID string) error {
	return s.send(to, "You are invited to an Orkome workspace",
		fmt.Sprintf("You have been invited to join workspace %q.\nSign in with this email and accept the invitation in Orkome (invitation %s).", workspace, invitationID))
}

func (s SMTP) SendPasswordReset(_ context.Context, to, link string) error {
	return s.send(to, "Reset your Orkome password",
		fmt.Sprintf("Reset your password with this link (valid for a short time):\n%s\n\nIf you did not request this, ignore this email.", link))
}

func (s SMTP) SendEmailVerification(_ context.Context, to, link string) error {
	return s.send(to, "Verify your Orkome email",
		fmt.Sprintf("Verify your email address with this link:\n%s", link))
}

// Resend sends through the Resend API.
type Resend struct {
	Client *resend.Client
	From   string
}

func (r Resend) SendLoginCode(_ context.Context, to, code string) error {
	_, err := r.Client.Emails.Send(&resend.SendEmailRequest{
		From:    r.From,
		To:      []string{to},
		Subject: "Your Orkome verification code",
		Text:    fmt.Sprintf("Your verification code is: %s\nIt expires in 10 minutes.", code),
	})
	return err
}

func (r Resend) SendInvite(_ context.Context, to, workspace, invitationID string) error {
	_, err := r.Client.Emails.Send(&resend.SendEmailRequest{
		From:    r.From,
		To:      []string{to},
		Subject: "You are invited to an Orkome workspace",
		Text:    fmt.Sprintf("You have been invited to join workspace %q (invitation %s).", workspace, invitationID),
	})
	return err
}

func (r Resend) SendPasswordReset(_ context.Context, to, link string) error {
	_, err := r.Client.Emails.Send(&resend.SendEmailRequest{
		From:    r.From,
		To:      []string{to},
		Subject: "Reset your Orkome password",
		Text:    fmt.Sprintf("Reset your password with this link (valid for a short time):\n%s", link),
	})
	return err
}

func (r Resend) SendEmailVerification(_ context.Context, to, link string) error {
	_, err := r.Client.Emails.Send(&resend.SendEmailRequest{
		From:    r.From,
		To:      []string{to},
		Subject: "Verify your Orkome email",
		Text:    fmt.Sprintf("Verify your email address with this link:\n%s", link),
	})
	return err
}
