package api

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

var dlName = regexp.MustCompile(`^linux-(amd64|arm64)(\.sha256|\.sig)?$`)

// AgentBinaryPath is where the panel keeps published agent builds:
// data/agent/vpsagent-linux-<arch> (+ .sig written by the release module).
func (a *API) AgentBinaryPath(arch string) string {
	return filepath.Join(a.P.DataDir, "agent", "vpsagent-linux-"+arch)
}

func serveAgentFile(c *gin.Context, path string) {
	if !strings.HasSuffix(path, ".gz") {
		c.File(path)
		return
	}
	c.Header("Vary", "Accept-Encoding")
	if acceptsGzip(c.GetHeader("Accept-Encoding")) {
		c.Header("Content-Encoding", "gzip")
		c.Header("Content-Type", "application/octet-stream")
		c.File(path)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	defer gr.Close()
	c.Header("Content-Type", "application/octet-stream")
	c.Status(http.StatusOK)
	if c.Request.Method != http.MethodHead {
		_, _ = io.Copy(c.Writer, gr)
	}
}

func (a *API) downloadAgent(c *gin.Context) {
	m := dlName.FindStringSubmatch(c.Param("file"))
	if m == nil {
		c.Status(http.StatusNotFound)
		return
	}
	base := "vpsagent-linux-" + m[1]
	switch m[2] {
	case "":
		path, ok := a.P.ResolveAgentFile(base)
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		serveAgentFile(c, path)
	case ".sha256":
		path, ok := a.P.ResolveAgentFile(base)
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer f.Close()
		var r io.Reader = f
		if strings.HasSuffix(path, ".gz") {
			gr, err := gzip.NewReader(f)
			if err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			defer gr.Close()
			r = gr
		}
		h := sha256.New()
		_, _ = io.Copy(h, r)
		c.String(http.StatusOK, hex.EncodeToString(h.Sum(nil))+"  vpsagent\n")
	case ".sig":
		path, ok := a.P.ResolveAgentFile(base + ".sig")
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		b, err := os.ReadFile(path)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.String(http.StatusOK, strings.TrimSpace(string(b))+"\n")
	}
}

// installScriptBody fills in the signing public key.
func (a *API) installScriptBody() []byte {
	pub := a.P.St.Get("sign_pubkey", "")
	if pub == "" {
		return a.Script
	}
	return []byte(strings.Replace(string(a.Script), "__SIGN_PUBKEY__\"", pub+"\"", 1))
}
