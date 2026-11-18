package lsp_test

import (
	"os"
	"testing"

	"github.com/liamg/oriel/lsp/lsptest"
)

func TestMain(m *testing.M) {
	lsptest.ServeProcess()
	os.Exit(m.Run())
}
