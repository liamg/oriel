package rpcagent

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/liamg/oriel/agent"
)

// Serve says why it stopped: nil when Oriel closed the connection, the read error when it broke.
func TestServeReturnsWhyItStopped(t *testing.T) {
	none := func(Init) (agent.Provider, error) { return nil, errors.New("unused") }
	if err := Serve(strings.NewReader(""), io.Discard, none); err != nil {
		t.Errorf("a closed connection: %v, want nil", err)
	}
	broke := errors.New("pipe broke")
	if err := Serve(iotest.ErrReader(broke), io.Discard, none); !errors.Is(err, broke) && (err == nil || !strings.Contains(err.Error(), "pipe broke")) {
		t.Errorf("a broken connection: %v, want its read error", err)
	}
}
