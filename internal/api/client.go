package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/buildkite/zstash/internal/trace"
	"github.com/google/go-querystring/query"
	"github.com/klauspost/compress/gzhttp"
	"github.com/rs/zerolog/log"
	otel "go.opentelemetry.io/otel/trace"
)

const (
	CacheRegistryNotFound = "Cache registry not found"
	CacheEntryNotFound    = "Cache entry not found"
)

var (
	ErrCacheEntryNotFound = errors.New("cache entry not found")
)

// isJSONContentType checks if the content type indicates JSON response
// Handles cases like "application/json" and "application/json; charset=utf-8"
func isJSONContentType(contentType string) bool {
	// Remove any leading/trailing whitespace and convert to lowercase
	contentType = strings.TrimSpace(strings.ToLower(contentType))

	// Check if it starts with "application/json"
	return strings.HasPrefix(contentType, "application/json")
}

type Client struct {
	client   *http.Client
	endpoint string
	slug     string
}

type CacheCreateReq struct {
	Store        string   `json:"store"` // The store used for the cache entry
	Key          string   `json:"key"`
	FallbackKeys []string `json:"fallback_keys"`
	Compression  string   `json:"compression"`
	FileSize     int      `json:"file_size"`
	Digest       string   `json:"digest"`
	Paths        []string `json:"paths"`
	Platform     string   `json:"platform"`
	Pipeline     string   `json:"pipeline"`
	Branch       string   `json:"branch"`
	Organization string   `json:"owner"`
}

type CacheRetrieveReq struct {
	Key          string `url:"key"`
	Branch       string `url:"branch"`
	FallbackKeys string `url:"fallback_keys"`
}

type CacheRetrieveResp struct {
	Store                string    `json:"store"`    // The store used for the cache entry
	Key                  string    `json:"key"`      // The key of the cache entry, we MUST use this in rest of the restore process to cater for fallbacks
	Fallback             bool      `json:"fallback"` // Indicates if this is a fallback cache entry
	ExpiresAt            time.Time `json:"expires_at"`
	Multipart            bool      `json:"multipart"`
	DownloadInstructions []string  `json:"download_instructions"`
	Message              string    `json:"message"`
}

type CacheCreateResp struct {
	UploadID           string   `json:"upload_id"`
	Multipart          bool     `json:"multipart"`
	UploadInstructions []string `json:"upload_instructions"`
	Message            string   `json:"message"`
}

type CachePeekReq struct {
	Key    string `url:"key"`
	Branch string `url:"branch"`
}

type CachePeekResp struct {
	Store       string    `json:"store"` // The store used for the cache entry
	Digest      string    `json:"digest"`
	ExpiresAt   time.Time `json:"expires_at"`
	Compression string    `json:"compression"`
	Message     string    `json:"message"`
}

type CacheRegistryResp struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Store string `json:"store"` // The store used for the cache registry
}

type CacheCommitReq struct {
	UploadID string `json:"upload_id"`
}
type CacheCommitResp struct {
	Message string `json:"message"`
}

func NewClient(ctx context.Context, version, endpoint, slug, token string) Client {
	client := &http.Client{}

	client.Transport = gzhttp.Transport(roundTripperFunc(
		func(req *http.Request) (*http.Response, error) {
			req = req.Clone(req.Context())
			req.Header.Set("Authorization", fmt.Sprintf("Token %s", token))
			req.Header.Set("User-Agent", fmt.Sprint("zstash/", version))
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Encoding", "gzip, deflate, br")
			return http.DefaultTransport.RoundTrip(req)
		}),
	)

	return Client{client: client, slug: slug, endpoint: endpoint}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func (c Client) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req)
}

// MessageGetter interface for types that have a Message field
type MessageGetter interface {
	GetMessage() string
}

// GetMessage returns the message for CachePeekResp
func (r CachePeekResp) GetMessage() string {
	return r.Message
}

// GetMessage returns the message for CacheRetrieveResp
func (r CacheRetrieveResp) GetMessage() string {
	return r.Message
}

// handleCacheResponse handles common cache response patterns and error handling using generics
func handleCacheResponse[T MessageGetter](span otel.Span, res *http.Response, resp T) (T, bool, error) {
	// Assert content type is application/json for successful responses
	if res.StatusCode == http.StatusOK {
		contentType := res.Header.Get("Content-Type")
		if !isJSONContentType(contentType) {
			return resp, false, trace.NewError(span, "unexpected content type: %s", contentType)
		}
		return resp, true, nil
	}

	switch res.StatusCode {
	case http.StatusNotFound:
		switch resp.GetMessage() {
		case CacheEntryNotFound:
			return resp, false, nil
		case CacheRegistryNotFound:
			return resp, false, trace.NewError(span, "cache registry not found: %s", res.Status)
		}
		return resp, false, trace.NewError(span, "not found: %s", res.Status)
	case http.StatusBadRequest:
		return resp, false, trace.NewError(span, "bad request: %s", res.Status)
	default:
		return resp, false, trace.NewError(span, "request failed with status: %s", res.Status)
	}
}

