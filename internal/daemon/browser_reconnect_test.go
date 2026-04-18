package daemon

import (
	"context"
	"errors"
	"testing"
)

func TestIsReconnectableCDPErr(t *testing.T) {
	if !isReconnectableCDPErr(context.Canceled) {
		t.Fatal("context.Canceled")
	}
	if !isReconnectableCDPErr(errors.New("read tcp: use of closed network connection")) {
		t.Fatal("closed conn")
	}
	if !isReconnectableCDPErr(errors.New("channel closed")) {
		t.Fatal("channel closed")
	}
	if isReconnectableCDPErr(errors.New("unknown tab id")) {
		t.Fatal("non-CDP should be false")
	}
}
