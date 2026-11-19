package main

import (
	"bytes"
	"crypto/sha256"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// bundledAgentDir returns src, or the "agent" directory next to the panel
// executable when src is empty.
func bundledAgentDir(src string) string {
	if src == "" {
		if exe, err := os.Executable(); err == nil {
			src = filepath.Join(filepath.Dir(exe), "agent")
		}
	}
	return src
}

// seedAgents copies bundled agent binaries into the data dir so /install.sh
// works right after the first start. When no custom release has been manually
// published (overwrite == true), it also refreshes changed bundled binaries so
// upgrading the panel image updates /dl/agent automatically.
func seedAgents(dataDir, src string, overwrite bool) {
	src = bundledAgentDir(src)
	es, err := os.ReadDir(src)
	if err != nil {
		return
	}
	dst := filepath.Join(dataDir, "agent")
	_ = os.MkdirAll(dst, 0o755)
	for _, e := range es {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "vpsagent-linux-") {
			continue
		}
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		_, statErr := os.Stat(dstPath)
		existed := statErr == nil
		if existed && (!overwrite || sameFile(srcPath, dstPath)) {
			continue
		}
		mode := os.FileMode(0o755)
		if strings.HasSuffix(e.Name(), ".sig") {
			mode = 0o644
		}
		if err := copyFileAtomic(srcPath, dstPath, mode); err == nil {
			if !strings.HasSuffix(e.Name(), ".sig") {
				if _, err := os.Stat(srcPath + ".sig"); os.IsNotExist(err) {
					_ = os.Remove(dstPath + ".sig")
				}
			}
			if existed {
				log.Printf("updated %s", e.Name())
			} else {
				log.Printf("seeded %s", e.Name())
			}
		}
	}
}

func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil || fa.Size() != fb.Size() {
		return false
	}
	ha, err := fileSHA256(a)
	if err != nil {
		return false
	}
	hb, err := fileSHA256(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ha, hb)
}

func fileSHA256(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func copyFileAtomic(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
