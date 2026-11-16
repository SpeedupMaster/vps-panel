package api

import (
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

// AgentBinaryPath is where the panel keeps the current agent builds:
// data/agent/vpsagent-linux-<arch> (+ .sig written by the release module).
func (a *API) AgentBinaryPath(arch string) string {
	return filepath.Join(a.P.DataDir, "agent", "vpsagent-linux-"+arch)
}

func (a *API) downloadAgent(c *gin.Context) {
	m := dlName.FindStringSubmatch(c.Param("file"))
	if m == nil {
		c.Status(http.StatusNotFound)
		return
	}
	path := a.AgentBinaryPath(m[1])
	switch m[2] {
	case "":
		c.File(path)
	case ".sha256":
		f, err := os.Open(path)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer f.Close()
		h := sha256.New()
		_, _ = io.Copy(h, f)
		c.String(http.StatusOK, hex.EncodeToString(h.Sum(nil))+"  vpsagent\n")
	case ".sig":
		b, err := os.ReadFile(path + ".sig")
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
