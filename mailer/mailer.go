/*
 * Copyright © 2024 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package mailer

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	netmail "net/mail"
	"net/textproto"
	"strings"

	"github.com/mailgun/mailgun-go"
	"github.com/writeas/web-core/log"
	"github.com/writefreely/writefreely/config"
	mail "github.com/xhit/go-simple-mail/v2"
)

type (
	// Mailer holds configurations for the preferred mailing provider.
	Mailer struct {
		smtp    *mail.SMTPServer
		mailGun *mailgun.MailgunImpl
	}

	// Message holds the email contents and metadata for the preferred mailing provider.
	Message struct {
		mgMsg   *mailgun.Message
		smtpMsg *SmtpMessage
	}

	SmtpMessage struct {
		from       string
		replyTo    string
		subject    string
		recipients []Recipient
		html       string
		text       string
	}

	Recipient struct {
		email string
		vars  map[string]string
	}
)

// FormatAddress returns a correctly quoted and escaped "name <address>"
// string, per RFC 5322, for use as a From, To, or Reply-To header value.
// This ensures names containing special characters (like commas) don't
// break address parsing.
func FormatAddress(name, address string) string {
	return (&netmail.Address{Name: name, Address: address}).String()
}

// Validate reports a configuration error in the instance's config.EmailCfg,
// so that it can be caught at startup rather than when the first email is
// sent. Only settings of the SMTP provider are checked, and only when SMTP is
// the provider in use.
func Validate(eCfg *config.EmailCfg) error {
	if usesMailgun(eCfg) {
		return nil
	}
	if eCfg.EnableTLS && eCfg.EnableStartTLS {
		return errors.New("smtp_enable_tls and smtp_enable_start_tls cannot both be set")
	}
	return nil
}

func usesMailgun(eCfg *config.EmailCfg) bool {
	return eCfg.Domain != "" && eCfg.MailgunPrivate != ""
}

// New creates a new Mailer from the instance's config.EmailCfg, returning an error if not properly configured.
func New(eCfg config.EmailCfg) (*Mailer, error) {
	if err := Validate(&eCfg); err != nil {
		return nil, err
	}
	m := &Mailer{}
	switch {
	case usesMailgun(&eCfg):
		m.mailGun = mailgun.NewMailgun(eCfg.Domain, eCfg.MailgunPrivate)
		if eCfg.MailgunEurope {
			m.mailGun.SetAPIBase("https://api.eu.mailgun.net/v3")
		}
	case eCfg.Username != "" && eCfg.Password != "" && eCfg.Host != "" && eCfg.Port > 0:
		m.smtp = mail.NewSMTPClient()
		m.smtp.Host = eCfg.Host
		m.smtp.Port = eCfg.Port
		m.smtp.Username = eCfg.Username
		m.smtp.Password = eCfg.Password
		if eCfg.EnableTLS || (eCfg.Port == 465 && !eCfg.EnableStartTLS) {
			// Implicit TLS: the connection is encrypted from the first byte.
			m.smtp.Encryption = mail.EncryptionSSLTLS
			m.smtp.TLSConfig = &tls.Config{ServerName: eCfg.Host, MinVersion: tls.VersionTLS12}
		} else if eCfg.EnableStartTLS {
			m.smtp.Encryption = mail.EncryptionSTARTTLS
		}
		// To allow sending multiple email
		m.smtp.KeepAlive = true
	default:
		return nil, fmt.Errorf("no email provider is configured")
	}

	return m, nil
}

// NewMessage creates a new Message from the given parameters.
func (m *Mailer) NewMessage(from, subject, text string, to ...string) (*Message, error) {
	msg := &Message{}
	if m.mailGun != nil {
		msg.mgMsg = m.mailGun.NewMessage(from, subject, text, to...)
	} else if m.smtp != nil {
		msg.smtpMsg = &SmtpMessage{
			from:       from,
			replyTo:    "",
			subject:    subject,
			recipients: make([]Recipient, 0, len(to)),
			html:       "",
			text:       text,
		}
		for _, r := range to {
			msg.smtpMsg.recipients = append(msg.smtpMsg.recipients, Recipient{r, make(map[string]string)})
		}
	}
	return msg, nil
}

// SetHTML sets the body of the message.
func (m *Message) SetHTML(html string) {
	if m.smtpMsg != nil {
		m.smtpMsg.html = html
	} else if m.mgMsg != nil {
		m.mgMsg.SetHtml(html)
	}
}

func (m *Message) SetReplyTo(replyTo string) {
	if m.smtpMsg != nil {
		m.smtpMsg.replyTo = replyTo
	} else {
		m.mgMsg.SetReplyTo(replyTo)
	}
}

// AddTag attaches a tag to the Message for providers that support it.
func (m *Message) AddTag(tag string) {
	if m.mgMsg != nil {
		m.mgMsg.AddTag(tag)
	}
}

func (m *Message) AddRecipientAndVariables(r string, vars map[string]string) error {
	if m.smtpMsg != nil {
		m.smtpMsg.recipients = append(m.smtpMsg.recipients, Recipient{r, vars})
		return nil
	} else {
		varsInterfaces := make(map[string]interface{}, len(vars))
		for k, v := range vars {
			varsInterfaces[k] = v
		}
		return m.mgMsg.AddRecipientAndVariables(r, varsInterfaces)
	}
}

// recipientError describes err, a failure to send to one recipient, without
// any text that could carry the recipient's address: an SMTP server's reply
// often repeats it, and an address that doesn't parse is quoted in full. A
// reply is given by its code. A network error or a closed connection, which
// names the server at most, is kept as it is.
func recipientError(err error) error {
	var reply *textproto.Error
	var netErr net.Error
	switch {
	case errors.As(err, &reply):
		return fmt.Errorf("the SMTP server answered %d", reply.Code)
	case errors.As(err, &netErr), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return err
	default:
		return errors.New("the message couldn't be composed or sent")
	}
}

// Send sends the given message via the preferred provider.
func (m *Mailer) Send(msg *Message) error {
	if m.smtp != nil {
		client, err := m.smtp.Connect()
		if err != nil {
			return err
		}
		emailSent := false
		for _, r := range msg.smtpMsg.recipients {
			customMsg := mail.NewMSG()
			customMsg.SetFrom(msg.smtpMsg.from)
			if msg.smtpMsg.replyTo != "" {
				customMsg.SetReplyTo(msg.smtpMsg.replyTo)
			}
			customMsg.SetSubject(msg.smtpMsg.subject)
			customMsg.AddTo(r.email)
			cText := msg.smtpMsg.text
			cHtml := msg.smtpMsg.html
			for v, value := range r.vars {
				placeHolder := fmt.Sprintf("%%recipient.%s%%", v)
				cText = strings.ReplaceAll(cText, placeHolder, value)
				cHtml = strings.ReplaceAll(cHtml, placeHolder, value)
			}
			customMsg.SetBody(mail.TextHTML, cHtml)
			customMsg.AddAlternative(mail.TextPlain, cText)
			e := customMsg.Error
			if e == nil {
				e = customMsg.Send(client)
			}
			if e == nil {
				emailSent = true
			} else {
				e = recipientError(e)
				log.Error("Unable to send email to a recipient: %v", e)
				err = e
			}
		}
		if !emailSent {
			// only send an error if no email could be sent (to avoid retry of successfully sent emails)
			return err
		}
	} else if m.mailGun != nil {
		_, _, err := m.mailGun.Send(msg.mgMsg)
		if err != nil {
			return err
		}
	}
	return nil
}