func (c Client) CacheRegistry(ctx context.Context) (CacheRegistryResp, error) {
	ctx, span := trace.Start(ctx, "Client.CacheRegistry")
	defer span.End()

	var resp CacheRegistryResp

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s", c.endpoint, c.slug))
	if err != nil {
		return resp, trace.NewError(span, "failed to parse url: %w", err)
	}

	res, resp, err := doRequest[any, CacheRegistryResp](ctx, c.client, http.MethodGet, u.String(), nil)
	if err != nil {
		return resp, trace.NewError(span, "failed to do request: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return resp, trace.NewError(span, "failed to get cache registry: %s", res.Status)
	}

	// Assert content type is application/json
	contentType := res.Header.Get("Content-Type")
	if !isJSONContentType(contentType) {
		return resp, trace.NewError(span, "unexpected content type: %s", contentType)
	}

	return resp, nil
}

func (c Client) CachePeekExists(ctx context.Context, create CachePeekReq) (CachePeekResp, bool, error) {
	ctx, span := trace.Start(ctx, "Client.CachePeekExists")
	defer span.End()

	var resp CachePeekResp

	queryParams, err := query.Values(create)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to marshal query params: %w", err)
	}

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s/peek", c.endpoint, c.slug))
	if err != nil {
		return resp, false, trace.NewError(span, "failed to parse url: %w", err)
	}

	u.RawQuery = queryParams.Encode()

	res, resp, err := doRequest[any, CachePeekResp](ctx, c.client, http.MethodGet, u.String(), nil)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to do request: %w", err)
	}

	return handleCacheResponse(span, res, resp)
}

func (c Client) CacheCommit(ctx context.Context, commit CacheCommitReq) (CacheCommitResp, error) {
	ctx, span := trace.Start(ctx, "Client.CacheCommit")
	defer span.End()

	var resp CacheCommitResp

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s/commit", c.endpoint, c.slug))
	if err != nil {
		return resp, trace.NewError(span, "failed to parse url: %w", err)
	}

	res, resp, err := doRequest[CacheCommitReq, CacheCommitResp](ctx, c.client, http.MethodPut, u.String(), &commit)
	if err != nil {
		return resp, trace.NewError(span, "failed to do request: %w", err)
	}

	log.Info().Fields(map[string]any{
		"resp": resp,
	}).Msg("Cache committed with the following parameters")

	if res.StatusCode != http.StatusOK {
		return resp, trace.NewError(span, "failed to commit: %s", res.Status)
	}

	return resp, nil
}

func (c Client) CacheCreate(ctx context.Context, create CacheCreateReq) (CacheCreateResp, error) {
	ctx, span := trace.Start(ctx, "Client.CacheCreate")
	defer span.End()

	var resp CacheCreateResp

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s/store", c.endpoint, c.slug))
	if err != nil {
		return resp, trace.NewError(span, "failed to parse url: %w", err)
	}

	res, resp, err := doRequest[CacheCreateReq, CacheCreateResp](ctx, c.client, http.MethodPut, u.String(), &create)
	if err != nil {
		return resp, trace.NewError(span, "failed to do request: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return resp, trace.NewError(span, "failed to save: %s", res.Status)
	}

	return resp, nil
}

func (c Client) CacheRetrieve(ctx context.Context, retrieve CacheRetrieveReq) (CacheRetrieveResp, bool, error) {
	ctx, span := trace.Start(ctx, "Client.CacheRetrieve")
	defer span.End()

	var resp CacheRetrieveResp

	queryParams, err := query.Values(retrieve)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to marshal query params: %w", err)
	}

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s/retrieve", c.endpoint, c.slug))
	if err != nil {
		return resp, false, trace.NewError(span, "failed to parse url: %w", err)
	}

	u.RawQuery = queryParams.Encode()

	log.Info().Str("url", u.String()).Msg("Cache retrieve URL")

	res, resp, err := doRequest[CacheRetrieveReq, CacheRetrieveResp](ctx, c.client, http.MethodGet, u.String(), nil)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to do request: %w", err)
	}

	log.Info().Fields(map[string]any{
		"resp":   resp,
		"status": res.Status,
		"code":   res.StatusCode,
	}).Msg("Cache retrieved with the following parameters")

	return handleCacheResponse(span, res, resp)
}

func doRequest[T any, V any](ctx context.Context, client *http.Client, method string, url string, body *T) (res *http.Response, resp V, err error) {
	ctx, span := trace.Start(ctx, "DoRequest")
	defer span.End()

	var bodyrdr io.Reader = http.NoBody

	// ONLY set body if method is PUT or POST
	if method == http.MethodPut || method == http.MethodPost {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, resp, trace.NewError(span, "failed to marshal request body: %w", err)
		}
		bodyrdr = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyrdr)
	if err != nil {
		return nil, resp, trace.NewError(span, "failed to create request: %w", err)
	}

	res, err = client.Do(req)
	if err != nil {
		return nil, resp, trace.NewError(span, "failed to do request: %w", err)
	}

	// Don't return error for expected status codes that are handled by callers
	if res.StatusCode < 200 || res.StatusCode >= 500 {
		return res, resp, trace.NewError(span, "request failed with status: %s", res.Status)
	}

	if res.Body == http.NoBody {
		return res, resp, nil
	}

	defer func() {
		if res != nil && res.Body != nil {
			_ = res.Body.Close()
		}
	}()

	// Assert content type is application/json
	contentType := res.Header.Get("Content-Type")
	if !isJSONContentType(contentType) {
		return res, resp, trace.NewError(span, "unexpected content type: %s", contentType)
	}

	// read the response body
	if err = json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, resp, trace.NewError(span, "failed to decode response body: %w", err)
	}

	return res, resp, nil
}
