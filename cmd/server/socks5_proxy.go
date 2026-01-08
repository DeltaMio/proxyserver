package main

import (
	"context"
	"log"
	"net"
	"proxyserver/socks5"
	"regexp"
	"strings"
)

func spawnSocks5Proxy(ctx context.Context, logger *log.Logger, serveAddr, socks5Username, socks5Password string) error {
	var listenOnAddr *net.TCPAddr
	var proxyFromIP net.IP

	// [Case 1] port not specified: "127.0.0.1"
	// [Case 2] addr not specified: ""
	if !strings.Contains(serveAddr, ":") {
		serveAddr = serveAddr + ":2222"
	}

	if addr, err := net.ResolveTCPAddr("tcp", serveAddr); err != nil {
		return err
	} else {
		listenOnAddr = addr
		proxyFromIP = net.ParseIP(regexp.MustCompile(":[0-9]+").ReplaceAllString(serveAddr, ""))
	}

	server := socks5.NewServer(logger, proxyFromIP)
	server.OnConnected(func(network, address string, port int) {
		logger.Printf("[socks5Proxy] proxying to %s\n", address)
	})
	server.EnableUDP()
	server.SetAuthHandle(func(username, password string) bool {
		return username == socks5Username && password == socks5Password
	})

	return server.Run(ctx, listenOnAddr)
}
