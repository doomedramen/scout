package enrollment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ClaimedJob struct {
	JobID             string `json:"jobId"`
	DeviceID          string `json:"deviceId"`
	ScopeID           string `json:"scopeId"`
	ScopeRevision     int64  `json:"scopeRevision"`
	Epoch             int64  `json:"epoch"`
	Deadline          string `json:"deadline"`
	Destination       string `json:"destination"`
	TrustRef          string `json:"trustRef"`
	CredentialGrantID string `json:"credentialGrantId"`
	ReleaseID         string `json:"releaseId"`
}

type RedeemedCredential struct {
	Secret     string
	AuthMethod string
	Username   string
}

type RemoteWorker struct {
	ServerURL string
	Token     string
	Client    *http.Client
}

func (w *RemoteWorker) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}
	return http.DefaultClient
}

func (w *RemoteWorker) endpoint(path string) string {
	return strings.TrimRight(w.ServerURL, "/") + "/worker/v1" + path
}

func (w *RemoteWorker) do(ctx context.Context, method, path string, body any, output any) (int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, w.endpoint(path), reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+w.Token)
	response, err := w.client().Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("worker API returned HTTP %d", response.StatusCode)
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}

func (w *RemoteWorker) Claim(ctx context.Context) (*ClaimedJob, error) {
	var result ClaimedJob
	status, err := w.do(ctx, http.MethodPost, "/claim", map[string]any{}, &result)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if status == http.StatusNotFound || status == http.StatusConflict {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if result.JobID == "" || result.Epoch < 1 {
		return nil, errors.New("worker claim omitted lease")
	}
	return &result, nil
}

func (w *RemoteWorker) Renew(ctx context.Context, job ClaimedJob) error {
	_, err := w.do(ctx, http.MethodPost, "/jobs/"+job.JobID+"/renew", map[string]any{"epoch": job.Epoch}, nil)
	return err
}

func (w *RemoteWorker) Progress(ctx context.Context, job ClaimedJob, state string, result map[string]string) error {
	_, err := w.do(ctx, http.MethodPost, "/jobs/"+job.JobID+"/progress", map[string]any{"epoch": job.Epoch, "state": state, "result": result}, nil)
	return err
}

func (w *RemoteWorker) Redeem(ctx context.Context, job ClaimedJob) (RedeemedCredential, error) {
	var result struct {
		Credential string `json:"credential"`
		AuthMethod string `json:"authMethod"`
		Username   string `json:"username"`
	}
	_, err := w.do(ctx, http.MethodPost, "/grants/"+job.CredentialGrantID+"/redeem", map[string]any{}, &result)
	if err != nil {
		return RedeemedCredential{}, err
	}
	if result.Credential == "" {
		return RedeemedCredential{}, errors.New("worker credential grant was empty")
	}
	return RedeemedCredential{Secret: result.Credential, AuthMethod: result.AuthMethod, Username: result.Username}, nil
}

func (w *RemoteWorker) Confirmed(ctx context.Context, job ClaimedJob) (bool, error) {
	var result struct {
		Confirmed bool `json:"confirmed"`
	}
	_, err := w.do(ctx, http.MethodGet, "/jobs/"+job.JobID+"/confirmation", nil, &result)
	return result.Confirmed, err
}
