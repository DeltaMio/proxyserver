package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	db, errOpen := sql.Open("sqlite3", smtpProxyDatabase)
	if errOpen != nil {
		return errOpen
	}
	_, errExec := db.Exec(`CREATE TABLE IF NOT EXISTS emails (
		id INTEGER PRIMARY KEY,
		from_addr TEXT NOT NULL,
		to_addrs TEXT NOT NULL,
		raw_data BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if errExec != nil {
		return errExec
	}

	b := &Backend{logger: logger, db: db}

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}

			time.Sleep(5 * time.Second)

			type rowSchema struct {
				id   int64
				from string
				to   string
				body []byte
			}
			var rows []rowSchema

			tableRows, err := b.db.Query("SELECT id, from_addr, to_addrs, raw_data FROM emails ORDER BY created_at LIMIT 100")
			if err != nil {
				logger.Printf("[smtpProxy] error selecting: %v\n", err)
				continue
			}
			for tableRows.Next() {
				var row rowSchema
				_ = tableRows.Scan(&row.id, &row.from, &row.to, &row.body)
				rows = append(rows, row)
			}
			_ = tableRows.Close()

			for i := range rows {
				body, _ := json.Marshal(map[string]any{
					"from": rows[i].from,
					"to":   json.RawMessage(rows[i].to),
					"raw":  rows[i].body,
				})

				resp, errForward := http.Post(smtpProxyDownstream, "application/json", bytes.NewReader(body))
				if errForward != nil {
					logger.Printf("[smtpProxy] error forwarding email: %v\n", errForward)
					break
				}
				if resp.StatusCode != 200 {
					_ = resp.Body.Close()
					logger.Printf("[smtpProxy] error forwarding email: non 200 status code\n")
					break
				}
				_ = resp.Body.Close()

				_, err = b.db.Exec("DELETE FROM emails WHERE id = ?", rows[i].id)
				if err != nil {
					logger.Printf("[smtpProxy] error deleting: %v\n", err)
					break
				}

				logger.Printf("[smtpProxy] forwarded an email to %s\n", smtpProxyDownstream)
			}
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
