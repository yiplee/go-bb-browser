package store

import (
	"encoding/json"
	"testing"
)

func TestParseResponseSummaryOK(t *testing.T) {
	resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tab":"abcd","seq":7}}`)
	tab, seq, ok, errMsg := ParseResponseSummary(resp)
	if !ok || errMsg != "" || tab != "abcd" || seq != 7 {
		t.Fatalf("got tab=%q seq=%d ok=%v err=%q", tab, seq, ok, errMsg)
	}
}

func TestParseResponseSummaryError(t *testing.T) {
	resp := []byte(`{"jsonrpc":"2.0","id":1,"error":{"message":"nope"}}`)
	_, _, ok, errMsg := ParseResponseSummary(resp)
	if ok || errMsg != "nope" {
		t.Fatalf("got ok=%v err=%q", ok, errMsg)
	}
}

func TestTabFromRequestBody(t *testing.T) {
	body := json.RawMessage(`{"jsonrpc":"2.0","method":"goto","params":{"tab":"1234","url":"https://ex"},"id":1}`)
	if got := TabFromRequestBody(body); got != "1234" {
		t.Fatalf("tab: %q", got)
	}
}
