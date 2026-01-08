package socks5

import "net"

type OnConnectedHandle func(network, address string, port int)
type OnStartedHandle func(conn *net.TCPListener)
