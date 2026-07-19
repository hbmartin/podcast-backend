// Package push delivers APNs alert notifications for newly published
// episodes. The iOS client parses no custom payload keys: a plain alert
// (title/body/sound) is displayed as-is and triggers a refresh, so that is
// the entire wire contract.
package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Notification is the visible alert content. Category, CollapseID and Data
// (Slice 8) are optional: zero values leave the wire payload exactly as the
// original title/body contract, which new-episode sends still use.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`

	// Category maps to aps.category — the iOS tap-dispatch key.
	Category string `json:"-"`
	// CollapseID sets the apns-collapse-id header so repeats replace.
	CollapseID string `json:"-"`
	// Data is flattened into top-level custom payload keys.
	Data map[string]string `json:"-"`
}

// Sender delivers one notification to one device token.
type Sender interface {
	Send(ctx context.Context, deviceToken string, n Notification) error
}

// ErrUnregistered reports that APNs no longer recognizes the device token;
// callers should drop the token.
var ErrUnregistered = errors.New("push: device token unregistered")

// DefaultEndpoint is the production APNs host. Use
// "https://api.sandbox.push.apple.com" for development builds.
const DefaultEndpoint = "https://api.push.apple.com"

// Apple accepts provider JWTs between 20 and 60 minutes old; refresh at 40.
const tokenLifetime = 40 * time.Minute

// Client is a token-based (p8 key) APNs provider over HTTP/2.
type Client struct {
	endpoint string
	topic    string
	keyID    string
	teamID   string
	key      *ecdsa.PrivateKey
	http     *http.Client

	mu        sync.Mutex
	jwt       string
	jwtIssued time.Time
}

// NewClient builds a client from a PEM-encoded PKCS#8 EC private key (the
// contents of an Apple .p8 auth key). endpoint defaults to production.
func NewClient(keyPEM []byte, keyID, teamID, topic, endpoint string) (*Client, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("push: APNs key is not PEM encoded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("push: parsing APNs key: %w", err)
	}
	ecKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("push: APNs key is not an EC key")
	}

	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	return &Client{
		endpoint: endpoint,
		topic:    topic,
		keyID:    keyID,
		teamID:   teamID,
		key:      ecKey,
		http:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// NewClientFromFile reads the .p8 key from disk.
func NewClientFromFile(keyFile, keyID, teamID, topic, endpoint string) (*Client, error) {
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("push: reading APNs key: %w", err)
	}
	return NewClient(keyPEM, keyID, teamID, topic, endpoint)
}

func (c *Client) bearer() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.jwt != "" && time.Since(c.jwtIssued) < tokenLifetime {
		return c.jwt, nil
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": c.teamID,
		"iat": now.Unix(),
	})
	token.Header["kid"] = c.keyID

	signed, err := token.SignedString(c.key)
	if err != nil {
		return "", fmt.Errorf("push: signing provider token: %w", err)
	}
	c.jwt, c.jwtIssued = signed, now
	return signed, nil
}

// Send posts one alert. It maps APNs 410/Unregistered/BadDeviceToken
// responses to ErrUnregistered.
func (c *Client) Send(ctx context.Context, deviceToken string, n Notification) error {
	bearer, err := c.bearer()
	if err != nil {
		return err
	}

	aps := map[string]any{
		"alert": n,
		"sound": "default",
	}
	if n.Category != "" {
		aps["category"] = n.Category
	}
	payloadBody := map[string]any{"aps": aps}
	for key, value := range n.Data {
		payloadBody[key] = value
	}
	payload, err := json.Marshal(payloadBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/3/device/"+deviceToken, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+bearer)
	req.Header.Set("apns-topic", c.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10))
	if n.CollapseID != "" {
		req.Header.Set("apns-collapse-id", n.CollapseID)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("push: delivering to APNs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	var apnsErr struct {
		Reason string `json:"reason"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	_ = json.Unmarshal(body, &apnsErr)

	if resp.StatusCode == http.StatusGone || apnsErr.Reason == "Unregistered" || apnsErr.Reason == "BadDeviceToken" {
		return ErrUnregistered
	}
	return fmt.Errorf("push: APNs rejected the notification: status %d reason %q", resp.StatusCode, apnsErr.Reason)
}
