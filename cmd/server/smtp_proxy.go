package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/emersion/go-smtp"
	_ "github.com/mattn/go-sqlite3"
)

type Backend struct {
	logger *log.Logger
	db     *sql.DB
}

type Session struct {
	*Backend
	from string
	to   []string
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error { s.from = from; return nil }
func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error   { s.to = append(s.to, to); return nil }
func (s *Session) Reset()                                         { s.from = ""; s.to = nil }
func (s *Session) Logout() error                                  { return nil }
func (s *Session) AuthPlain(username, password string) error      { return nil }

func (s *Session) Data(r io.Reader) error {
	raw, _ := io.ReadAll(r)
	toJSON, _ := json.Marshal(s.to)
	_, err := s.db.Exec(
		"INSERT INTO emails (from_addr, to_addrs, raw_data) VALUES (?, ?, ?)",
		s.from, string(toJSON), raw,
	)
	s.logger.Printf("[smtpProxy] received an email from %s\n", s.from)
	return err
}

func (b *Backend) NewSession(_ *smtp.Conn) (smtp.Session, error) { return &Session{Backend: b}, nil }

func spawnSmtpProxy(ctx context.Context, logger *log.Logger, addr, smtpProxyDownstream, smtpProxyDatabase string) error {
	db, err := sql.Open("sqlite3", fmt.Sprintf("%s?_journal_mode=WAL", smtpProxyDatabase))
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS emails (
		id INTEGER PRIMARY KEY,
		from_addr TEXT NOT NULL,
		to_addrs TEXT NOT NULL,
		raw_data BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return err
	}

	b := &Backend{logger: logger, db: db}

	// Forwarder
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}

			time.Sleep(5 * time.Second)

			rows, _ := b.db.Query("SELECT id, from_addr, to_addrs, raw_data FROM emails ORDER BY created_at LIMIT 100")
			for rows.Next() {
				if ctx.Err() != nil {
					return
				}

				var id int64
				var from, toJSON string
				var raw []byte
				_ = rows.Scan(&id, &from, &toJSON, &raw)

				body, _ := json.Marshal(map[string]any{
					"id":   id,
					"from": from,
					"to":   json.RawMessage(toJSON),
					"raw":  raw,
				})

				resp, errForward := http.Post(smtpProxyDownstream, "application/json", bytes.NewReader(body))
				if errForward != nil || resp.StatusCode != 200 {
					logger.Printf("[smtpProxy] error forwarding email: %v\n", errForward)
					_ = rows.Close()
					break
				}
				_ = resp.Body.Close()
				_, _ = b.db.Exec("DELETE FROM emails WHERE id = ?", id)

				logger.Printf("[smtpProxy] forwarded an email successfully\n")
			}
			_ = rows.Close()
		}
	}()

	// SMTP server
	s := smtp.NewServer(b)
	s.Network = "tcp"
	s.Addr = addr + ":25"
	s.Domain = "localhost"
	s.AllowInsecureAuth = true

	go func() {
		select {
		case <-ctx.Done():
			s.Shutdown(ctx)
		}
	}()

	return s.ListenAndServe()
}
