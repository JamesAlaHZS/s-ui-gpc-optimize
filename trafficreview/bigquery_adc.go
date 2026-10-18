package trafficreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMetadataTokenURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"
	defaultBigQueryBaseURL  = "https://bigquery.googleapis.com/bigquery/v2"
)

// ADCBigQueryClient queries BigQuery Standard usage cost export using VM
// Application Default Credentials from the GCE metadata server. It never
// accepts or stores long-lived service account JSON private keys.
type ADCBigQueryClient struct {
	HTTPClient       *http.Client
	MetadataTokenURL string
	BigQueryBaseURL  string
}

func NewADCBigQueryClient() *ADCBigQueryClient {
	return &ADCBigQueryClient{
		HTTPClient:       &http.Client{Timeout: 30 * time.Second},
		MetadataTokenURL: defaultMetadataTokenURL,
		BigQueryBaseURL:  defaultBigQueryBaseURL,
	}
}

func (c *ADCBigQueryClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *ADCBigQueryClient) token(ctx context.Context) (string, error) {
	u := c.MetadataTokenURL
	if u == "" {
		u = defaultMetadataTokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch VM ADC token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("VM ADC metadata status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || strings.TrimSpace(tok.AccessToken) == "" {
		return "", errors.New("invalid VM ADC token payload")
	}
	return tok.AccessToken, nil
}

func (c *ADCBigQueryClient) tableStorageBytes(ctx context.Context, tok string, cfg BillingSyncConfig) (int64, error) {
	base := strings.TrimRight(c.BigQueryBaseURL, "/")
	if base == "" {
		base = defaultBigQueryBaseURL
	}
	u := fmt.Sprintf("%s/projects/%s/datasets/%s/tables/%s",
		base,
		url.PathEscape(cfg.DatasetProjectID),
		url.PathEscape(cfg.DatasetID),
		url.PathEscape(cfg.TableID),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("BigQuery table metadata status %d: %s", resp.StatusCode, clip(string(body), 256))
	}
	var meta struct {
		NumBytes string `json:"numBytes"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return 0, err
	}
	if meta.NumBytes == "" {
		return 0, errors.New("BigQuery table metadata omitted numBytes")
	}
	return strconv.ParseInt(meta.NumBytes, 10, 64)
}

type bqQueryRequest struct {
	Query              string `json:"query"`
	UseLegacySQL       bool   `json:"useLegacySql"`
	DryRun             bool   `json:"dryRun,omitempty"`
	MaximumBytesBilled string `json:"maximumBytesBilled,omitempty"`
	MaxResults         int    `json:"maxResults,omitempty"`
	TimeoutMs          int    `json:"timeoutMs,omitempty"`
}

type bqQueryResponse struct {
	PageToken    string `json:"pageToken"`
	TotalRows    string `json:"totalRows"`
	JobReference struct {
		ProjectID string `json:"projectId"`
		JobID     string `json:"jobId"`
		Location  string `json:"location"`
	} `json:"jobReference"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	JobComplete         bool   `json:"jobComplete"`
	TotalBytesProcessed string `json:"totalBytesProcessed"`
	TotalBytesBilled    string `json:"totalBytesBilled"`
	Rows                []struct {
		F []struct {
			V any `json:"v"`
		} `json:"f"`
	} `json:"rows"`
}

func (c *ADCBigQueryClient) DryRun(ctx context.Context, query string, cfg BillingSyncConfig) (BigQueryDryRunResult, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return BigQueryDryRunResult{}, err
	}
	storageBytes, err := c.tableStorageBytes(ctx, tok, cfg)
	if err != nil {
		return BigQueryDryRunResult{}, err
	}
	resp, err := c.runQueryRequest(ctx, tok, cfg.QueryProjectID, bqQueryRequest{
		Query:        query,
		UseLegacySQL: false,
		DryRun:       true,
	})
	if err != nil {
		return BigQueryDryRunResult{}, err
	}
	var est int64
	if resp.TotalBytesProcessed == "" {
		return BigQueryDryRunResult{}, errors.New("BigQuery dry run omitted scan estimate")
	} else {
		est, err = strconv.ParseInt(resp.TotalBytesProcessed, 10, 64)
		if err != nil {
			return BigQueryDryRunResult{}, err
		}
	}
	return BigQueryDryRunResult{
		EstimatedBytes:              est,
		DatasetStorageBytes:         storageBytes,
		ExistingAccountMonthlyBytes: cfg.ExistingAccountMonthlyBytes,
	}, nil
}

func (c *ADCBigQueryClient) Query(ctx context.Context, query string, cfg BillingSyncConfig) (BigQueryQueryResult, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return BigQueryQueryResult{}, err
	}
	resp, err := c.runQueryRequest(ctx, tok, cfg.QueryProjectID, bqQueryRequest{
		Query:              query,
		UseLegacySQL:       false,
		DryRun:             false,
		MaximumBytesBilled: strconv.FormatInt(cfg.MaxBytesPerQuery, 10),
		MaxResults:         2000,
		TimeoutMs:          25000,
	})
	if err != nil {
		return BigQueryQueryResult{}, err
	}
	// jobs.query returns only the first page and may return before completion.
	job := resp.JobReference
	for !resp.JobComplete {
		select {
		case <-ctx.Done():
			return BigQueryQueryResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
		resp, err = c.queryResults(ctx, tok, job.ProjectID, job.JobID, job.Location, "")
		if err != nil {
			return BigQueryQueryResult{}, err
		}
	}
	// Missing billing statistics are charged conservatively to the local ledger.
	billed := cfg.MaxBytesPerQuery
	if resp.TotalBytesBilled != "" {
		billed, err = strconv.ParseInt(resp.TotalBytesBilled, 10, 64)
		if err != nil || billed < 0 {
			return BigQueryQueryResult{}, errors.New("invalid BigQuery billed byte count")
		}
	}
	allRows := resp.Rows
	totalRows := resp.TotalRows
	seenTokens := map[string]bool{}
	for resp.PageToken != "" {
		if seenTokens[resp.PageToken] || len(allRows) > 10000 {
			return BigQueryQueryResult{}, errors.New("BigQuery result pagination exceeds bounded import")
		}
		seenTokens[resp.PageToken] = true
		resp, err = c.queryResults(ctx, tok, job.ProjectID, job.JobID, job.Location, resp.PageToken)
		if err != nil {
			return BigQueryQueryResult{}, err
		}
		allRows = append(allRows, resp.Rows...)
	}
	if len(allRows) > 10000 {
		return BigQueryQueryResult{}, errors.New("BigQuery result exceeds 10000 rows")
	}
	if totalRows != "" {
		n, err := strconv.ParseInt(totalRows, 10, 64)
		if err != nil || n != int64(len(allRows)) {
			return BigQueryQueryResult{}, errors.New("incomplete BigQuery result; refusing partial daily replacement")
		}
	}
	rows := make([]StandardExportRow, 0, len(allRows))
	for _, r := range allRows {
		if len(r.F) < 11 {
			return BigQueryQueryResult{}, errors.New("unexpected BigQuery Standard export column count")
		}
		cell := func(i int) string {
			if r.F[i].V == nil {
				return ""
			}
			return fmt.Sprint(r.F[i].V)
		}
		expSec, err := ParseExportTimestamp(cell(10))
		if err != nil {
			return BigQueryQueryResult{}, err
		}
		rows = append(rows, StandardExportRow{
			Date:         cell(0),
			ProjectID:    cell(1),
			SKUID:        cell(2),
			Description:  cell(3),
			InvoiceMonth: cell(4),
			UsageUnit:    cell(5),
			UsageAmount:  cell(6),
			Cost:         cell(7),
			Credits:      cell(8),
			Currency:     cell(9),
			ExportTime:   expSec,
		})
	}
	return BigQueryQueryResult{BilledBytes: billed, Rows: rows}, nil
}

func (c *ADCBigQueryClient) queryResults(ctx context.Context, tok, project, job, location, page string) (bqQueryResponse, error) {
	if project == "" || job == "" {
		return bqQueryResponse{}, errors.New("BigQuery omitted job reference for unfinished/paginated result")
	}
	base := strings.TrimRight(c.BigQueryBaseURL, "/")
	if base == "" {
		base = defaultBigQueryBaseURL
	}
	params := url.Values{"maxResults": {"2000"}, "timeoutMs": {"1000"}}
	if location != "" {
		params.Set("location", location)
	}
	if page != "" {
		params.Set("pageToken", page)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/projects/"+url.PathEscape(project)+"/queries/"+url.PathEscape(job)+"?"+params.Encode(), nil)
	if err != nil {
		return bqQueryResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return c.readQueryResponse(req)
}

func (c *ADCBigQueryClient) runQueryRequest(ctx context.Context, tok, projectID string, payload bqQueryRequest) (bqQueryResponse, error) {
	base := strings.TrimRight(c.BigQueryBaseURL, "/")
	if base == "" {
		base = defaultBigQueryBaseURL
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return bqQueryResponse{}, err
	}
	u := fmt.Sprintf("%s/projects/%s/queries", base, url.PathEscape(projectID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return bqQueryResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	return c.readQueryResponse(req)
}

func (c *ADCBigQueryClient) readQueryResponse(req *http.Request) (bqQueryResponse, error) {
	resp, err := c.client().Do(req)
	if err != nil {
		return bqQueryResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return bqQueryResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return bqQueryResponse{}, fmt.Errorf("BigQuery query status %d: %s", resp.StatusCode, clip(string(body), 256))
	}
	var out bqQueryResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return bqQueryResponse{}, err
	}
	if len(out.Errors) > 0 {
		return bqQueryResponse{}, fmt.Errorf("BigQuery query reported errors: %s", clip(out.Errors[0].Message, 256))
	}
	return out, nil
}
