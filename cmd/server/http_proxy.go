package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type httpProxyHandler struct {
	httpClient http.Client
	ctx        context.Context
	downstream string
	logger     *log.Logger
}

func (r *httpProxyHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	ctxRequest, ctxRequestCancel := context.WithTimeout(r.ctx, time.Second*6)
	defer ctxRequestCancel()

	remoteHost, _, _ := net.SplitHostPort(request.RemoteAddr)
	ip := net.ParseIP(remoteHost)
	cfConnectingIp := strings.TrimSpace(request.Header.Get("cf-connecting-ip"))

	if cfConnectingIp != "" {
		ip = net.ParseIP(cfConnectingIp)
	}

	r.logger.Printf("[httpProxyHandler] request: %v %v\n", request.Method, request.RequestURI)

	req, err := http.NewRequestWithContext(ctxRequest, request.Method, fmt.Sprintf("%s/%s", r.downstream, strings.TrimPrefix(request.RequestURI, "/")), request.Body)
	if err != nil {
		r.logger.Printf("[httpProxyHandler] error creating a request: %v\n", err)
		writer.WriteHeader(500)
		return
	}
	req.Header.Set("User-Agent", request.UserAgent())
	req.Header.Set("X-IP", ip.String())
	req.Header.Set("X-Proxied-Host", request.Host)

	r.logger.Printf("[httpProxyHandler] proxying to %s\n", fmt.Sprintf("%s/%s", r.downstream, strings.TrimPrefix(request.RequestURI, "/")))
	res, err := r.httpClient.Do(req)
	if err != nil {
		r.logger.Printf("[httpProxyHandler] error making request: %v\n", err)
		writer.WriteHeader(503)
		return
	}

	// The order of calls to writer is important!

	for key, headers := range res.Header {
		for _, ithHeader := range headers {
			r.logger.Printf("[httpProxyHandler] writing header: %s %s\n", key, ithHeader)
			writer.Header().Add(key, ithHeader)
		}
	}
	writer.WriteHeader(res.StatusCode)

	_, err = io.Copy(writer, res.Body)
	if err != nil {
		r.logger.Printf("[httpProxyHandler] error copying body: %v\n", err)
		writer.WriteHeader(503)
		return
	}
}

func spawnHttpProxy(ctx context.Context, logger *log.Logger, addr, httpProxyDownstream string) error {
	if !strings.Contains(addr, ":") {
		addr = addr + ":80"
	}

	server := http.Server{
		Addr:              addr,
		ReadTimeout:       time.Second * 10,
		ReadHeaderTimeout: time.Second * 5,
		WriteTimeout:      time.Second * 5,
		Handler: &httpProxyHandler{
			logger:     logger,
			downstream: httpProxyDownstream,
			ctx:        ctx,
			httpClient: http.Client{
				Timeout: time.Second * 8,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			},
		},
	}

	return server.ListenAndServe()
}
