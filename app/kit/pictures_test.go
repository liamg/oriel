package kit

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A remote image's client dials no loopback or private address, whatever the URL names.
func TestPictureClientRefusesLocalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes(t, solidImage(2, 2, color.NRGBA{A: 255})))
	}))
	defer srv.Close()
	if _, err := getPicture(context.Background(), NewPictures(nil, nil).client, srv.URL+"/a.png"); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("a loopback image: %v", err)
	}
}

// It follows at most 3 redirects, each to http(s) only, and checks every hop's address as it dials.
func TestPictureClientRedirects(t *testing.T) {
	img := pngBytes(t, solidImage(2, 2, color.NRGBA{A: 255}))
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(img) }))
	defer blocked.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/r/"))
		switch {
		case r.URL.Path == "/ftp":
			http.Redirect(w, r, "ftp://example.com/a.png", http.StatusFound)
		case r.URL.Path == "/blocked":
			http.Redirect(w, r, blocked.URL+"/a.png", http.StatusFound)
		case n > 0:
			http.Redirect(w, r, fmt.Sprintf("/r/%d", n-1), http.StatusFound)
		default:
			w.Write(img)
		}
	}))
	defer srv.Close()
	refuse := blocked.Listener.Addr().String()
	c := newPictureClient(func(_, address string, _ syscall.RawConn) error {
		if address == refuse {
			return &net.AddrError{Err: "refused", Addr: address}
		}
		return nil
	})
	if _, err := getPicture(context.Background(), c, srv.URL+"/r/3"); err != nil {
		t.Errorf("3 redirects: %v", err)
	}
	for _, path := range []string{"/r/4", "/ftp", "/blocked"} {
		if _, err := getPicture(context.Background(), c, srv.URL+path); err == nil {
			t.Errorf("%s was fetched", path)
		}
	}
	if _, err := getPicture(context.Background(), c, "file:///etc/passwd"); err == nil {
		t.Error("a file: URL was fetched")
	}
}

// A failed fetch is tried again once its backoff has passed, and not before.
func TestRemotePictureRetries(t *testing.T) {
	const src = "https://example.com/late.png"
	files := map[string][]byte{}
	release, client := remotePictures(t, files)
	release()
	now := time.Now()
	pics := testPictures(t, client)
	pics.Now = func() time.Time { return now }
	settled := func() *Picture {
		pc := pics.Remote(src)
		for i := 0; pc.Loading && i < 2000; i++ {
			time.Sleep(time.Millisecond)
			pc = pics.Remote(src)
		}
		return pc
	}
	if pc := settled(); pc.Err == nil {
		t.Fatal("the first fetch didn't fail")
	}
	files[src] = pngBytes(t, solidImage(4, 4, color.NRGBA{R: 200, A: 255}))
	now = now.Add(10 * time.Second)
	if pc := settled(); pc.Img != nil {
		t.Error("fetched again before its backoff")
	}
	now = now.Add(time.Minute)
	if pc := settled(); pc.Img == nil {
		t.Errorf("not fetched again after its backoff: %v", pc.Err)
	}
}

// A remote image that can't be fetched says why, and the fetch isn't tried again each ask.
func TestRemotePictureFails(t *testing.T) {
	release, client := remotePictures(t, nil)
	release()
	pics := testPictures(t, client)
	pc := pics.Remote("https://example.com/gone.png")
	for i := 0; pc.Loading && i < 500; i++ {
		time.Sleep(time.Millisecond)
		pc = pics.Remote("https://example.com/gone.png")
	}
	if pc := pics.Remote("https://example.com/gone.png"); pc.Loading || pc.Err == nil || !strings.Contains(pc.Err.Error(), "404") {
		t.Errorf("a failed fetch: loading %v, err %v", pc.Loading, pc.Err)
	}
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func solidImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// remotePictures is a client answering remote images from files instead of the network, each fetch held until
// release; an unknown URL is a 404.
func remotePictures(t *testing.T, files map[string][]byte) (release func(), client *http.Client) {
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		b, ok := files[r.URL.String()]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: http.NoBody, Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// testPictures are a window's pictures, fetched with client, until the test ends.
func testPictures(t *testing.T, client *http.Client) *Pictures {
	p := NewPictures(client, nil)
	t.Cleanup(p.Close)
	return p
}

// A file's picture is read and decoded off the loop: loading at first, then the image.
func TestPictureFileDecodesOffTheLoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(path, pngBytes(t, solidImage(3, 2, color.NRGBA{G: 200, A: 255})), 0o644)
	pics := testPictures(t, nil)
	if pc := pics.File(path); !pc.Loading || pc.Img != nil {
		t.Fatalf("the first ask decoded on the loop: %+v", pc)
	}
	pc := pics.File(path)
	for i := 0; pc.Loading && i < 2000; i++ {
		time.Sleep(time.Millisecond)
		pc = pics.File(path)
	}
	if pc.Img == nil || pc.Img.Bounds().Dx() != 3 {
		t.Errorf("the picture: %+v", pc)
	}
}
