package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNewRequest(t *testing.T) {
	args := map[string]string{
		"title":    "Test issue",
		"priority": "1",
	}

	req, err := NewRequest(OpCreate, args)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	if req.Operation != OpCreate {
		t.Errorf("Expected operation %s, got %s", OpCreate, req.Operation)
	}

	var unmarshaledArgs map[string]string
	if err := req.UnmarshalArgs(&unmarshaledArgs); err != nil {
		t.Fatalf("UnmarshalArgs failed: %v", err)
	}

	if unmarshaledArgs["title"] != args["title"] {
		t.Errorf("Expected title %s, got %s", args["title"], unmarshaledArgs["title"])
	}
}

func TestNewSuccessResponse(t *testing.T) {
	data := map[string]interface{}{
		"id":     "bd-123",
		"status": "open",
	}

	resp, err := NewSuccessResponse(data)
	if err != nil {
		t.Fatalf("NewSuccessResponse failed: %v", err)
	}

	if !resp.Success {
		t.Error("Expected success=true")
	}

	if resp.Error != "" {
		t.Errorf("Expected empty error, got %s", resp.Error)
	}

	var unmarshaledData map[string]interface{}
	if err := resp.UnmarshalData(&unmarshaledData); err != nil {
		t.Fatalf("UnmarshalData failed: %v", err)
	}

	if unmarshaledData["id"] != data["id"] {
		t.Errorf("Expected id %s, got %s", data["id"], unmarshaledData["id"])
	}
}

func TestNewErrorResponse(t *testing.T) {
	testErr := errors.New("test error")

	resp := NewErrorResponse(testErr)

	if resp.Success {
		t.Error("Expected success=false")
	}

	if resp.Error != testErr.Error() {
		t.Errorf("Expected error %s, got %s", testErr.Error(), resp.Error)
	}

	if len(resp.Data) != 0 {
		t.Errorf("Expected empty data, got %v", resp.Data)
	}
}

func TestRequestResponseJSON(t *testing.T) {
	req := &Request{
		Operation: OpList,
		Args:      json.RawMessage(`{"status":"open"}`),
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal request failed: %v", err)
	}

	var unmarshaledReq Request
	if err := json.Unmarshal(reqJSON, &unmarshaledReq); err != nil {
		t.Fatalf("Unmarshal request failed: %v", err)
	}

	if unmarshaledReq.Operation != req.Operation {
		t.Errorf("Expected operation %s, got %s", req.Operation, unmarshaledReq.Operation)
	}

	resp := &Response{
		Success: true,
		Data:    json.RawMessage(`{"count":5}`),
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal response failed: %v", err)
	}

	var unmarshaledResp Response
	if err := json.Unmarshal(respJSON, &unmarshaledResp); err != nil {
		t.Fatalf("Unmarshal response failed: %v", err)
	}

	if unmarshaledResp.Success != resp.Success {
		t.Errorf("Expected success %v, got %v", resp.Success, unmarshaledResp.Success)
	}
}

func TestBatchRequest(t *testing.T) {
	req1, _ := NewRequest(OpCreate, map[string]string{"title": "Issue 1"})
	req2, _ := NewRequest(OpCreate, map[string]string{"title": "Issue 2"})

	batch := &BatchRequest{
		Operations: []Request{*req1, *req2},
		Atomic:     true,
	}

	batchJSON, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("Marshal batch failed: %v", err)
	}

	var unmarshaledBatch BatchRequest
	if err := json.Unmarshal(batchJSON, &unmarshaledBatch); err != nil {
		t.Fatalf("Unmarshal batch failed: %v", err)
	}

	if len(unmarshaledBatch.Operations) != 2 {
		t.Errorf("Expected 2 operations, got %d", len(unmarshaledBatch.Operations))
	}

	if !unmarshaledBatch.Atomic {
		t.Error("Expected atomic=true")
	}
}
