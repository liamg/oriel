package editor_test

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/registry"
)

// A click on a failed image's chip fetches it again at once.
func TestMarkdownRemoteImageRetriesOnClick(t *testing.T) {
	const license = "https://img.shields.io/badge/license-MIT-blue.svg"
	files := map[string][]byte{}
	release, client := remotePictures(t, files)
	release()
	s := readmeScene(t, client)
	framesUntil(s, t, "the badge to fail", func(int) {}, func() bool { return s.FindRole("button", "License") != nil })
	files[license] = []byte(editor.Badge)
	s.Click(apptest.Centre(s.NodeRole("button", "License").Bounds), 0)
	framesUntil(s, t, "the badge to load", func(int) {}, func() bool {
		n := s.FindRole("image", "License")
		return n != nil && n.Bounds.Dx() == 90
	})
}

// markdown.remote_images off: a remote image is a chip saying so, and nothing is fetched.
func TestMarkdownRemoteImagesOff(t *testing.T) {
	const src = "https://example.com/pixel.png"
	var fetched atomic.Bool
	s := readmeScene(t, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		fetched.Store(fetched.Load() || r.URL.String() == src)
		return nil, errors.New("offline")
	})})
	if err := s.Registry().Options().Set("markdown.remote_images", registry.LayerUser, registry.At{}, false); err != nil {
		t.Fatal(err)
	}
	pc, why := editor.MdPicture(apptest.PluginOf[*editor.Markdown](s, "markdown").Pictures, src, "")
	if pc.Loading || !strings.Contains(why, "markdown.remote_images") {
		t.Errorf("loading %v, why %q", pc.Loading, why)
	}
	s.Idle()
	if fetched.Load() {
		t.Error("a remote image was fetched with markdown.remote_images off")
	}
}
