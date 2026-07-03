package store

import (
	"testing"
)

func TestParseRPCAuditSummaryOK(t *testing.T) {
	resp := []byte(`{"jsonrpc":"2.0","result":{"tab":"abcd","seq":7},"id":1}`)
	tab, seq, ok, errMsg := ParseRPCAuditSummary(resp)
	if !ok || errMsg != "" || tab != "abcd" || seq != 7 {
		t.Fatalf("got tab=%q seq=%d ok=%v err=%q", tab, seq, ok, errMsg)
	}
}

func TestParseRPCAuditSummaryError(t *testing.T) {
	resp := []byte(`{"jsonrpc":"2.0","error":{"code":-32602,"message":"invalid params"},"id":1}`)
	_, _, ok, errMsg := ParseRPCAuditSummary(resp)
	if ok || errMsg != "invalid params" {
		t.Fatalf("got ok=%v err=%q", ok, errMsg)
	}
}

func TestTabFromRequestBody(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"goto","params":{"tab":"1234","url":"https://ex"},"id":1}`)
	if got := TabFromRequestBody(body); got != "1234" {
		t.Fatalf("tab %q", got)
	}
}
