// Пакет orders содержит HTTP-клиент внешнего Order Service.
package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"loyaltyledger/internal/domain"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	Created   = "CREATED"
	Paid      = "PAID"
	Completed = "COMPLETED"
	Cancelled = "CANCELLED"
)

type Info struct {
	Number  string       `json:"number"`
	OwnerID int64        `json:"owner_id"`
	Status  string       `json:"status"`
	Amount  domain.Money `json:"amount"`
}
type Client struct {
	baseURL, apiKey string
	http            *http.Client
}

func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}
func (c *Client) GetOrder(ctx context.Context, number string) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/orders/"+url.PathEscape(number), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed", domain.ErrExternalUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, domain.ErrOrderNotFound
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return nil, domain.ErrExternalUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return nil, domain.ErrExternalResponse
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return nil, domain.ErrExternalUnavailable
	}
	if len(data) > 65536 {
		return nil, domain.ErrExternalResponse
	}
	// Указатель позволяет отличить нулевую сумму от отсутствующего поля.
	var payload struct {
		Number  string        `json:"number"`
		OwnerID int64         `json:"owner_id"`
		Status  string        `json:"status"`
		Amount  *domain.Money `json:"amount"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, domain.ErrExternalResponse
	}
	if payload.Number != number || payload.OwnerID <= 0 || payload.Amount == nil {
		return nil, domain.ErrExternalResponse
	}
	switch payload.Status {
	case Created, Paid, Completed, Cancelled:
	default:
		return nil, domain.ErrExternalResponse
	}
	return &Info{Number: payload.Number, OwnerID: payload.OwnerID, Status: payload.Status, Amount: *payload.Amount}, nil
}
