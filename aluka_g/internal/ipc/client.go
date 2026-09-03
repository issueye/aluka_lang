package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Client AIP 客户端。
type Client struct {
	conn       net.Conn
	writeMu    sync.Mutex
	seqCounter uint32
	pendingMu  sync.Mutex
	pending    map[uint32]chan *Frame
	eventMu    sync.RWMutex
	eventSub   map[string][]func(interface{})
	closed     chan struct{}
	closeOnce  sync.Once
}

// NewClient 从已有连接创建 AIP 客户端。
func NewClient(conn net.Conn) *Client {
	c := &Client{
		conn:     conn,
		pending:  make(map[uint32]chan *Frame),
		eventSub: make(map[string][]func(interface{})),
		closed:   make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Connect 连接到指定地址并创建客户端。
func Connect(address string) (*Client, error) {
	conn, err := DialIPC(address)
	if err != nil {
		return nil, fmt.Errorf("aip: connect failed to %q: %w", address, err)
	}
	return NewClient(conn), nil
}

// Close 关闭客户端连接。
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = WriteFrame(c.conn, &Frame{
			Header: Header{MsgType: OpDisconnect},
		})
		err = c.conn.Close()

		c.pendingMu.Lock()
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = make(map[uint32]chan *Frame)
		c.pendingMu.Unlock()
	})
	return err
}

// Call 同步发送 RPC 请求并等待结果（支持超时）。
func (c *Client) Call(method string, params interface{}, timeout time.Duration) (interface{}, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	seqID := atomic.AddUint32(&c.seqCounter, 1)
	reqPayload, err := json.Marshal(map[string]interface{}{
		"method": method,
		"params": params,
	})
	if err != nil {
		return nil, fmt.Errorf("aip: marshal rpc request failed: %w", err)
	}

	resChan := make(chan *Frame, 1)
	c.pendingMu.Lock()
	c.pending[seqID] = resChan
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, seqID)
		c.pendingMu.Unlock()
	}()

	frame := &Frame{
		Header: Header{
			MsgType:    OpRPCRequest,
			SequenceID: seqID,
		},
		Payload: reqPayload,
	}

	c.writeMu.Lock()
	err = WriteFrame(c.conn, frame)
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("aip: write rpc request failed: %w", err)
	}

	select {
	case resFrame, ok := <-resChan:
		if !ok || resFrame == nil {
			return nil, errors.New("aip: connection closed before receiving response")
		}
		if resFrame.Header.MsgType == OpRPCError {
			var errBody struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(resFrame.Payload, &errBody)
			return nil, fmt.Errorf("aip rpc error [%d]: %s", errBody.Error.Code, errBody.Error.Message)
		}

		var resBody struct {
			Result interface{} `json:"result"`
		}
		if err := json.Unmarshal(resFrame.Payload, &resBody); err != nil {
			return nil, fmt.Errorf("aip: unmarshal rpc response failed: %w", err)
		}
		return resBody.Result, nil

	case <-time.After(timeout):
		return nil, fmt.Errorf("aip: rpc call %q (seq=%d) timed out after %v", method, seqID, timeout)
	case <-c.closed:
		return nil, errors.New("aip: client closed")
	}
}

// Emit 单向发送事件广播。
func (c *Client) Emit(event string, data interface{}) error {
	payload, err := json.Marshal(map[string]interface{}{
		"event": event,
		"data":  data,
	})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.conn, &Frame{
		Header: Header{
			MsgType: OpEventEmit,
		},
		Payload: payload,
	})
}

// On 注册事件监听器。
func (c *Client) On(event string, handler func(interface{})) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	c.eventSub[event] = append(c.eventSub[event], handler)
}

func (c *Client) readLoop() {
	for {
		frame, err := ReadFrame(c.conn)
		if err != nil {
			_ = c.Close()
			return
		}

		switch frame.Header.MsgType {
		case OpPing:
			c.writeMu.Lock()
			_ = WriteFrame(c.conn, &Frame{
				Header: Header{
					MsgType:    OpPong,
					SequenceID: frame.Header.SequenceID,
				},
			})
			c.writeMu.Unlock()

		case OpRPCResponse, OpRPCError:
			c.pendingMu.Lock()
			ch, ok := c.pending[frame.Header.SequenceID]
			c.pendingMu.Unlock()
			if ok {
				ch <- frame
			}

		case OpEventEmit:
			var eventBody struct {
				Event string      `json:"event"`
				Data  interface{} `json:"data"`
			}
			if err := json.Unmarshal(frame.Payload, &eventBody); err == nil {
				c.eventMu.RLock()
				handlers := c.eventSub[eventBody.Event]
				c.eventMu.RUnlock()
				for _, h := range handlers {
					go h(eventBody.Data)
				}
			}
		}
	}
}
