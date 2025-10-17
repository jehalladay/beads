package rpc

import "encoding/json"

// Request represents an RPC request from a client to the daemon.
type Request struct {
	Operation string          `json:"operation"`
	Args      json.RawMessage `json:"args"`
}

// Response represents an RPC response from the daemon to a client.
type Response struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// BatchRequest represents a batch of operations to execute atomically.
type BatchRequest struct {
	Operations []Request `json:"operations"`
	Atomic     bool      `json:"atomic"`
}

// Operations supported by the daemon.
const (
	OpCreate       = "create"
	OpUpdate       = "update"
	OpClose        = "close"
	OpList         = "list"
	OpShow         = "show"
	OpReady        = "ready"
	OpBlocked      = "blocked"
	OpStats        = "stats"
	OpDepAdd       = "dep_add"
	OpDepRemove    = "dep_remove"
	OpDepTree      = "dep_tree"
	OpLabelAdd     = "label_add"
	OpLabelRemove  = "label_remove"
	OpLabelList    = "label_list"
	OpLabelListAll = "label_list_all"
	OpExport       = "export"
	OpImport       = "import"
	OpCompact      = "compact"
	OpRestore      = "restore"
	OpBatch        = "batch"
)

// NewRequest creates a new RPC request with the given operation and arguments.
func NewRequest(operation string, args interface{}) (*Request, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &Request{
		Operation: operation,
		Args:      argsJSON,
	}, nil
}

// NewSuccessResponse creates a successful response with the given data.
func NewSuccessResponse(data interface{}) (*Response, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &Response{
		Success: true,
		Data:    dataJSON,
	}, nil
}

// NewErrorResponse creates an error response with the given error message.
func NewErrorResponse(err error) *Response {
	return &Response{
		Success: false,
		Error:   err.Error(),
	}
}

// UnmarshalArgs unmarshals the request arguments into the given value.
func (r *Request) UnmarshalArgs(v interface{}) error {
	return json.Unmarshal(r.Args, v)
}

// UnmarshalData unmarshals the response data into the given value.
func (r *Response) UnmarshalData(v interface{}) error {
	return json.Unmarshal(r.Data, v)
}
