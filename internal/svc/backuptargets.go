package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"aegis/internal/store"
)

// TargetConfig is the (per-kind) connection config, JSON-marshaled and
// stored encrypted in backup_targets.config_enc.
type TargetConfig struct {
	// s3 / b2
	Endpoint  string `json:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	UseSSL    bool   `json:"use_ssl,omitempty"`
	// sftp
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Path     string `json:"path,omitempty"`
}

// CreateBackupTarget validates, encrypts and stores a new offsite target.
func (b *Backup) CreateBackupTarget(ctx context.Context, kind, label string, cfg TargetConfig, retentionDays int) (*store.BackupTarget, error) {
	if kind != store.BackupKindS3 && kind != store.BackupKindB2 && kind != store.BackupKindSFTP {
		return nil, errors.New("kind must be s3, b2 or sftp")
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	enc, err := b.Cipher.Encrypt(string(raw))
	if err != nil {
		return nil, err
	}
	t := &store.BackupTarget{Kind: kind, Label: label, ConfigEnc: enc, RetentionDays: retentionDays, Enabled: true}
	if err := b.Store.CreateBackupTarget(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (b *Backup) DeleteBackupTarget(ctx context.Context, id int64) error {
	return b.Store.DeleteBackupTarget(ctx, id)
}

func (b *Backup) ListBackupTargets(ctx context.Context) ([]*store.BackupTarget, error) {
	return b.Store.ListBackupTargets(ctx)
}

func (b *Backup) decodeTarget(t *store.BackupTarget) (TargetConfig, error) {
	var cfg TargetConfig
	raw, err := b.Cipher.Decrypt(t.ConfigEnc)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal([]byte(raw), &cfg)
	return cfg, err
}

// TestTarget verifies connectivity without pushing anything.
func (b *Backup) TestTarget(ctx context.Context, t *store.BackupTarget) error {
	cfg, err := b.decodeTarget(t)
	if err != nil {
		return err
	}
	switch t.Kind {
	case store.BackupKindS3, store.BackupKindB2:
		cli, err := s3Client(cfg)
		if err != nil {
			return err
		}
		ok, err := cli.BucketExists(ctx, cfg.Bucket)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		if !ok {
			return fmt.Errorf("bucket %q does not exist or isn't reachable with these credentials", cfg.Bucket)
		}
		return nil
	case store.BackupKindSFTP:
		cli, closeFn, err := sftpClient(cfg)
		if err != nil {
			return err
		}
		defer closeFn()
		if cfg.Path != "" {
			if err := cli.MkdirAll(cfg.Path); err != nil {
				return fmt.Errorf("create remote path: %w", err)
			}
		}
		return nil
	}
	return errors.New("unknown target kind")
}

func s3Client(cfg TargetConfig) (*minio.Client, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" {
		return nil, errors.New("endpoint, bucket and access key are required")
	}
	return minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
}

func sftpClient(cfg TargetConfig) (*sftp.Client, func(), error) {
	if cfg.Host == "" || cfg.User == "" {
		return nil, nil, errors.New("host and user are required")
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	conn, err := ssh.Dial("tcp", net.JoinHostPort(cfg.Host, fmt.Sprint(port)), &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint: backup offsite target, not an interactive shell
		Timeout:         15 * time.Second,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("ssh dial: %w", err)
	}
	cli, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("sftp: %w", err)
	}
	return cli, func() { cli.Close(); conn.Close() }, nil
}

// PushOffsite uploads a local backup archive to target.
func (b *Backup) PushOffsite(ctx context.Context, info *BackupInfo, t *store.BackupTarget) error {
	cfg, err := b.decodeTarget(t)
	if err != nil {
		return err
	}
	f, err := os.Open(info.Path)
	if err != nil {
		return err
	}
	defer f.Close()

	switch t.Kind {
	case store.BackupKindS3, store.BackupKindB2:
		cli, err := s3Client(cfg)
		if err != nil {
			return err
		}
		_, err = cli.PutObject(ctx, cfg.Bucket, info.Name, f, info.Size, minio.PutObjectOptions{ContentType: "application/gzip"})
		return err
	case store.BackupKindSFTP:
		cli, closeFn, err := sftpClient(cfg)
		if err != nil {
			return err
		}
		defer closeFn()
		if cfg.Path != "" {
			if err := cli.MkdirAll(cfg.Path); err != nil {
				return err
			}
		}
		dst, err := cli.Create(filepath.Join(cfg.Path, info.Name))
		if err != nil {
			return err
		}
		defer dst.Close()
		_, err = io.Copy(dst, f)
		return err
	}
	return errors.New("unknown target kind")
}

// ApplyRetention deletes local backups older than each enabled target's
// retention window, and does the same on the remote side of that target.
func (b *Backup) ApplyRetention(ctx context.Context) error {
	targets, err := b.Store.ListBackupTargets(ctx)
	if err != nil {
		return err
	}
	local, err := b.List()
	if err != nil {
		return err
	}
	// Local retention: the shortest configured retention window across
	// enabled targets governs how long local copies stick around (0/no
	// targets = keep local backups indefinitely, matching today's behavior).
	shortest := 0
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		if shortest == 0 || t.RetentionDays < shortest {
			shortest = t.RetentionDays
		}
		if err := b.applyRemoteRetention(ctx, t); err != nil {
			return fmt.Errorf("retention on %s: %w", t.Label, err)
		}
	}
	if shortest > 0 {
		cutoff := time.Now().AddDate(0, 0, -shortest)
		for _, info := range local {
			if info.CreatedAt.Before(cutoff) {
				_ = os.Remove(info.Path)
			}
		}
	}
	return nil
}

func (b *Backup) applyRemoteRetention(ctx context.Context, t *store.BackupTarget) error {
	cfg, err := b.decodeTarget(t)
	if err != nil {
		return err
	}
	cutoff := time.Now().AddDate(0, 0, -t.RetentionDays)
	switch t.Kind {
	case store.BackupKindS3, store.BackupKindB2:
		cli, err := s3Client(cfg)
		if err != nil {
			return err
		}
		for obj := range cli.ListObjects(ctx, cfg.Bucket, minio.ListObjectsOptions{Prefix: "aegis-"}) {
			if obj.Err != nil {
				continue
			}
			if obj.LastModified.Before(cutoff) {
				_ = cli.RemoveObject(ctx, cfg.Bucket, obj.Key, minio.RemoveObjectOptions{})
			}
		}
		return nil
	case store.BackupKindSFTP:
		cli, closeFn, err := sftpClient(cfg)
		if err != nil {
			return err
		}
		defer closeFn()
		entries, err := cli.ReadDir(cfg.Path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "aegis-") {
				continue
			}
			if e.ModTime().Before(cutoff) {
				_ = cli.Remove(filepath.Join(cfg.Path, e.Name()))
			}
		}
		return nil
	}
	return nil
}

// AutoBackup is a daily ticker: creates a full backup, pushes it to every
// enabled target, then applies retention. Same shape as SSL.AutoRenew.
func (b *Backup) AutoBackup(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.runScheduled(ctx)
		}
	}
}

func (b *Backup) runScheduled(ctx context.Context) {
	enabled, _ := b.Store.GetSetting(ctx, "backup_schedule_enabled")
	if enabled != "true" {
		return
	}
	info, err := b.Create(ctx, "full", 0)
	if err != nil {
		return
	}
	targets, err := b.Store.ListBackupTargets(ctx)
	if err != nil {
		return
	}
	for _, t := range targets {
		if t.Enabled {
			_ = b.PushOffsite(ctx, info, t)
		}
	}
	_ = b.ApplyRetention(ctx)
}
