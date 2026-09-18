package dola

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"dola2api/internal/config"
)

// UploadImage pushes raw image bytes to ByteDance ImageX and returns the
// StoreUri that the completion request references.
//
// Flow: prepare_upload -> ApplyImageUpload (SigV4) -> byte upload -> CommitImageUpload.
func (c *Client) UploadImage(ctx context.Context, cookie string, data []byte, name string, width, height int) (*UploadedImage, error) {
	settings := c.settings()
	if cookie == "" {
		return nil, ErrInvalidCredential
	}
	if name == "" {
		name = "image.png"
	}
	extension := ".png"
	if index := strings.LastIndex(name, "."); index >= 0 {
		extension = name[index:]
	}

	prepareBody, _ := json.Marshal(map[string]any{"tenant_id": "5", "scene_id": "4", "resource_type": 2})
	req, err := c.newRequest(ctx, settings, http.MethodPost, c.endpoint(settings, "/alice/resource/prepare_upload"), bytes.NewReader(prepareBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("cookie", cookie)

	resp, err := c.do(req, settings)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	var prepared struct {
		Code int `json:"code"`
		Data struct {
			ServiceID   string `json:"service_id"`
			UploadHost  string `json:"upload_host"`
			UploadToken struct {
				AccessKey  string `json:"access_key"`
				SecretKey  string `json:"secret_key"`
				SessionTok string `json:"session_token"`
			} `json:"upload_auth_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return nil, fmt.Errorf("prepare_upload: %w", err)
	}
	if prepared.Code != 0 {
		return nil, fmt.Errorf("prepare_upload rejected: %s", truncate(string(raw), 200))
	}

	creds := prepared.Data.UploadToken
	query := map[string]string{
		"Action":        "ApplyImageUpload",
		"Version":       "2018-08-01",
		"ServiceId":     prepared.Data.ServiceID,
		"FileSize":      fmt.Sprintf("%d", len(data)),
		"FileExtension": extension,
		"s":             fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	signed := signV4(creds.AccessKey, creds.SecretKey, creds.SessionTok, http.MethodGet, prepared.Data.UploadHost, query, "")

	applyReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+prepared.Data.UploadHost+"/?"+signed.canonicalQuery, nil)
	if err != nil {
		return nil, err
	}
	applyReq.Header.Set("X-Amz-Date", signed.amzDate)
	applyReq.Header.Set("x-amz-security-token", creds.SessionTok)
	applyReq.Header.Set("Authorization", signed.authorization)

	applyResp, err := c.do(applyReq, settings)
	if err != nil {
		return nil, err
	}
	applyRaw, err := io.ReadAll(io.LimitReader(applyResp.Body, 1<<20))
	applyResp.Body.Close()
	if err != nil {
		return nil, err
	}

	var applied struct {
		Result struct {
			UploadAddress struct {
				UploadHosts []string `json:"UploadHosts"`
				StoreInfos  []struct {
					StoreURI string `json:"StoreUri"`
					Auth     string `json:"Auth"`
				} `json:"StoreInfos"`
				SessionKey string `json:"SessionKey"`
			} `json:"UploadAddress"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(applyRaw, &applied); err != nil {
		return nil, fmt.Errorf("apply upload: %w", err)
	}
	address := applied.Result.UploadAddress
	if len(address.StoreInfos) == 0 || len(address.UploadHosts) == 0 {
		return nil, fmt.Errorf("apply upload rejected: %s", truncate(string(applyRaw), 200))
	}
	store := address.StoreInfos[0]

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+address.UploadHosts[0]+"/upload/v1/"+store.StoreURI, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	putReq.Header.Set("Authorization", store.Auth)
	putReq.Header.Set("Content-CRC32", crc32Hex(data))
	putReq.Header.Set("Content-Type", "application/octet-stream")

	putResp, err := c.do(putReq, settings)
	if err != nil {
		return nil, err
	}
	putRaw, err := io.ReadAll(io.LimitReader(putResp.Body, 1<<20))
	putResp.Body.Close()
	if err != nil {
		return nil, err
	}
	var put struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(putRaw, &put); err != nil {
		return nil, fmt.Errorf("image byte upload: %w", err)
	}
	if put.Code != 2000 {
		return nil, fmt.Errorf("image byte upload rejected: %s", truncate(string(putRaw), 200))
	}

	commitBody, _ := json.Marshal(map[string]string{"SessionKey": address.SessionKey})
	commitQuery := map[string]string{
		"Action":    "CommitImageUpload",
		"Version":   "2018-08-01",
		"ServiceId": prepared.Data.ServiceID,
	}
	commitSigned := signV4(creds.AccessKey, creds.SecretKey, creds.SessionTok, http.MethodPost,
		prepared.Data.UploadHost, commitQuery, string(commitBody))

	commitReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+prepared.Data.UploadHost+"/?"+commitSigned.canonicalQuery, bytes.NewReader(commitBody))
	if err != nil {
		return nil, err
	}
	commitReq.Header.Set("X-Amz-Date", commitSigned.amzDate)
	commitReq.Header.Set("x-amz-security-token", creds.SessionTok)
	commitReq.Header.Set("Authorization", commitSigned.authorization)
	commitReq.Header.Set("Content-Type", "application/json")

	commitResp, err := c.do(commitReq, settings)
	if err != nil {
		return nil, err
	}
	commitRaw, err := io.ReadAll(io.LimitReader(commitResp.Body, 1<<20))
	commitResp.Body.Close()
	if err != nil {
		return nil, err
	}

	var committed struct {
		Result struct {
			Results []struct {
				URI       string `json:"Uri"`
				URIStatus int    `json:"UriStatus"`
			} `json:"Results"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(commitRaw, &committed); err != nil {
		return nil, fmt.Errorf("commit upload: %w", err)
	}
	if len(committed.Result.Results) == 0 || committed.Result.Results[0].URIStatus != 2000 {
		return nil, fmt.Errorf("commit upload rejected: %s", truncate(string(commitRaw), 200))
	}

	return &UploadedImage{
		URI:    committed.Result.Results[0].URI,
		Name:   name,
		Width:  width,
		Height: height,
	}, nil
}

type signedRequest struct {
	amzDate        string
	authorization  string
	canonicalQuery string
}

// signV4 implements Volcengine's AWS-SigV4-compatible ImageX signing.
func signV4(ak, sk, token, method, host string, query map[string]string, body string) signedRequest {
	amzDate := time.Now().UTC().Format("20060102T150405Z")
	dateStamp := amzDate[:8]

	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, uriEncode(key)+"="+uriEncode(query[key]))
	}
	canonicalQuery := strings.Join(pairs, "&")

	headers := map[string]string{
		"host":                 host,
		"x-amz-date":           amzDate,
		"x-amz-security-token": token,
	}
	headerKeys := make([]string, 0, len(headers))
	for key := range headers {
		headerKeys = append(headerKeys, key)
	}
	sort.Strings(headerKeys)
	signedHeaders := strings.Join(headerKeys, ";")
	var canonicalHeaders strings.Builder
	for _, key := range headerKeys {
		canonicalHeaders.WriteString(key + ":" + headers[key] + "\n")
	}

	canonicalRequest := strings.Join([]string{
		method, "/", canonicalQuery, canonicalHeaders.String(), signedHeaders, sha256Hex(body),
	}, "\n")

	scope := dateStamp + "/us-east-1/imagex/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex(canonicalRequest),
	}, "\n")

	kSigning := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+sk), dateStamp), "us-east-1"), "imagex"), "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	return signedRequest{
		amzDate:        amzDate,
		authorization:  "AWS4-HMAC-SHA256 Credential=" + ak + "/" + scope + ", SignedHeaders=" + signedHeaders + ", Signature=" + signature,
		canonicalQuery: canonicalQuery,
	}
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func uriEncode(value string) string {
	var builder strings.Builder
	for _, char := range []byte(value) {
		switch {
		case (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.' || char == '~':
			builder.WriteByte(char)
		default:
			builder.WriteString(fmt.Sprintf("%%%02X", char))
		}
	}
	return builder.String()
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// Probe performs a minimal completion to validate a session cookie.
func (c *Client) Probe(ctx context.Context, cookie string) (time.Duration, error) {
	settings := c.settings()
	started := time.Now()
	_, err := c.Completion(ctx, Options{
		Cookie:    cookie,
		Text:      "ping",
		DeepThink: 0,
		Timeout:   settings.RequestTimeout(),
	})
	return time.Since(started), err
}

// HealthCheck is a lightweight reachability probe against the upstream host.
func (c *Client) HealthCheck(ctx context.Context) (time.Duration, error) {
	settings := c.settings()
	started := time.Now()
	req, err := c.newRequest(ctx, settings, http.MethodGet, strings.TrimRight(settings.Upstream.BaseURL, "/")+"/chat/", nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.do(req, settings)
	if err != nil {
		return time.Since(started), err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return time.Since(started), nil
}

var _ = config.Settings{}
