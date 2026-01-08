package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var (
		addr                string
		httpProxyDownstream string
		smtpProxyDownstream string
		smtpProxyDatabase   string
		socks5Username      string
		socks5Password      string
	)

	flag.StringVar(&httpProxyDownstream, "http_proxy_downstream", "", "where http requests will be forwarded")
	flag.StringVar(&smtpProxyDownstream, "smtp_proxy_downstream", "", "where accepted emails will be forwarded")
	flag.StringVar(&smtpProxyDatabase, "smtp_proxy_database", "", "file where emails will be saved")
	flag.StringVar(&socks5Username, "socks5_username", "", "socks5 username")
	flag.StringVar(&socks5Password, "socks5_password", "", "socks5 password")
	flag.StringVar(&addr, "addr", "", "network interface")
	flag.Parse()

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	logger := &log.Logger{}
	logger.SetOutput(os.Stdout)

	done := make(chan error)

	go func() {
		logger.Printf("starting smtp proxy\n")

		if err := spawnSmtpProxy(ctx, logger, addr, smtpProxyDownstream, smtpProxyDatabase); err != nil {
			if ctx.Err() != nil {
				done <- nil
			} else {
				done <- err
				logger.Printf("smtp proxy: %v\n", err)
			}

			return
		}

		done <- nil
	}()

	go func() {
		logger.Printf("starting http proxy\n")

		if err := spawnHttpProxy(ctx, logger, addr, httpProxyDownstream); err != nil {
			if ctx.Err() != nil {
				done <- nil
			} else {
				done <- err
				logger.Printf("http proxy: %v\n", err)
			}

			return
		}

		done <- nil
	}()

	go func() {
		logger.Printf("starting socks5 proxy\n")

		if err := spawnSocks5Proxy(ctx, logger, addr, socks5Username, socks5Password); err != nil {
			if ctx.Err() != nil {
				done <- nil
			} else {
				done <- err
				logger.Printf("socks5 proxy: %v\n", err)
			}

			return
		}

		done <- nil
	}()

	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGQUIT)

	select {
	case <-signals:
		logger.Printf("signal received\n")

		<-time.After(time.Second * 5)
	case err := <-done:
		if err != nil {
			logger.Printf("error: %v\n", err)
			os.Exit(1)
		}
	}

	os.Exit(0)
}
