package protocol

import "net"

// Blackhole silently closes the client connection and never writes a response.
// Unlike an HTTP error, a blackhole must reveal no policy response to the caller.
type Blackhole struct{}

func (Blackhole) Handle(conn net.Conn) error {
	if conn == nil {
		return nil
	}
	return conn.Close()
}
