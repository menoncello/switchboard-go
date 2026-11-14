package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SMTPNotifier sends the email alerts. The tier switch warning goes out once per
// episode: it disarms when an extra tier serves a request and re-arms when tier 0 does.
type SMTPNotifier struct {
	cfg            SMTPConfig
	mu             sync.Mutex
	switchDisarmed bool
}

func NewSMTPNotifier(cfg SMTPConfig) *SMTPNotifier { return &SMTPNotifier{cfg: cfg} }
func (n *SMTPNotifier) NotifySwitch(idx int, st StatusResponse) {
	go n.send("Switchboard Go switched upstream key", fmt.Sprintf("Switched away from key %d\n\n%+v", idx, st))
}
func (n *SMTPNotifier) NotifyAllExhausted(st StatusResponse) {
	go n.send("Switchboard Go exhausted all upstream keys", fmt.Sprintf("All keys exhausted\n\n%+v", st))
}
func (n *SMTPNotifier) NotifyPaidFirstUse(tier, model, day string) {
	go n.send("Switchboard Go paid tier in use: "+tier, fmt.Sprintf("Paid tier %q served its first request of %s (model %q).", tier, day, model))
}
func (n *SMTPNotifier) configured() bool {
	return n.cfg.Host != "" && n.cfg.To != "" && n.cfg.From != ""
}

// NotifyTierSwitch warns that an extra tier served a request tier 0 did not, once per episode.
func (n *SMTPNotifier) NotifyTierSwitch(tier, model string) {
	if !n.configured() {
		return
	}
	n.mu.Lock()
	first := !n.switchDisarmed
	n.switchDisarmed = true
	n.mu.Unlock()
	if first {
		go func() {
			// a lost warning must not silence the rest of the episode: retry on the next switch
			if !n.send("Switchboard Go switched tier: "+tier, fmt.Sprintf("Tier 0 did not serve a request; tier %q served it (model %q).", tier, truncateModel(model))) {
				n.rearmTierSwitch()
			}
		}()
	}
}

// rearmTierSwitch ends the switch episode: tier 0 served a request.
func (n *SMTPNotifier) rearmTierSwitch() {
	n.mu.Lock()
	n.switchDisarmed = false
	n.mu.Unlock()
}

// send reports whether the email went out.
func (n *SMTPNotifier) send(subject, body string) bool {
	if !n.configured() {
		return false
	}
	addr := net.JoinHostPort(n.cfg.Host, strconv.Itoa(n.cfg.Port))
	auth := smtp.Auth(nil)
	if strings.TrimSpace(n.cfg.Username) != "" {
		auth = smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)
	}
	msg := []byte("To: " + n.cfg.To + "\r\nSubject: " + subject + "\r\n\r\n" + body + "\r\n")
	if err := sendMail(addr, auth, n.cfg.From, []string{n.cfg.To}, msg, n.cfg.TLS, n.cfg.StartTLS); err != nil {
		log.Printf("smtp notification failed: %v", err)
		return false
	}
	return true
}

// tiny indirection to keep stdlib-only and testable.
var sendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte, useTLS, useStartTLS bool) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	host, _, _ := net.SplitHostPort(addr)
	if useTLS {
		conn = tls.Client(conn, &tls.Config{ServerName: host})
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Quit()
	if useStartTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return err
			}
		}
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}
