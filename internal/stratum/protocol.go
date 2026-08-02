package stratum

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Supported stratum methods.
const (
	// Client to server methods.
	Subscribe   = "mining.subscribe"
	Authorize   = "mining.authorize"
	Submit      = "mining.submit"
	SuggestDiff = "mining.suggest_difficulty"

	// Server to client methods.
	Notify        = "mining.notify"
	SetDifficulty = "mining.set_difficulty"
	SetExtraNonce = "mining.set_extranonce"
)

// Stratum error codes.
const (
	ErrCodeLowDifficulty  = 20
	ErrCodeUnknown        = 21
	ErrCodeStaleShare     = 22
	ErrCodeDuplicateShare = 23
	ErrCodeUnauthorized   = 24
)

// StratumError is a JSON-RPC error.
type StratumError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Request is a request message sent by a miner.
type Request struct {
	ID     uint64            `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// Response is a response message sent to a miner.
type Response struct {
	ID     uint64        `json:"id"`
	Result interface{}   `json:"result,omitempty"`
	Error  *StratumError `json:"error,omitempty"`
}

// Notification is a server initiated message.
type Notification struct {
	ID     int           `json:"id"`
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

// ParseRequest unmarshals a request message and resolves its parameters into
// the expected string fields.
func ParseRequest(raw []byte) (*Request, error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.Method == "" {
		return nil, errors.New("request method is empty")
	}
	return &req, nil
}

// ParseStringParams resolves the request params as strings.
func (r *Request) ParseStringParams() ([]string, error) {
	params := make([]string, 0, len(r.Params))
	for _, p := range r.Params {
		var s string
		if err := json.Unmarshal(p, &s); err != nil {
			return nil, fmt.Errorf("invalid string param: %w", err)
		}
		params = append(params, s)
	}
	return params, nil
}

// ParseFloatParams resolves the request params as floats.
func (r *Request) ParseFloatParams() ([]float64, error) {
	params := make([]float64, 0, len(r.Params))
	for _, p := range r.Params {
		var f float64
		if err := json.Unmarshal(p, &f); err != nil {
			return nil, fmt.Errorf("invalid float param: %w", err)
		}
		params = append(params, f)
	}
	return params, nil
}

// NewResponse creates a response with a result payload.
func NewResponse(id uint64, result interface{}) *Response {
	return &Response{ID: id, Result: result}
}

// NewErrorResponse creates a response with an error payload.
func NewErrorResponse(id uint64, code int, message string) *Response {
	return &Response{ID: id, Error: &StratumError{Code: code, Message: message}}
}

// Marshal serializes a response to JSON.
func (r *Response) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// NewNotify creates a work notification.
func NewNotify(jobID string, prevHash string, genTx1 string, genTx2 string,
	merkleBranches []string, version string, nbits string, ntime string,
	cleanJobs bool) *Notification {

	params := []interface{}{jobID, prevHash, genTx1, genTx2, merkleBranches,
		version, nbits, ntime, cleanJobs}
	return &Notification{ID: 0, Method: Notify, Params: params}
}

// NewSetDifficulty creates a difficulty notification.
func NewSetDifficulty(difficulty float64) *Notification {
	return &Notification{ID: 0, Method: SetDifficulty, Params: []interface{}{difficulty}}
}

// NewSetExtraNonce creates an extra nonce notification.
func NewSetExtraNonce(extraNonce1 string, extraNonce2Length int) *Notification {
	return &Notification{ID: 0, Method: SetExtraNonce,
		Params: []interface{}{extraNonce1, extraNonce2Length}}
}

// Marshal serializes a notification to JSON.
func (n *Notification) Marshal() ([]byte, error) {
	return json.Marshal(n)
}

// jsonMarshal marshals msg to JSON.  It is used by the server for all outgoing
// messages so the encoding behavior is centralized.
func jsonMarshal(msg interface{}) ([]byte, error) {
	return json.Marshal(msg)
}
