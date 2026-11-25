package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/panel/notify"
	"vpspanel/internal/panel/store"
)

// ---------- remote targets ----------

func hmacSHA(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

// uploadS3File PUTs a file with AWS SigV4 using streaming I/O.
func (p *Panel) uploadS3File(ctx context.Context, name, filePath string) error {
	endpoint := strings.TrimRight(p.St.Get("backup_s3_endpoint", ""), "/")
	bucket := p.St.Get("backup_s3_bucket", "")
	ak := p.St.Get("backup_s3_access_key", "")
	sk := p.SecretSetting("backup_s3_secret")
	region := p.St.Get("backup_s3_region", "auto")
	if endpoint == "" || bucket == "" || ak == "" || sk == "" {
		return errors.New("S3 配置不完整")
	}
	key := strings.Trim(p.St.Get("backup_s3_prefix", "vps-panel"), "/")
	if key != "" {
		key += "/"
	}
	key += name
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	req, err := s3SignedRequest(ctx, endpoint, region, bucket, key, ak, sk, hex.EncodeToString(h.Sum(nil)), fi.Size(), f, time.Now().UTC())
	if err != nil {
		return err
	}
	resp, err := notify.SafeHTTPClient(5 * time.Minute).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func s3Request(ctx context.Context, endpoint, region, bucket, key, ak, sk string, body []byte, now time.Time) (*http.Request, error) {
	sum := sha256.Sum256(body)
	return s3SignedRequest(ctx, endpoint, region, bucket, key, ak, sk, hex.EncodeToString(sum[:]), int64(len(body)), bytes.NewReader(body), now)
}

func s3SignedRequest(ctx context.Context, endpoint, region, bucket, key, ak, sk, payload string, size int64, body io.Reader, now time.Time) (*http.Request, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("S3 endpoint 无效")
	}
	escKey := (&url.URL{Path: key}).EscapedPath()
	path := strings.TrimRight(u.Path, "/") + "/" + bucket + "/" + escKey
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	signed := "host;x-amz-content-sha256;x-amz-date"
	canon := "PUT\n" + path + "\n\n" + "host:" + u.Host + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amzDate + "\n\n" + signed + "\n" + payload
	scope := day + "/" + region + "/s3/aws4_request"
	ch := sha256.Sum256([]byte(canon))
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	k := hmacSHA([]byte("AWS4"+sk), day)
	k = hmacSHA(k, region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, sts))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.Scheme+"://"+u.Host+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+ak+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	req.ContentLength = size
	return req, nil
}

func (p *Panel) uploadWebDAVFile(ctx context.Context, name, filePath string) error {
	base := strings.TrimRight(p.St.Get("backup_webdav_url", ""), "/")
	if base == "" {
		return errors.New("未设置 WebDAV 地址")
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("WebDAV 地址无效")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/"+name, f)
	if err != nil {
		return err
	}
	req.ContentLength = fi.Size()
	if u := p.St.Get("backup_webdav_user", ""); u != "" {
		req.SetBasicAuth(u, p.SecretSetting("backup_webdav_password"))
	}
	resp, err := notify.SafeHTTPClient(5 * time.Minute).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *Panel) uploadTelegramFile(ctx context.Context, name, filePath string) error {
	var chs []store.NotifyChannel
	q := p.St.DB.Where("type = ?", "telegram")
	if id, _ := strconv.Atoi(p.St.Get("backup_tg_channel", "")); id > 0 {
		q = q.Where("id = ?", id)
	}
	q.Order("id").Find(&chs)
	if len(chs) == 0 {
		return errors.New("没有 Telegram 通知渠道")
	}
	cfg, err := p.ChannelConfig(&chs[0])
	if err != nil {
		return err
	}
	fi, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if fi.Size() > 49<<20 {
		return errors.New("备份超过 Telegram 50MB 限制")
	}
	return notify.SendTelegramDocumentFile(ctx, cfg, name, filePath, "vps-panel 备份 "+time.Now().Format("2006-01-02 15:04"))
}

// ---------- restore ----------

const restoreDir = "restore-pending"

// StageRestore validates an in-memory backup and stages it on disk.
func (p *Panel) StageRestore(data []byte, pass string) error {
	plain, err := decryptBackup(data, pass)
	if err != nil {
		return err
	}
	return p.stageRestoreFromReader(bytes.NewReader(plain))
}

// StageRestoreFile validates a backup file on disk and streams its contents
// directly into restore-pending without buffering the uncompressed database in RAM.
func (p *Panel) StageRestoreFile(srcPath, pass string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	var magic [5]byte
	n, err := io.ReadFull(f, magic[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = f.Close()
		return err
	}
	if n >= len(backupMagic) && bytes.Equal(magic[:], backupMagic) {
		_ = f.Close()
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		plain, err := decryptBackup(data, pass)
		data = nil
		if err != nil {
			return err
		}
		return p.stageRestoreFromReader(bytes.NewReader(plain))
	}
	if n < 2 || magic[0] != 0x1f || magic[1] != 0x8b {
		_ = f.Close()
		return errors.New("不是有效的备份文件")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	defer f.Close()
	return p.stageRestoreFromReader(f)
}

func (p *Panel) stageRestoreFromReader(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return errors.New("备份文件已损坏")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	tmpDir := filepath.Join(p.DataDir, fmt.Sprintf(".%s-tmp-%d", restoreDir, time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	var gotDB, gotKey bool
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("备份文件已损坏")
		}
		if h.Name != "panel.db" && h.Name != "master.key" {
			continue
		}
		dst := filepath.Join(tmpDir, h.Name)
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		var head [16]byte
		lr := io.LimitReader(tr, 2<<30)
		nHead, headErr := io.ReadFull(lr, head[:])
		if headErr != nil && !errors.Is(headErr, io.EOF) && !errors.Is(headErr, io.ErrUnexpectedEOF) {
			_ = out.Close()
			return headErr
		}
		if _, err := out.Write(head[:nHead]); err != nil {
			_ = out.Close()
			return err
		}
		nRest, copyErr := io.Copy(out, lr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		total := int64(nHead) + nRest
		if h.Name == "panel.db" && nHead == 16 && string(head[:]) == "SQLite format 3\x00" {
			gotDB = true
		}
		if h.Name == "master.key" && total >= 64 {
			gotKey = true
		}
	}
	if !gotDB || !gotKey {
		return errors.New("备份中缺少数据库或主密钥")
	}
	dir := filepath.Join(p.DataDir, restoreDir)
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmpDir, dir); err != nil {
		return err
	}
	ok = true
	return nil
}

// ApplyPendingRestore swaps in a staged restore before the store is opened.
// The previous database and key are kept with a .before-restore suffix.
func ApplyPendingRestore(dataDir string) error {
	dir := filepath.Join(dataDir, restoreDir)
	if _, err := os.Stat(filepath.Join(dir, "panel.db")); err != nil {
		return nil
	}
	ts := time.Now().Format("20060102-150405")
	for _, n := range []string{"panel.db", "master.key"} {
		cur := filepath.Join(dataDir, n)
		if _, err := os.Stat(cur); err == nil {
			if err := os.Rename(cur, cur+".before-restore-"+ts); err != nil {
				return err
			}
		}
		if err := os.Rename(filepath.Join(dir, n), cur); err != nil {
			return err
		}
	}
	os.Remove(filepath.Join(dataDir, "panel.db-wal"))
	os.Remove(filepath.Join(dataDir, "panel.db-shm"))
	log.Printf("restored database from backup (previous copy kept with suffix .before-restore-%s)", ts)
	return os.RemoveAll(dir)
}
