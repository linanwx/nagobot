package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/linanwx/nagobot/config"
)

// rpcRequest is the JSON-RPC request envelope.
type rpcRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// rpcResponseMsg is the JSON-RPC response envelope.
type rpcResponseMsg struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// rpcCall connects to the running serve process via unix socket,
// sends an RPC request, and returns the raw result.
func rpcCall(method string, params any) (json.RawMessage, error) {
	return rpcCallWithTimeout(method, params, 5*time.Second)
}

// rpcCallWithTimeout is like rpcCall but with a custom deadline.
func rpcCallWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	socketPath, err := config.SocketPath()
	if err != nil {
		return nil, fmt.Errorf("socket path: %w", err)
	}

	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to serve: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout))

	req := rpcRequest{
		ID:     "1",
		Method: method,
		Params: params,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send rpc request: %w", err)
	}

	var resp rpcResponseMsg
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read rpc response: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("rpc error: %s", resp.Error)
	}
	return resp.Result, nil
}
