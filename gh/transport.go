package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HTTP calls the API at Base (https://api.github.com/, or a fake's) with a token.
type HTTP struct {
	Base   string
	Token  string
	Client *http.Client // http.DefaultClient if nil
}

func (h HTTP) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	base := strings.TrimSuffix(h.Base, "/")
	if path == "graphql" {
		base = strings.TrimSuffix(base, "/v3") // an Enterprise server's GraphQL is /api/graphql, beside /api/v3
	}
	req, err := http.NewRequestWithContext(ctx, method, base+"/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	c := h.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &e) != nil || e.Message == "" {
			e.Message = strings.TrimSpace(string(b))
		}
		return &Error{Status: resp.StatusCode, Message: e.Message}
	}
	return decode(b, out)
}

func decode(b []byte, out any) error {
	switch o := out.(type) {
	case nil:
		return nil
	case *string:
		*o = string(b)
		return nil
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

// CLI calls the API through the gh command (`gh api`), with the user's own login, in Dir with Env added: a
// sandboxed workspace's git guard.
type CLI struct {
	Bin  string // "gh" if ""
	Dir  string
	Env  []string
	Host string // a GitHub Enterprise server's hostname; github.com if ""
}

var ghStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

func (c CLI) Do(ctx context.Context, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	bin := c.Bin
	if bin == "" {
		bin = "gh"
	}
	args := []string{"api", "--method", method, "-H", "Accept: application/vnd.github+json", path}
	if c.Host != "" && !strings.EqualFold(c.Host, "github.com") {
		args = append(args, "--hostname", c.Host)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		cmd.Args = append(cmd.Args, "--input", "-")
		cmd.Stdin = bytes.NewReader(b)
	}
	cmd.Dir = c.Dir
	cmd.Env = append(append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1"), c.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return fmt.Errorf("gh: %w", err)
		}
		status := 0
		if m := ghStatus.FindStringSubmatch(msg); m != nil {
			status, _ = strconv.Atoi(m[1])
			msg = strings.TrimSpace(strings.TrimPrefix(ghStatus.ReplaceAllString(msg, ""), "gh:"))
		}
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(stdout.Bytes(), &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return &Error{Status: status, Message: msg}
	}
	return decode(stdout.Bytes(), out)
}
