package ntfy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateDestinationRejectsUnsafeAddressesAndRoutes(t *testing.T) {
	client := NewClient(nil)
	client.Resolver = staticResolver{hosts: map[string][]net.IPAddr{
		"public.test":  {{IP: net.ParseIP("203.0.113.10")}},
		"private.test": {{IP: net.ParseIP("192.168.0.10")}},
		"mixed.test":   {{IP: net.ParseIP("192.168.0.10")}, {IP: net.ParseIP("169.254.169.254")}},
		"empty.test":   nil,
	}}

	if _, err := client.ValidateDestination(context.Background(), Destination{BaseURL: "https://public.test", Topic: "scout_alerts"}); err != nil {
		t.Fatalf("public HTTPS destination rejected: %v", err)
	}
	if _, err := client.ValidateDestination(context.Background(), Destination{BaseURL: "http://private.test", Topic: "scout_alerts", AllowPlainHTTP: true}); err != nil {
		t.Fatalf("private HTTP destination rejected: %v", err)
	}
	cases := []struct {
		name        string
		destination Destination
		want        error
	}{
		{name: "plain HTTP without opt in", destination: Destination{BaseURL: "http://private.test", Topic: "scout_alerts"}, want: ErrInvalidDestination},
		{name: "public plain HTTP", destination: Destination{BaseURL: "http://public.test", Topic: "scout_alerts", AllowPlainHTTP: true}, want: ErrAddressNotAllowed},
		{name: "mixed DNS answers", destination: Destination{BaseURL: "https://mixed.test", Topic: "scout_alerts"}, want: ErrAddressNotAllowed},
		{name: "empty DNS answer", destination: Destination{BaseURL: "https://empty.test", Topic: "scout_alerts"}, want: ErrAddressNotAllowed},
		{name: "query string", destination: Destination{BaseURL: "https://public.test/path?token=secret", Topic: "scout_alerts"}, want: ErrInvalidDestination},
		{name: "fragment", destination: Destination{BaseURL: "https://public.test/path#fragment", Topic: "scout_alerts"}, want: ErrInvalidDestination},
		{name: "embedded credentials", destination: Destination{BaseURL: "https://user:secret@public.test", Topic: "scout_alerts"}, want: ErrInvalidDestination},
		{name: "path traversal", destination: Destination{BaseURL: "https://public.test/a/../b", Topic: "scout_alerts"}, want: ErrInvalidDestination},
		{name: "reserved topic", destination: Destination{BaseURL: "https://public.test", Topic: "publish"}, want: ErrInvalidDestination},
		{name: "unsafe topic characters", destination: Destination{BaseURL: "https://public.test", Topic: "scout/alerts"}, want: ErrInvalidDestination},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.ValidateDestination(context.Background(), test.destination); !errors.Is(err, test.want) {
				t.Fatalf("validation error = %v, want %v", err, test.want)
			}
		})
	}

	if err := ValidateTopic("scout_alerts"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTopic("v1"); !errors.Is(err, ErrInvalidDestination) {
		t.Fatalf("reserved topic validation error = %v", err)
	}
}

