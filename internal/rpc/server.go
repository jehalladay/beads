package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/steveyegge/beads/internal/storage"
)

// Server is the RPC server that handles requests from bd clients.
type Server struct {
	storage  storage.Storage
	listener net.Listener
	sockPath string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex // Protects shutdown state
	shutdown bool
}

// NewServer creates a new RPC server.
func NewServer(store storage.Storage, sockPath string) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		storage:  store,
		sockPath: sockPath,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start starts the RPC server listening on the Unix socket.
func (s *Server) Start() error {
	if err := os.Remove(s.sockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	listener, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket %s: %w", s.sockPath, err)
	}
	s.listener = listener

	if err := os.Chmod(s.sockPath, 0600); err != nil {
		s.listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop gracefully stops the RPC server.
func (s *Server) Stop() error {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return nil
	}
	s.shutdown = true
	s.mu.Unlock()

	s.cancel()

	if s.listener != nil {
		s.listener.Close()
	}

	s.wg.Wait()

	if err := os.Remove(s.sockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove socket: %w", err)
	}

	return nil
}

// acceptLoop accepts incoming connections and handles them.
func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				fmt.Fprintf(os.Stderr, "Error accepting connection: %v\n", err)
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection handles a single client connection.
func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	writer := bufio.NewWriter(conn)

	for scanner.Scan() {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		line := scanner.Bytes()
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			resp := NewErrorResponse(fmt.Errorf("invalid request JSON: %w", err))
			s.sendResponse(writer, resp)
			continue
		}

		resp := s.handleRequest(&req)
		s.sendResponse(writer, resp)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading from connection: %v\n", err)
	}
}

// sendResponse sends a response to the client.
func (s *Server) sendResponse(writer *bufio.Writer, resp *Response) {
	respJSON, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling response: %v\n", err)
		return
	}

	if _, err := writer.Write(respJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing response: %v\n", err)
		return
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing newline: %v\n", err)
		return
	}
	if err := writer.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "Error flushing response: %v\n", err)
	}
}

// handleRequest processes an RPC request and returns a response.
func (s *Server) handleRequest(req *Request) *Response {
	ctx := context.Background()

	switch req.Operation {
	case OpBatch:
		return s.handleBatch(ctx, req)
	case OpCreate:
		return s.handleCreate(ctx, req)
	case OpUpdate:
		return s.handleUpdate(ctx, req)
	case OpClose:
		return s.handleClose(ctx, req)
	case OpList:
		return s.handleList(ctx, req)
	case OpShow:
		return s.handleShow(ctx, req)
	case OpReady:
		return s.handleReady(ctx, req)
	case OpBlocked:
		return s.handleBlocked(ctx, req)
	case OpStats:
		return s.handleStats(ctx, req)
	case OpDepAdd:
		return s.handleDepAdd(ctx, req)
	case OpDepRemove:
		return s.handleDepRemove(ctx, req)
	case OpDepTree:
		return s.handleDepTree(ctx, req)
	case OpLabelAdd:
		return s.handleLabelAdd(ctx, req)
	case OpLabelRemove:
		return s.handleLabelRemove(ctx, req)
	case OpLabelList:
		return s.handleLabelList(ctx, req)
	case OpLabelListAll:
		return s.handleLabelListAll(ctx, req)
	default:
		return NewErrorResponse(fmt.Errorf("unknown operation: %s", req.Operation))
	}
}

// Placeholder handlers - will be implemented in future commits
func (s *Server) handleBatch(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("batch operation not yet implemented"))
}

func (s *Server) handleCreate(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("create operation not yet implemented"))
}

func (s *Server) handleUpdate(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("update operation not yet implemented"))
}

func (s *Server) handleClose(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("close operation not yet implemented"))
}

func (s *Server) handleList(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("list operation not yet implemented"))
}

func (s *Server) handleShow(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("show operation not yet implemented"))
}

func (s *Server) handleReady(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("ready operation not yet implemented"))
}

func (s *Server) handleBlocked(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("blocked operation not yet implemented"))
}

func (s *Server) handleStats(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("stats operation not yet implemented"))
}

func (s *Server) handleDepAdd(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("dep_add operation not yet implemented"))
}

func (s *Server) handleDepRemove(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("dep_remove operation not yet implemented"))
}

func (s *Server) handleDepTree(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("dep_tree operation not yet implemented"))
}

func (s *Server) handleLabelAdd(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("label_add operation not yet implemented"))
}

func (s *Server) handleLabelRemove(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("label_remove operation not yet implemented"))
}

func (s *Server) handleLabelList(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("label_list operation not yet implemented"))
}

func (s *Server) handleLabelListAll(ctx context.Context, req *Request) *Response {
	return NewErrorResponse(fmt.Errorf("label_list_all operation not yet implemented"))
}
