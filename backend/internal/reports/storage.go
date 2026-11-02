package reports

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StorageConfig describes where scheduled reports are copied after generation.
// "local" writes to a mounted directory; "s3" works with AWS S3, MinIO, Ceph
// RGW and any S3-compatible endpoint that supports Signature V4.
type StorageConfig struct {
	Type          string `json:"type"`
	Endpoint      string `json:"endpoint"`
	Region        string `json:"region"`
	Bucket        string `json:"bucket"`
	Prefix        string `json:"prefix"`
	AccessKey     string `json:"access_key"`
	SecretKey     string `json:"secret_key"`
	Path          string `json:"path"`
	PublicBaseURL string `json:"public_base_url"`
	PathStyle     bool   `json:"path_style"`
}

func (s StorageConfig) Upload(ctx context.Context, filePath, filename string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "local", "directory", "filesystem":
		if strings.TrimSpace(s.Path) == "" {
			return "", fmt.Errorf("la ruta local de exportación está vacía")
		}
		if err := os.MkdirAll(s.Path, 0o755); err != nil {
			return "", err
		}
		dst := filepath.Join(s.Path, filename)
		if absDst, err := filepath.Abs(dst); err == nil {
			if absSrc, err := filepath.Abs(filePath); err == nil && absDst == absSrc {
				if s.PublicBaseURL != "" {
					return strings.TrimRight(s.PublicBaseURL, "/") + "/" + url.PathEscape(filename), nil
				}
				return dst, nil
			}
		}
		in, err := os.Open(filePath)
		if err != nil {
			return "", err
		}
		defer in.Close()
		out, err := os.Create(dst)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		if s.PublicBaseURL != "" {
			return strings.TrimRight(s.PublicBaseURL, "/") + "/" + url.PathEscape(filename), nil
		}
		return dst, nil
	case "s3", "minio", "s3-compatible":
		return s.uploadS3(ctx, filePath, filename)
	default:
		return "", fmt.Errorf("tipo de almacenamiento no soportado: %s", s.Type)
	}
}

func (s StorageConfig) uploadS3(ctx context.Context, filePath, filename string) (string, error) {
	if strings.TrimSpace(s.Bucket) == "" || strings.TrimSpace(s.AccessKey) == "" || strings.TrimSpace(s.SecretKey) == "" {
		return "", fmt.Errorf("bucket, access_key y secret_key son obligatorios para S3")
	}
	region := strings.TrimSpace(s.Region)
	if region == "" {
		region = "us-east-1"
	}
	endpoint := strings.TrimSpace(s.Endpoint)
	if endpoint == "" {
		endpoint = "https://s3." + region + ".amazonaws.com"
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint S3 inválido: %w", err)
	}
	prefix := strings.Trim(strings.TrimSpace(s.Prefix), "/")
	key := filename
	if prefix != "" {
		key = prefix + "/" + filename
	}
	key = path.Clean(key)
	key = strings.TrimPrefix(key, "/")

	host := base.Host
	escapedKey := escapePath(key)
	requestPath := "/" + escapedKey
	if s.PathStyle {
		requestPath = "/" + url.PathEscape(s.Bucket) + "/" + escapedKey
	} else if strings.Contains(host, "amazonaws.com") || !strings.Contains(host, ".") {
		host = s.Bucket + "." + host
	} else {
		requestPath = "/" + url.PathEscape(s.Bucket) + "/" + escapedKey
	}
	requestURL := base.Scheme + "://" + host + requestPath

	body, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	headers := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	var signedHeaders []string
	for k := range headers {
		signedHeaders = append(signedHeaders, k)
	}
	sort.Strings(signedHeaders)
	var canonicalHeaders strings.Builder
	for _, k := range signedHeaders {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headers[k]))
		canonicalHeaders.WriteString("\n")
	}
	canonicalRequest := strings.Join([]string{
		"PUT", requestPath, "",
		canonicalHeaders.String(),
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")
	scope := date + "/" + region + "/s3/aws4_request"
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	signingKey := hmacSHA256([]byte("AWS4"+s.SecretKey), date)
	signingKey = hmacSHA256(signingKey, region)
	signingKey = hmacSHA256(signingKey, "s3")
	signingKey = hmacSHA256(signingKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	authorization := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.AccessKey, scope, strings.Join(signedHeaders, ";"), signature)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Authorization", authorization)
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("S3 respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if s.PublicBaseURL != "" {
		return strings.TrimRight(s.PublicBaseURL, "/") + "/" + escapedKey, nil
	}
	return requestURL, nil
}

func escapePath(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func hmacSHA256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}
