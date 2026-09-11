package ntfy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	MaxPayloadBytes = 4096
	maxTokenBytes   = 4096
	maxURLBytes     = 2048
	maxTitleBytes   = 256
	maxClickBytes   = 2048
	maxRetryAfter   = 24 * time.Hour
)

var (
	ErrInvalidDestination = errors.New("invalid ntfy destination")
	ErrAddressNotAllowed  = errors.New("ntfy destination address is not allowed")
	ErrRedirect           = errors.New("ntfy redirect is not allowed")
	ErrTimeout            = errors.New("ntfy request timed out")
	ErrResponseLost       = errors.New("ntfy response was lost")
	ErrRetryable          = errors.New("ntfy request is retryable")
	ErrPermanent          = errors.New("ntfy request failed permanently")
)

type Destination struct {
	BaseURL        string
	Topic          string
	Token          string
	AllowPlainHTTP bool
}

type Message struct {
	Title    string
	Message  string
	Priority int
	ClickURL string
}

type Result struct {
	Accepted   bool
	StatusCode int
	RemoteID   string
	RetryAfter time.Duration
}

type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Client struct {
	HTTPClient *http.Client
	Resolver   IPResolver
	Dialer     *net.Dialer
}

type publishPayload struct {
	Topic    string `json:"topic"`
	Title    string `json:"title,omitempty"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
	Click    string `json:"click,omitempty"`
}

type publishResponse struct {
	ID string `json:"id"`
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		HTTPClient: httpClient,
		Resolver:   net.DefaultResolver,
		Dialer:     &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second},
	}
}

func ValidateDestination(ctx context.Context, destination Destination) (Destination, error) {
	return NewClient(nil).ValidateDestination(ctx, destination)
}

func (c *Client) ValidateDestination(ctx context.Context, destination Destination) (Destination, error) {
	if c == nil {
		c = NewClient(nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(destination.BaseURL) == 0 || len(destination.BaseURL) > maxURLBytes {
		return Destination{}, fmt.Errorf("%w: base URL length", ErrInvalidDestination)
	}
	parsed, err := parseBaseURL(destination.BaseURL)
	if err != nil {
		return Destination{}, err
	}
	destination.BaseURL = parsed.String()
	destination.Topic = strings.TrimSpace(destination.Topic)
	if err := ValidateTopic(destination.Topic); err != nil {
		return Destination{}, err
	}
	destination.Token = strings.TrimSpace(destination.Token)
	if len(destination.Token) > maxTokenBytes || strings.ContainsAny(destination.Token, "\r\n") {
		return Destination{}, fmt.Errorf("%w: token", ErrInvalidDestination)
	}
	addresses, err := c.lookupAndValidate(ctx, parsed, destination.AllowPlainHTTP)
	if err != nil {
		return Destination{}, err
	}
	if len(addresses) == 0 {
		return Destination{}, fmt.Errorf("%w: no resolved addresses", ErrAddressNotAllowed)
	}
	return destination, nil
}

func ValidateTopic(topic string) error {
	topic = strings.TrimSpace(topic)
	if len(topic) == 0 || len(topic) > 128 {
		return fmt.Errorf("%w: topic length", ErrInvalidDestination)
	}
	for _, char := range topic {
		if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return fmt.Errorf("%w: topic characters", ErrInvalidDestination)
	}
	if _, reserved := reservedTopics[strings.ToLower(topic)]; reserved {
		return fmt.Errorf("%w: reserved topic", ErrInvalidDestination)
	}
	return nil
}

func (d Destination) MaskedTopic() string {
	topic := d.Topic
	if len(topic) <= 4 {
		return strings.Repeat("*", len(topic))
	}
	return topic[:2] + strings.Repeat("*", len(topic)-4) + topic[len(topic)-2:]
}

func (d Destination) HasToken() bool {
	return d.Token != ""
}

func (d Destination) String() string {
	return fmt.Sprintf("ntfy destination %s topic=%s token=%t", d.BaseURL, d.MaskedTopic(), d.HasToken())
}

func (c *Client) Publish(ctx context.Context, destination Destination, message Message) (Result, error) {
	if c == nil {
		c = NewClient(nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := parseBaseURL(destination.BaseURL)
	if err != nil {
		return Result{}, err
	}
	destination.BaseURL = parsed.String()
	destination.Topic = strings.TrimSpace(destination.Topic)
	if err := ValidateTopic(destination.Topic); err != nil {
		return Result{}, err
	}
	destination.Token = strings.TrimSpace(destination.Token)
	if len(destination.Token) > maxTokenBytes || strings.ContainsAny(destination.Token, "\r\n") {
		return Result{}, fmt.Errorf("%w: token", ErrInvalidDestination)
	}
	addresses, err := c.lookupAndValidate(ctx, parsed, destination.AllowPlainHTTP)
	if err != nil {
		return Result{}, err
	}
	if len(addresses) == 0 {
		return Result{}, fmt.Errorf("%w: no resolved addresses", ErrAddressNotAllowed)
	}
	payload, err := encodePayload(destination.Topic, message)
	if err != nil {
		return Result{}, err
	}
	endpoint := appendTopic(parsed, destination.Topic)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return Result{}, fmt.Errorf("%w: request URL", ErrInvalidDestination)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if destination.Token != "" {
		request.Header.Set("Authorization", "Bearer "+destination.Token)
	}

	httpClient, err := c.httpClient(parsed, addresses)
	if err != nil {
		return Result{}, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		if errors.Is(err, ErrRedirect) {
			return Result{}, err
		}
		if isTimeout(err) {
			return Result{}, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return Result{}, fmt.Errorf("%w: %v", ErrResponseLost, err)
	}
	defer response.Body.Close()
	body, bodyErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	result := Result{StatusCode: response.StatusCode, RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now().UTC())}
	if bodyErr != nil {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return result, fmt.Errorf("%w: %w: %v", ErrRetryable, ErrResponseLost, bodyErr)
		}
		body = nil
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		result.Accepted = true
		var decoded publishResponse
		if len(body) > 0 && json.Unmarshal(body, &decoded) == nil {
			result.RemoteID = decoded.ID
		}
		return result, nil
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return result, fmt.Errorf("%w: HTTP %d", ErrRedirect, response.StatusCode)
	}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return result, fmt.Errorf("%w: HTTP %d", ErrRetryable, response.StatusCode)
	}
	return result, fmt.Errorf("%w: HTTP %d", ErrPermanent, response.StatusCode)
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("%w: URL must be an absolute credential-free endpoint", ErrInvalidDestination)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("%w: scheme", ErrInvalidDestination)
	}
	if parsed.Hostname() == "" || strings.Contains(parsed.Hostname(), "%") {
		return nil, fmt.Errorf("%w: host", ErrInvalidDestination)
	}
	if _, err := strconv.Atoi(parsed.Port()); parsed.Port() != "" && err != nil {
		return nil, fmt.Errorf("%w: port", ErrInvalidDestination)
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, fmt.Errorf("%w: path traversal", ErrInvalidDestination)
		}
	}
	parsed.Path = normalizePath(parsed.Path)
	parsed.RawPath = ""
	return parsed, nil
}

func normalizePath(value string) string {
	if value == "" || value == "/" {
		return ""
	}
	clean := path.Clean("/" + strings.TrimPrefix(value, "/"))
	if clean == "/" {
		return ""
	}
	return strings.TrimSuffix(clean, "/")
}

func appendTopic(base *url.URL, topic string) string {
	copy := *base
	copy.Path = strings.TrimSuffix(copy.Path, "/") + "/" + topic
	copy.RawPath = ""
	return copy.String()
}

func (c *Client) lookupAndValidate(ctx context.Context, parsed *url.URL, allowPlainHTTP bool) ([]net.IP, error) {
	if parsed.Scheme == "http" && !allowPlainHTTP {
		return nil, fmt.Errorf("%w: plain HTTP requires explicit opt-in", ErrInvalidDestination)
	}
	addresses, err := c.lookup(ctx, parsed.Hostname())
	if err != nil {
		return nil, fmt.Errorf("%w: DNS lookup failed", ErrAddressNotAllowed)
	}
	result := make([]net.IP, 0, len(addresses))
	seen := map[string]struct{}{}
	for _, address := range addresses {
		ip := address.IP
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
			return nil, fmt.Errorf("%w: %s", ErrAddressNotAllowed, address.String())
		}
		if parsed.Scheme == "http" && !ip.IsLoopback() && !ip.IsPrivate() {
			return nil, fmt.Errorf("%w: plain HTTP is limited to private or loopback addresses", ErrAddressNotAllowed)
		}
		key := ip.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, ip)
	}
	return result, nil
}

func (c *Client) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	resolver := c.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return resolver.LookupIPAddr(ctx, host)
}

func (c *Client) httpClient(parsed *url.URL, addresses []net.IP) (*http.Client, error) {
	base := c.HTTPClient
	if base == nil {
		base = NewClient(nil).HTTPClient
	}
	client := *base
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		return fmt.Errorf("%w: %s", ErrRedirect, request.URL.Redacted())
	}
	if transport, ok := base.Transport.(*http.Transport); ok {
		clone := transport.Clone()
		clone.Proxy = nil
		clone.DialContext = c.dialContext(addresses)
		client.Transport = clone
	} else if base.Transport == nil {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("%w: unsupported default transport", ErrInvalidDestination)
		}
		clone := transport.Clone()
		clone.Proxy = nil
		clone.DialContext = c.dialContext(addresses)
		client.Transport = clone
	}
	return &client, nil
}

func (c *Client) dialContext(addresses []net.IP) func(context.Context, string, string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if c.Dialer != nil {
		dialer = *c.Dialer
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid connection address", ErrAddressNotAllowed)
		}
		var lastErr error
		for _, ip := range addresses {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = ErrAddressNotAllowed
		}
		return nil, lastErr
	}
}

func encodePayload(topic string, message Message) ([]byte, error) {
	if message.Priority == 0 {
		message.Priority = 3
	}
	if message.Priority < 1 || message.Priority > 5 {
		return nil, fmt.Errorf("%w: priority", ErrInvalidDestination)
	}
	message.Title = truncateUTF8(strings.TrimSpace(message.Title), maxTitleBytes)
	message.ClickURL = truncateUTF8(strings.TrimSpace(message.ClickURL), maxClickBytes)
	runes := []rune(message.Message)
	low, high := 0, len(runes)
	var best []byte
	for low <= high {
		middle := low + (high-low)/2
		candidate := publishPayload{Topic: topic, Title: message.Title, Message: string(runes[:middle]), Priority: message.Priority, Click: message.ClickURL}
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return nil, fmt.Errorf("%w: message JSON", ErrInvalidDestination)
		}
		if len(encoded) <= MaxPayloadBytes {
			best = encoded
			low = middle + 1
			continue
		}
		high = middle - 1
	}
	if best == nil {
		return nil, fmt.Errorf("%w: message cannot fit the payload limit", ErrInvalidDestination)
	}
	return best, nil
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	used := 0
	result := make([]rune, 0, len(value))
	for _, char := range value {
		size := len(string(char))
		if used+size > maxBytes {
			break
		}
		result = append(result, char)
		used += size
	}
	return string(result)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return minDuration(time.Duration(seconds)*time.Second, maxRetryAfter)
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return minDuration(when.Sub(now), maxRetryAfter)
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

var reservedTopics = map[string]struct{}{
	"access": {}, "attachment": {}, "auth": {}, "config": {}, "docs": {}, "file": {}, "health": {},
	"metrics": {}, "publish": {}, "static": {}, "stats": {}, "subscribe": {}, "token": {}, "v1": {},
}