func TestPublishUsesBearerTokenRedactsSecretsAndBoundsPayload(t *testing.T) {
	receiver := newTestNtfyReceiver(t, false, nil)
	client := NewClient(nil)
	destination := Destination{BaseURL: receiver.server.URL, Topic: "scout_alerts", Token: "secret-token", AllowPlainHTTP: true}
	message := Message{Title: "CPU high", Message: strings.Repeat("😀", 5000), Priority: 4, ClickURL: "https://scout.example.test/incidents/incident-1"}
	result, err := client.Publish(context.Background(), destination, message)
	if err != nil || !result.Accepted || result.RemoteID != "accepted-id" {
		t.Fatalf("publish result = %+v err=%v", result, err)
	}
	received := receiver.lastRequest()
	if received.Path != "/scout_alerts" || received.RawQuery != "" {
		t.Fatalf("publish route = %+v", received)
	}
	if received.Authorization != "Bearer secret-token" {
		t.Fatalf("authorization header = %q", received.Authorization)
	}
	if len(received.Body) > MaxPayloadBytes {
		t.Fatalf("payload size = %d, want <= %d", len(received.Body), MaxPayloadBytes)
	}
	if bytes.Contains(received.Body, []byte(destination.Token)) {
		t.Fatal("token leaked into request body")
	}
	if strings.Contains(destination.String(), destination.Token) || destination.MaskedTopic() == destination.Topic {
		t.Fatalf("destination was not redacted: %s", destination)
	}
	var payload map[string]any
	if err := json.Unmarshal(received.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["topic"] != destination.Topic || payload["priority"] != float64(message.Priority) {
		t.Fatalf("publish payload = %+v", payload)
	}
}

func TestPublishRequiresTLSUnlessPrivateHTTPIsExplicitlyAllowed(t *testing.T) {
	receiver := newTestNtfyReceiver(t, false, nil)
	client := NewClient(nil)
	_, err := client.Publish(context.Background(), Destination{BaseURL: receiver.server.URL, Topic: "scout_alerts"}, Message{Message: "test"})
	if !errors.Is(err, ErrInvalidDestination) {
		t.Fatalf("plain HTTP without opt-in error = %v", err)
	}
	if receiver.requestCount.Load() != 0 {
		t.Fatal("plain HTTP request was sent without explicit opt-in")
	}
}

func TestPublishUsesTLSAndRejectsRedirects(t *testing.T) {
	tlsReceiver := newTestNtfyReceiver(t, true, nil)
	tlsClient := NewClient(tlsReceiver.server.Client())
	result, err := tlsClient.Publish(context.Background(), Destination{BaseURL: tlsReceiver.server.URL, Topic: "scout_alerts"}, Message{Message: "secure"})
	if err != nil || !result.Accepted {
		t.Fatalf("TLS publish result = %+v err=%v", result, err)
	}

	target := newTestNtfyReceiver(t, false, nil)
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.server.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	client := NewClient(nil)
	_, err = client.Publish(context.Background(), Destination{BaseURL: redirect.URL, Topic: "scout_alerts", AllowPlainHTTP: true}, Message{Message: "redirect"})
	if !errors.Is(err, ErrRedirect) {
		t.Fatalf("redirect error = %v", err)
	}
	if target.requestCount.Load() != 0 {
		t.Fatal("redirect target received a request")
	}
}

func TestPublishClassifiesTimeoutResponseLossAndHTTPStatus(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		t.Cleanup(server.Close)
		client := NewClient(&http.Client{Timeout: 25 * time.Millisecond})
		_, err := client.Publish(context.Background(), Destination{BaseURL: server.URL, Topic: "scout_alerts", AllowPlainHTTP: true}, Message{Message: "timeout"})
		if !errors.Is(err, ErrTimeout) || errors.Is(err, ErrResponseLost) {
			t.Fatalf("timeout error = %v", err)
		}
	})

	t.Run("response loss after receiver accepts", func(t *testing.T) {
		receiver := newTestNtfyReceiver(t, false, nil)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client := NewClient(&http.Client{Transport: responseLossTransport{base: transport}})
		_, err := client.Publish(context.Background(), Destination{BaseURL: receiver.server.URL, Topic: "scout_alerts", AllowPlainHTTP: true}, Message{Message: "lost response"})
		if !errors.Is(err, ErrResponseLost) || !errors.Is(err, ErrRetryable) {
			t.Fatalf("response-loss error = %v", err)
		}
		if receiver.requestCount.Load() != 1 {
			t.Fatalf("receiver count = %d", receiver.requestCount.Load())
		}
	})

	t.Run("retryable and permanent statuses", func(t *testing.T) {
		retryReceiver := newTestNtfyReceiver(t, false, func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Retry-After", "2")
			writer.WriteHeader(http.StatusTooManyRequests)
		})
		client := NewClient(nil)
		result, err := client.Publish(context.Background(), Destination{BaseURL: retryReceiver.server.URL, Topic: "scout_alerts", AllowPlainHTTP: true}, Message{Message: "retry"})
		if !errors.Is(err, ErrRetryable) || result.StatusCode != http.StatusTooManyRequests || result.RetryAfter != 2*time.Second {
			t.Fatalf("retry result = %+v err=%v", result, err)
		}

		permanentReceiver := newTestNtfyReceiver(t, false, func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusUnauthorized) })
		result, err = client.Publish(context.Background(), Destination{BaseURL: permanentReceiver.server.URL, Topic: "scout_alerts", AllowPlainHTTP: true}, Message{Message: "permanent"})
		if !errors.Is(err, ErrPermanent) || errors.Is(err, ErrRetryable) || result.StatusCode != http.StatusUnauthorized {
			t.Fatalf("permanent result = %+v err=%v", result, err)
		}
	})
}

type staticResolver struct {
	hosts map[string][]net.IPAddr
}

func (resolver staticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return resolver.hosts[host], nil
}

type capturedNtfyRequest struct {
	Path          string
	RawQuery      string
	Authorization string
	Body          []byte
}

type testNtfyReceiver struct {
	server       *httptest.Server
	requestCount atomic.Int32
	mu           sync.Mutex
	requests     []capturedNtfyRequest
}

func newTestNtfyReceiver(t *testing.T, tls bool, handler http.HandlerFunc) *testNtfyReceiver {
	t.Helper()
	receiver := &testNtfyReceiver{}
	if handler == nil {
		handler = receiver.handle
	}
	if tls {
		receiver.server = httptest.NewTLSServer(handler)
	} else {
		receiver.server = httptest.NewServer(handler)
	}
	t.Cleanup(receiver.server.Close)
	return receiver
}

func (receiver *testNtfyReceiver) handle(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	receiver.requestCount.Add(1)
	receiver.mu.Lock()
	receiver.requests = append(receiver.requests, capturedNtfyRequest{Path: request.URL.Path, RawQuery: request.URL.RawQuery, Authorization: request.Header.Get("Authorization"), Body: body})
	receiver.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write([]byte(`{"id":"accepted-id"}`))
}

func (receiver *testNtfyReceiver) lastRequest() capturedNtfyRequest {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	return receiver.requests[len(receiver.requests)-1]
}

type responseLossTransport struct {
	base http.RoundTripper
}

func (transport responseLossTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return response, err
	}
	_ = response.Body.Close()
	response.Body = responseLossBody{}
	return response, nil
}

type responseLossBody struct{}

func (responseLossBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func (responseLossBody) Close() error { return nil }
