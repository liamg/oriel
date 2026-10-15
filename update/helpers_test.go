package update_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/json"
	"testing"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type tarFile struct {
	name     string
	body     []byte
	mode     int64
	dir      bool
	linkname string
}

func tarGz(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		h := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		switch {
		case f.dir:
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		case f.linkname != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, f.linkname, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jsonOf(v any) ([]byte, error) { return json.Marshal(v) }
