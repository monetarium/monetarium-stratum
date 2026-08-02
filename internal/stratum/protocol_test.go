package stratum

import (
	"encoding/json"
	"testing"
)

func TestParseRequest(t *testing.T) {
	raw := []byte(`{"id":1,"method":"mining.submit","params":["u","1","aa","bb","cc"]}`)
	req, err := ParseRequest(raw)
	if err != nil {
		t.Fatalf("unable to parse request: %v", err)
	}
	if req.ID != 1 || req.Method != Submit {
		t.Fatalf("got id=%d method=%q", req.ID, req.Method)
	}
	params, err := req.ParseStringParams()
	if err != nil {
		t.Fatalf("unable to parse params: %v", err)
	}
	if len(params) != 5 || params[0] != "u" || params[2] != "aa" {
		t.Fatalf("unexpected params: %v", params)
	}
}

func TestParseRequestEmptyMethod(t *testing.T) {
	_, err := ParseRequest([]byte(`{"id":1,"method":""}`))
	if err == nil {
		t.Fatal("expected error for empty method")
	}
}

func TestParseRequestInvalid(t *testing.T) {
	if _, err := ParseRequest([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestParseStringParamsInvalid(t *testing.T) {
	req := &Request{ID: 1, Method: Submit}
	req.Params = []json.RawMessage{json.RawMessage(`{"a":1}`)}
	if _, err := req.ParseStringParams(); err == nil {
		t.Fatal("expected error for non-string param")
	}
}

func TestNewNotifyShape(t *testing.T) {
	ntfn := NewNotify("42", "prevhash", "genTx1", "genTx2",
		[]string{}, "00000020", "ff00ffff", "01020304", true)

	raw, err := ntfn.Marshal()
	if err != nil {
		t.Fatalf("unable to marshal notify: %v", err)
	}
	var obj struct {
		ID     int           `json:"id"`
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unable to unmarshal notify: %v", err)
	}
	if obj.Method != Notify {
		t.Fatalf("method got %q", obj.Method)
	}
	if len(obj.Params) != 9 {
		t.Fatalf("params got %d want 9", len(obj.Params))
	}
	params := obj.Params
	if params[0] != "42" || params[1] != "prevhash" || params[2] != "genTx1" ||
		params[5] != "00000020" || params[8] != true {
		t.Fatalf("unexpected notify params: %v", params)
	}
}

func TestResponseMarshal(t *testing.T) {
	resp := NewResponse(3, true)
	raw, err := resp.Marshal()
	if err != nil {
		t.Fatalf("unable to marshal response: %v", err)
	}
	var parsed struct {
		ID     uint64        `json:"id"`
		Result bool          `json:"result"`
		Error  *StratumError `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unable to unmarshal response: %v", err)
	}
	if parsed.ID != 3 || !parsed.Result || parsed.Error != nil {
		t.Fatalf("unexpected response: %+v", parsed)
	}

	errResp := NewErrorResponse(3, ErrCodeUnauthorized, "bad worker")
	raw, err = errResp.Marshal()
	if err != nil {
		t.Fatalf("unable to marshal error response: %v", err)
	}
	var parsedErr struct {
		Error *StratumError `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsedErr); err != nil {
		t.Fatalf("unable to unmarshal error response: %v", err)
	}
	if parsedErr.Error == nil || parsedErr.Error.Code != ErrCodeUnauthorized {
		t.Fatalf("unexpected error response: %+v", parsedErr.Error)
	}
}
