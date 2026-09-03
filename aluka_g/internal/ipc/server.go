package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

// RPCHandler 定义 RPC 方法处理函数。
type RPCHandler func(params interface{}) (interface{}, error)

type serverConn struct {
	net.Conn
	writeMu sync.Mutex
}

func (sc *serverConn) writeFrame(f *Frame) error {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	return WriteFrame(sc.Conn, f)
}

// Server AIP 服务端。
type Server struct {
	listener  net.Listener
	methodsMu sync.RWMutex
	methods   map[string]RPCHandler
	connsMu   sync.Mutex
	conns     map[*serverConn]struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

// NewServer 创建 AIP 服务端。
func NewServer(address string) (*Server, error) {
	listener, err := ListenIPC(address)
	if err != nil {
		return nil, fmt.Errorf("aip: listen failed on %q: %w", address, err)
	}

	s := &Server{
		listener: listener,
		methods:  make(map[string]RPCHandler),
		conns:    make(map[*serverConn]struct{}),
		closed:   make(chan struct{}),
	}
	go s.acceptLoop()
	return s, nil
}

// Addr 返回服务端监听地址。
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

// RegisterMethod 注册 RPC 服务方法。
func (s *Server) RegisterMethod(name string, handler RPCHandler) {
	s.methodsMu.Lock()
	defer s.methodsMu.Unlock()
	s.methods[name] = handler
}

// Broadcast 向所有已连接客户端广播事件。
func (s *Server) Broadcast(event string, data interface{}) {
	payload, err := json.Marshal(map[string]interface{}{
		"event": event,
		"data":  data,
	})
	if err != nil {
		return
	}

	frame := &Frame{
		Header: Header{
			MsgType: OpEventEmit,
		},
		Payload: payload,
	}

	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	for sc := range s.conns {
		_ = sc.writeFrame(frame)
	}
}

// Close 关闭服务端并断开所有连接。
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		err = s.listener.Close()

		s.connsMu.Lock()
		for sc := range s.conns {
			_ = sc.Close()
		}
		s.conns = make(map[*serverConn]struct{})
		s.connsMu.Unlock()
	})
	return err
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
				return
			}
		}

		sc := &serverConn{Conn: conn}
		s.connsMu.Lock()
		s.conns[sc] = struct{}{}
		s.connsMu.Unlock()

		go s.handleConn(sc)
	}
}

func (s *Server) handleConn(sc *serverConn) {
	defer func() {
		_ = sc.Close()
		s.connsMu.Lock()
		delete(s.conns, sc)
		s.connsMu.Unlock()
	}()

	for {
		frame, err := ReadFrame(sc.Conn)
		if err != nil {
			return
		}

		switch frame.Header.MsgType {
		case OpPing:
			_ = sc.writeFrame(&Frame{
				Header: Header{
					MsgType:    OpPong,
					SequenceID: frame.Header.SequenceID,
				},
			})

		case OpDisconnect:
			return

		case OpRPCRequest:
			go s.processRPC(sc, frame)
		}
	}
}

func (s *Server) processRPC(sc *serverConn, req *Frame) {
	var reqBody struct {
		Method string      `json:"method"`
		Params interface{} `json:"params"`
	}

	if err := json.Unmarshal(req.Payload, &reqBody); err != nil {
		s.sendRPCError(sc, req.Header.SequenceID, -32700, "Parse error: "+err.Error())
		return
	}

	s.methodsMu.RLock()
	handler, ok := s.methods[reqBody.Method]
	s.methodsMu.RUnlock()

	if !ok {
		s.sendRPCError(sc, req.Header.SequenceID, -32601, fmt.Sprintf("Method %q not found", reqBody.Method))
		return
	}

	result, err := handler(reqBody.Params)
	if err != nil {
		s.sendRPCError(sc, req.Header.SequenceID, -32000, err.Error())
		return
	}

	resPayload, _ := json.Marshal(map[string]interface{}{
		"result": result,
	})

	_ = sc.writeFrame(&Frame{
		Header: Header{
			MsgType:    OpRPCResponse,
			SequenceID: req.Header.SequenceID,
		},
		Payload: resPayload,
	})
}

func (s *Server) sendRPCError(sc *serverConn, seqID uint32, code int, msg string) {
	errPayload, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": msg,
		},
	})
	_ = sc.writeFrame(&Frame{
		Header: Header{
			MsgType:    OpRPCError,
			SequenceID: seqID,
		},
		Payload: errPayload,
	})
}
