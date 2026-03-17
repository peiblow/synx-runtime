package swp

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

type MessageType string

const (
	DEPLOY MessageType = "DEPLOY"
	EXEC   MessageType = "EXEC"
	PING   MessageType = "PING"
)

type WireMesage struct {
	Type MessageType `json:"type"`
	ID   string      `json:"id"`
	Data interface{} `json:"data"`
}

type DeployPayload struct {
	Hash         string `json:"hash"`
	ContractName string `json:"contract_name"`
	Version      string `json:"version"`
	Owner        string `json:"owner"`
	Source       []byte `json:"source"`
}

type ArtifactMetadata struct {
	Bytecode     []byte                 `json:"bytecode"`
	ConstPool    []interface{}          `json:"const_pool"`
	Functions    map[string]interface{} `json:"functions"`
	FunctionName map[int]string         `json:"function_name"`
	Types        map[string]interface{} `json:"types"`
	InitStorage  map[int]interface{}    `json:"init_storage"`
}

type AgentMeta struct {
	Hash    string `json:"hash"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type DeployResponse struct {
	Agent            AgentMeta        `json:"agent"`
	ContractHash     string           `json:"contract_hash"`
	ContractName     string           `json:"contract_name"`
	ContractOwner    string           `json:"contract_owner"`
	ContractVersion  string           `json:"contract_version"`
	Functions        []string         `json:"functions"`
	ContractArtifact ArtifactMetadata `json:"contract_artifact"`
}

type ExecPayload struct {
	ArtifactHash     string           `json:"contract_id"`
	ContractArtifact ArtifactMetadata `json:"contract_artifact"`
	Function         string           `json:"function"`
	Args             map[string]any   `json:"args"`
	ContextId        string           `json:"context_id,omitempty"`
}

type ExecResponse struct {
	ArtifactHash string        `json:"artifact_hash"`
	Function     string        `json:"function"`
	Journal      []interface{} `json:"journal"`
	ExecPrice    int64         `json:"exec_price"`
	Timestamp    int64         `json:"timestamp"`
}

type PingPayload struct {
	Timestamp int64 `json:"timestamp"`
}

type WireResponse struct {
	Type    MessageType     `json:"type"`
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type SwpClient struct {
	addr string
	conn net.Conn
	mu   sync.Mutex
}

func NewSwpClient(addr string) *SwpClient {
	return &SwpClient{
		addr: addr,
	}
}

func (sc *SwpClient) Connect() error {
	conn, err := net.Dial("tcp", sc.addr)
	if err != nil {
		panic(err)
	}
	sc.conn = conn
	return nil
}

func (sc *SwpClient) Send(msg WireMesage, resp any) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	return sc.sendWithRetry(msg, resp, true)
}

func (sc *SwpClient) sendWithRetry(msg WireMesage, resp any, canRetry bool) error {
	fmt.Printf("[SWP] Sending message type=%s id=%s (retry=%v)\n", msg.Type, msg.ID, !canRetry)

	if err := Encode(sc.conn, msg); err != nil {
		fmt.Printf("[SWP] Encode error: %v\n", err)
		if canRetry {
			if err := sc.reconnect(); err != nil {
				return err
			}
			return sc.sendWithRetry(msg, resp, false)
		}
		return err
	}
	fmt.Println("[SWP] Encode success, waiting for response...")

	if err := Decode(sc.conn, resp); err != nil {
		fmt.Printf("[SWP] Decode error: %v\n", err)
		if canRetry {
			if err := sc.reconnect(); err != nil {
				return err
			}
			return sc.sendWithRetry(msg, resp, false)
		}
		return err
	}
	fmt.Println("[SWP] Decode success")

	return nil
}

func (sc *SwpClient) reconnect() error {
	fmt.Println("[SWP] Reconnecting...")
	if sc.conn != nil {
		sc.conn.Close()
	}
	conn, err := net.Dial("tcp", sc.addr)
	if err != nil {
		fmt.Printf("[SWP] Reconnect failed: %v\n", err)
		return err
	}
	sc.conn = conn
	fmt.Println("[SWP] Reconnected!")
	return nil
}

func (sc *SwpClient) Close() error {
	return sc.conn.Close()
}
