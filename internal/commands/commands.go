package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"net/http"
	"net/url"

	"github.com/buildkite/zstash/internal/trace"
	"github.com/google/go-querystring/query"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	CacheRegistryNotFound = "Cache registry not found"
	CacheEntryNotFound    = "Cache entry not found"
)

type Globals struct {
	Debug   bool
	Version string
}

type Client struct {
	client   *http.Client
	endpoint string
	slug     string
}

type CacheCreateReq struct {
	Key          string   `json:"key"`
	Compression  string   `json:"compression"`
	FileSize     int      `json:"file_size"`
	Digest       string   `json:"digest"`
	Paths        []string `json:"paths"`
	Platform     string   `json:"platform"`
	Pipeline     string   `json:"pipeline"`
	Branch       string   `json:"branch"`
	Organization string   `json:"owner"`
}

type CacheCreateResp struct {
	UploadID           string   `json:"upload_id"`
	Multipart          bool     `json:"multipart"`
	UploadInstructions []string `json:"upload_instructions"`
	Message            string   `json:"message"`
}

type CachePeekReq struct {
	Key string `url:"key"`
}

type CachePeekResp struct {
	Message string `json:"message"`
}

type CacheCommitReq struct {
	UploadID string `json:"upload_id"`
}
type CacheCommitResp struct {
	Message string `json:"message"`
}

func NewClient(ctx context.Context, endpoint, slug, token string) (Client, error) {
	client := &http.Client{}

	if token != "" {

		transport := http.DefaultTransport

		client.Transport = otelhttp.NewTransport(roundTripperFunc(
			func(req *http.Request) (*http.Response, error) {
				req = req.Clone(req.Context())
				req.Header.Set("Authorization", fmt.Sprintf("Token %s", token))
				return transport.RoundTrip(req)
			},
		))
	}

	return Client{client: client, slug: slug, endpoint: endpoint}, nil
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func (c Client) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req)
}

func (c Client) CachePeekExists(ctx context.Context, create CachePeekReq) (CachePeekResp, bool, error) {
	ctx, span := trace.Start(ctx, "Client.CachePeekExists")
	defer span.End()

	// body, err := json.Marshal(&create)
	// if err != nil {
	// 	return resp, trace.NewError(span, "failed to marshal request body: %w", err)
	// }
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "zstash")

	res, err := c.Do(req)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to do request: %w", err)
	}
	defer res.Body.Close()

	// read the response body
	resp = CachePeekResp{}
	if err = json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return resp, false, trace.NewError(span, "failed to decode response body: %w", err)
	}

	switch res.StatusCode {
	case http.StatusOK:
		return resp, true, nil
	case http.StatusNotFound:
		if resp.Message == CacheEntryNotFound {
			return resp, false, nil
		}
		if resp.Message == CacheRegistryNotFound {
			return resp, false, trace.NewError(span, "cache registry not found: %s", res.Status)
		}
		return resp, false, trace.NewError(span, "not found: %s", res.Status)
	case http.StatusBadRequest:
		return resp, false, trace.NewError(span, "bad request: %s", res.Status) // TODO handle this better
	default:
		return resp, false, trace.NewError(span, "failed to peek unknown status: %s", res.Status)
	}
}

func (c Client) CacheCreate(ctx context.Context, create CacheCreateReq) (CacheCreateResp, error) {
	ctx, span := trace.Start(ctx, "Client.CacheCreate")
	defer span.End()

	var resp CacheCreateResp

	body, err := json.Marshal(&create)
	if err != nil {
		return resp, trace.NewError(span, "failed to marshal request body: %w", err)
	}

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s/store", c.endpoint, c.slug))
	if err != nil {
		return resp, trace.NewError(span, "failed to parse url: %w", err)
	}

	// /v3/organizations/:organization_id/cache_registry/store
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		return resp, trace.NewError(span, "failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "zstash")

	res, err := c.Do(req)
	if err != nil {
		return resp, trace.NewError(span, "failed to do request: %w", err)
	}
	defer res.Body.Close()

	err = json.NewDecoder(res.Body).Decode(&resp)
	if err != nil {
		return resp, trace.NewError(span, "failed to decode response body: %w", err)
	}

	log.Info().Fields(map[string]any{
		"resp": resp,
	}).Msg("Cache created with the following parameters")

	if res.StatusCode != http.StatusOK {
		return resp, trace.NewError(span, "failed to save: %s", res.Status)
	}

	return resp, nil
}

func (c Client) CacheCommit(ctx context.Context, commit CacheCommitReq) (CacheCommitResp, error) {
	ctx, span := trace.Start(ctx, "Client.CacheCommit")
	defer span.End()

	var resp CacheCommitResp

	body, err := json.Marshal(&commit)
	if err != nil {
		return resp, trace.NewError(span, "failed to marshal request body: %w", err)
	}

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s/commit", c.endpoint, c.slug))
	if err != nil {
		return resp, trace.NewError(span, "failed to parse url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		return resp, trace.NewError(span, "failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "zstash")

	res, err := c.Do(req)
	if err != nil {
		return resp, trace.NewError(span, "failed to do request: %w", err)
	}
	defer res.Body.Close()
	err = json.NewDecoder(res.Body).Decode(&resp)
	if err != nil {
		return resp, trace.NewError(span, "failed to decode response body: %w", err)
	}

	log.Info().Fields(map[string]any{
		"resp": resp,
	}).Msg("Cache committed with the following parameters")

	if res.StatusCode != http.StatusOK {
		return resp, trace.NewError(span, "failed to commit: %s", res.Status)
	}

	return resp, nil
}

type CacheRetrieveReq struct {
	Key string `url:"key"`
}

type CacheRetrieveResp struct {
	Multipart            bool     `json:"multipart"`
	DownloadInstructions []string `json:"download_instructions"`
	Message              string   `json:"message"`
}

func (c Client) CacheRetrieve(ctx context.Context, create CacheRetrieveReq) (CacheRetrieveResp, bool, error) {
	ctx, span := trace.Start(ctx, "Client.CacheRetrieve")
	defer span.End()

	var resp CacheRetrieveResp

	queryParams, err := query.Values(create)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to marshal query params: %w", err)
	}

	u, err := url.Parse(fmt.Sprintf("%s/cache_registries/%s/retrieve", c.endpoint, c.slug))
	if err != nil {
		return resp, false, trace.NewError(span, "failed to parse url: %w", err)
	}

	u.RawQuery = queryParams.Encode()

	log.Info().Str("url", u.String()).Msg("Cache retrieve URL")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "zstash")

	res, err := c.Do(req)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to do request: %w", err)
	}
	defer res.Body.Close()

	err = json.NewDecoder(res.Body).Decode(&resp)
	if err != nil {
		return resp, false, trace.NewError(span, "failed to decode response body: %w", err)
	}

	log.Info().Fields(map[string]any{
		"resp": resp,
	}).Msg("Cache retrieved with the following parameters")

	switch res.StatusCode {
	case http.StatusOK:
		return resp, true, nil
	case http.StatusNotFound:
		if resp.Message == CacheEntryNotFound {
			return resp, false, nil
		}
		if resp.Message == CacheRegistryNotFound {
			return resp, false, trace.NewError(span, "cache registry not found: %s", res.Status)
		}
		return resp, false, trace.NewError(span, "not found: %s", res.Status)
	case http.StatusBadRequest:
		return resp, false, trace.NewError(span, "bad request: %s", res.Status) // TODO handle this better
	default:
		return resp, false, trace.NewError(span, "failed to retrieve unknown status: %s", res.Status)
	}

}
