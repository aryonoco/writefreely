/*
 * Copyright © 2026 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package mailer

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	netmail "net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/writefreely/writefreely/config"

	mail "github.com/xhit/go-simple-mail/v2"
)

func TestFormatAddress(t *testing.T) {
	cases := []struct {
		name, address string
	}{
		{"Le blog", "le-blog@example.com"},
		{"A, B, C et D", "sender@example.net"},
		{"", "noreply@example.com"},
	}
	for _, c := range cases {
		got := FormatAddress(c.name, c.address)
		addr, err := netmail.ParseAddress(got)
		if err != nil {
			t.Fatalf("FormatAddress(%q, %q) = %q, which failed to parse: %v", c.name, c.address, got, err)
		}
		if addr.Address != c.address {
			t.Errorf("FormatAddress(%q, %q) = %q, parsed address = %q, want %q", c.name, c.address, got, addr.Address, c.address)
		}
	}
}

func TestNewMessageDoesNotAddPhantomRecipients(t *testing.T) {
	m := &Mailer{smtp: mail.NewSMTPClient()}
	msg, err := m.NewMessage("Test <from@example.com>", "Subject", "body", "<to@example.com>")
	if err != nil {
		t.Fatalf("NewMessage returned error: %v", err)
	}
	if len(msg.smtpMsg.recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d: %+v", len(msg.smtpMsg.recipients), msg.smtpMsg.recipients)
	}
	if msg.smtpMsg.recipients[0].email != "<to@example.com>" {
		t.Errorf("unexpected recipient: %+v", msg.smtpMsg.recipients[0])
	}
}

func smtpCfg(port int) config.EmailCfg {
	return config.EmailCfg{Host: "127.0.0.1", Port: port, Username: "user", Password: "pass"}
}

func TestNewRefusesBothTLSModes(t *testing.T) {
	t.Parallel()
	cfg := smtpCfg(587)
	cfg.EnableTLS = true
	cfg.EnableStartTLS = true
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected an error when both TLS modes are set")
	}
	for _, key := range []string{"smtp_enable_tls", "smtp_enable_start_tls"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}
}

func TestNewSelectsImplicitTLS(t *testing.T) {
	t.Parallel()
	cfg := smtpCfg(2525)
	cfg.EnableTLS = true
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m.smtp.Encryption != mail.EncryptionSSLTLS {
		t.Errorf("Encryption = %v, want SSL/TLS", m.smtp.Encryption)
	}
}

func TestNewSelectsImplicitTLSOnPort465(t *testing.T) {
	t.Parallel()
	m, err := New(smtpCfg(465))
	if err != nil {
		t.Fatal(err)
	}
	if m.smtp.Encryption != mail.EncryptionSSLTLS {
		t.Errorf("port 465: Encryption = %v, want SSL/TLS", m.smtp.Encryption)
	}

	cfg := smtpCfg(587)
	cfg.EnableStartTLS = true
	m, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m.smtp.Encryption != mail.EncryptionSTARTTLS {
		t.Errorf("port 587: Encryption = %v, want STARTTLS", m.smtp.Encryption)
	}
}

// testSMTPServer is a minimal SMTP server speaking over implicit TLS.
type testSMTPServer struct {
	ln   net.Listener
	mu   sync.Mutex
	seen []string
}

func (s *testSMTPServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func (s *testSMTPServer) sawAuth() bool {
	for _, c := range s.commands() {
		if strings.HasPrefix(c, "AUTH") {
			return true
		}
	}
	return false
}

func (s *testSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *testSMTPServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	reply("220 localhost ESMTP")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				reply("250 OK")
			}
			continue
		}
		s.mu.Lock()
		s.seen = append(s.seen, line)
		s.mu.Unlock()
		switch verb := strings.ToUpper(strings.Fields(line + " x")[0]); verb {
		case "EHLO", "HELO":
			reply("250-localhost")
			reply("250 AUTH PLAIN")
		case "AUTH":
			reply("235 OK")
		case "DATA":
			inData = true
			reply("354 go ahead")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 OK")
		}
	}
}

// newTestSMTPServer starts a TLS SMTP server on 127.0.0.1 whose certificate is
// issued by a CA created here, and returns it with a pool trusting that CA.
func newTestSMTPServer(t *testing.T) (*testSMTPServer, *x509.CertPool, int) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	srv := &testSMTPServer{ln: ln}
	go srv.serve()
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return srv, pool, port
}

func sendTestMessage(t *testing.T, m *Mailer) error {
	t.Helper()
	msg, err := m.NewMessage("from@example.com", "Subject", "body", "to@example.com")
	if err != nil {
		t.Fatal(err)
	}
	msg.SetHTML("<p>body</p>")
	return m.Send(msg)
}

func TestImplicitTLSSendVerifiesCertificate(t *testing.T) {
	t.Parallel()
	t.Run("trusted CA", func(t *testing.T) {
		t.Parallel()
		srv, pool, port := newTestSMTPServer(t)
		cfg := smtpCfg(port)
		cfg.EnableTLS = true
		m, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m.smtp.TLSConfig.RootCAs = pool
		if err := sendTestMessage(t, m); err != nil {
			t.Fatalf("send failed: %v", err)
		}
		if !srv.sawAuth() {
			t.Errorf("server did not see AUTH; commands: %q", srv.commands())
		}
	})

	t.Run("untrusted certificate", func(t *testing.T) {
		t.Parallel()
		srv, _, port := newTestSMTPServer(t)
		cfg := smtpCfg(port)
		cfg.EnableTLS = true
		m, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		err = sendTestMessage(t, m)
		if err == nil {
			t.Fatal("send succeeded without a trusted CA")
		}
		// The mail library flattens the error to text, so match its message.
		if !strings.Contains(err.Error(), "failed to verify certificate") {
			t.Errorf("error is not a certificate error: %v", err)
		}
		if srv.sawAuth() {
			t.Errorf("server saw AUTH despite untrusted certificate; commands: %q", srv.commands())
		}
	})
}

func TestValidateOnlyChecksSMTP(t *testing.T) {
	t.Parallel()
	smtpBoth := smtpCfg(587)
	smtpBoth.EnableTLS = true
	smtpBoth.EnableStartTLS = true
	if err := Validate(&smtpBoth); err == nil {
		t.Error("SMTP with both TLS modes was not refused")
	}

	mailgun := smtpBoth
	mailgun.Domain = "mg.example.com"
	mailgun.MailgunPrivate = "key"
	if err := Validate(&mailgun); err != nil {
		t.Errorf("Mailgun configuration with stale SMTP keys was refused: %v", err)
	}
	if _, err := New(mailgun); err != nil {
		t.Errorf("New refused a Mailgun configuration: %v", err)
	}
}

func TestRecipientErrorLeavesOutAddress(t *testing.T) {
	t.Parallel()
	const addr = "someone@example.test"

	reply := &textproto.Error{Code: 550, Msg: "5.1.1 <" + addr + ">: Recipient address rejected: User unknown"}
	if got := recipientError(reply).Error(); got != "the SMTP server answered 550" {
		t.Errorf("SMTP reply described as %q, want its code alone", got)
	}

	// The mail library quotes an address it can't parse
	unparsed := errors.New("Mail Error: mail: missing @ in addr-spec; Header: [To] Address: [" + addr + "]")
	if got := recipientError(unparsed).Error(); strings.Contains(got, addr) {
		t.Errorf("unparsable address described as %q", got)
	}

	netErr := &net.OpError{Op: "write", Net: "tcp", Err: errors.New("broken pipe")}
	if got := recipientError(netErr); !errors.Is(got, netErr) {
		t.Errorf("network error described as %v, want it kept", got)
	}
}
