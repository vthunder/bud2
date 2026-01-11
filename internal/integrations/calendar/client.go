package calendar

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	baseURL      = "https://www.googleapis.com/calendar/v3"
	tokenURL     = "https://oauth2.googleapis.com/token"
	tokenExpiry  = 55 * time.Minute // Refresh before 1 hour expiry
)

// Client is a Google Calendar API client using service account authentication
type Client struct {
	httpClient  *http.Client
	calendarID  string
	credentials *serviceAccountCredentials

	// Token caching
	mu          sync.RWMutex
	accessToken string
	tokenExpiry time.Time
}

// serviceAccountCredentials holds the service account JSON key
type serviceAccountCredentials struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientID     string `json:"client_id"`
	AuthURI      string `json:"auth_uri"`
	TokenURI     string `json:"token_uri"`
}

// Config holds calendar client configuration
type Config struct {
	CredentialsFile string // Path to service account JSON file
	CalendarID      string // Calendar ID to access (usually an email address)
}

// NewClient creates a new Google Calendar client from environment variables
func NewClient() (*Client, error) {
	credsFile := os.Getenv("GOOGLE_CALENDAR_CREDENTIALS_FILE")
	if credsFile == "" {
		return nil, fmt.Errorf("GOOGLE_CALENDAR_CREDENTIALS_FILE not set")
	}

	calendarID := os.Getenv("GOOGLE_CALENDAR_ID")
	if calendarID == "" {
		return nil, fmt.Errorf("GOOGLE_CALENDAR_ID not set")
	}

	return NewClientWithConfig(Config{
		CredentialsFile: credsFile,
		CalendarID:      calendarID,
	})
}

// NewClientWithConfig creates a new client with explicit configuration
func NewClientWithConfig(cfg Config) (*Client, error) {
	// Read the service account credentials
	data, err := os.ReadFile(cfg.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("read credentials file: %w", err)
	}

	var creds serviceAccountCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	if creds.Type != "service_account" {
		return nil, fmt.Errorf("credentials file must be a service account key (got %s)", creds.Type)
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		calendarID:  cfg.CalendarID,
		credentials: &creds,
	}, nil
}

// getAccessToken returns a valid access token, refreshing if needed
func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		token := c.accessToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	// Create JWT assertion
	now := time.Now()
	claims := map[string]interface{}{
		"iss":   c.credentials.ClientEmail,
		"scope": "https://www.googleapis.com/auth/calendar.readonly https://www.googleapis.com/auth/calendar.events",
		"aud":   tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}

	jwt, err := c.signJWT(claims)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	// Exchange JWT for access token
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	data.Set("assertion", jwt)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token request failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	c.accessToken = tokenResp.AccessToken
	c.tokenExpiry = now.Add(tokenExpiry)

	return c.accessToken, nil
}

// signJWT creates a signed JWT assertion
func (c *Client) signJWT(claims map[string]interface{}) (string, error) {
	// Parse private key
	block, _ := pem.Decode([]byte(c.credentials.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("failed to parse PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("private key is not RSA")
	}

	// Create header
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}

	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64

	// Sign
	hash := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(nil, rsaKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + signatureB64, nil
}

// request makes an authenticated request to the Calendar API
func (c *Client) request(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("calendar API error (%d): %s", errResp.Error.Code, errResp.Error.Message)
		}
		return nil, fmt.Errorf("calendar API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Event represents a calendar event
type Event struct {
	ID          string            `json:"id"`
	Summary     string            `json:"summary"`
	Description string            `json:"description,omitempty"`
	Location    string            `json:"location,omitempty"`
	Start       time.Time         `json:"start"`
	End         time.Time         `json:"end"`
	AllDay      bool              `json:"all_day"`
	Status      string            `json:"status"` // confirmed, tentative, cancelled
	Organizer   string            `json:"organizer,omitempty"`
	Attendees   []Attendee        `json:"attendees,omitempty"`
	HtmlLink    string            `json:"html_link,omitempty"`
	MeetLink    string            `json:"meet_link,omitempty"`
	Recurrence  []string          `json:"recurrence,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Attendee represents an event attendee
type Attendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"display_name,omitempty"`
	ResponseStatus string `json:"response_status"` // needsAction, declined, tentative, accepted
	Self           bool   `json:"self,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
}

// googleEvent represents the Google Calendar API event format
type googleEvent struct {
	ID           string           `json:"id"`
	Summary      string           `json:"summary"`
	Description  string           `json:"description,omitempty"`
	Location     string           `json:"location,omitempty"`
	Status       string           `json:"status"`
	HtmlLink     string           `json:"htmlLink,omitempty"`
	Recurrence   []string         `json:"recurrence,omitempty"`
	Start        *googleDateTime  `json:"start,omitempty"`
	End          *googleDateTime  `json:"end,omitempty"`
	Organizer    *googlePerson    `json:"organizer,omitempty"`
	Attendees    []googleAttendee `json:"attendees,omitempty"`
	ConferenceData *conferenceData `json:"conferenceData,omitempty"`
	ExtendedProperties *extendedProps `json:"extendedProperties,omitempty"`
}

type googleDateTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type googlePerson struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName,omitempty"`
}

type googleAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Self           bool   `json:"self,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
}

type conferenceData struct {
	EntryPoints []entryPoint `json:"entryPoints,omitempty"`
}

type entryPoint struct {
	EntryPointType string `json:"entryPointType"`
	URI            string `json:"uri"`
}

type extendedProps struct {
	Private map[string]string `json:"private,omitempty"`
}

type eventsResponse struct {
	Items []googleEvent `json:"items"`
}

// ListEventsParams for querying events
type ListEventsParams struct {
	TimeMin      time.Time // Start of time range (required)
	TimeMax      time.Time // End of time range (required)
	MaxResults   int       // Max events to return (default 100)
	SingleEvents bool      // Expand recurring events (default true)
	OrderBy      string    // "startTime" or "updated" (default "startTime")
	Query        string    // Free text search
}

// ListEvents retrieves events in the specified time range
func (c *Client) ListEvents(ctx context.Context, params ListEventsParams) ([]Event, error) {
	if params.MaxResults == 0 {
		params.MaxResults = 100
	}
	if params.OrderBy == "" {
		params.OrderBy = "startTime"
	}

	queryParams := url.Values{}
	queryParams.Set("timeMin", params.TimeMin.Format(time.RFC3339))
	queryParams.Set("timeMax", params.TimeMax.Format(time.RFC3339))
	queryParams.Set("maxResults", fmt.Sprintf("%d", params.MaxResults))
	queryParams.Set("singleEvents", "true")
	queryParams.Set("orderBy", params.OrderBy)
	if params.Query != "" {
		queryParams.Set("q", params.Query)
	}

	path := fmt.Sprintf("/calendars/%s/events?%s", url.PathEscape(c.calendarID), queryParams.Encode())
	data, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp eventsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse events response: %w", err)
	}

	events := make([]Event, 0, len(resp.Items))
	for _, item := range resp.Items {
		event, err := convertEvent(&item)
		if err != nil {
			continue // Skip malformed events
		}
		events = append(events, event)
	}

	return events, nil
}

// GetEvent retrieves a specific event by ID
func (c *Client) GetEvent(ctx context.Context, eventID string) (*Event, error) {
	path := fmt.Sprintf("/calendars/%s/events/%s", url.PathEscape(c.calendarID), url.PathEscape(eventID))
	data, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var item googleEvent
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("parse event: %w", err)
	}

	event, err := convertEvent(&item)
	if err != nil {
		return nil, err
	}

	return &event, nil
}

// GetUpcomingEvents retrieves events in the next duration
func (c *Client) GetUpcomingEvents(ctx context.Context, duration time.Duration, maxResults int) ([]Event, error) {
	now := time.Now()
	return c.ListEvents(ctx, ListEventsParams{
		TimeMin:    now,
		TimeMax:    now.Add(duration),
		MaxResults: maxResults,
	})
}

// GetTodayEvents retrieves all events for today
func (c *Client) GetTodayEvents(ctx context.Context) ([]Event, error) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	return c.ListEvents(ctx, ListEventsParams{
		TimeMin: startOfDay,
		TimeMax: endOfDay,
	})
}

// FreeBusyParams for checking availability
type FreeBusyParams struct {
	TimeMin time.Time
	TimeMax time.Time
}

// BusyPeriod represents a period when the calendar is busy
type BusyPeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// FreeBusy checks availability for the calendar
func (c *Client) FreeBusy(ctx context.Context, params FreeBusyParams) ([]BusyPeriod, error) {
	reqBody := map[string]interface{}{
		"timeMin": params.TimeMin.Format(time.RFC3339),
		"timeMax": params.TimeMax.Format(time.RFC3339),
		"items": []map[string]string{
			{"id": c.calendarID},
		},
	}

	data, err := c.request(ctx, "POST", "/freeBusy", reqBody)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse freebusy response: %w", err)
	}

	calendar, ok := resp.Calendars[c.calendarID]
	if !ok {
		return nil, fmt.Errorf("calendar not found in response")
	}

	periods := make([]BusyPeriod, 0, len(calendar.Busy))
	for _, busy := range calendar.Busy {
		start, _ := time.Parse(time.RFC3339, busy.Start)
		end, _ := time.Parse(time.RFC3339, busy.End)
		periods = append(periods, BusyPeriod{
			Start: start,
			End:   end,
		})
	}

	return periods, nil
}

// CreateEventParams for creating a new event
type CreateEventParams struct {
	Summary     string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	AllDay      bool
	Attendees   []string // Email addresses
}

// CreateEvent creates a new calendar event
func (c *Client) CreateEvent(ctx context.Context, params CreateEventParams) (*Event, error) {
	event := map[string]interface{}{
		"summary":     params.Summary,
		"description": params.Description,
		"location":    params.Location,
	}

	if params.AllDay {
		event["start"] = map[string]string{
			"date": params.Start.Format("2006-01-02"),
		}
		event["end"] = map[string]string{
			"date": params.End.Format("2006-01-02"),
		}
	} else {
		event["start"] = map[string]string{
			"dateTime": params.Start.Format(time.RFC3339),
			"timeZone": params.Start.Location().String(),
		}
		event["end"] = map[string]string{
			"dateTime": params.End.Format(time.RFC3339),
			"timeZone": params.End.Location().String(),
		}
	}

	if len(params.Attendees) > 0 {
		attendees := make([]map[string]string, len(params.Attendees))
		for i, email := range params.Attendees {
			attendees[i] = map[string]string{"email": email}
		}
		event["attendees"] = attendees
	}

	path := fmt.Sprintf("/calendars/%s/events", url.PathEscape(c.calendarID))
	data, err := c.request(ctx, "POST", path, event)
	if err != nil {
		return nil, err
	}

	var item googleEvent
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("parse created event: %w", err)
	}

	result, err := convertEvent(&item)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// CalendarID returns the configured calendar ID
func (c *Client) CalendarID() string {
	return c.calendarID
}

// convertEvent converts a Google Calendar event to our Event type
func convertEvent(item *googleEvent) (Event, error) {
	event := Event{
		ID:          item.ID,
		Summary:     item.Summary,
		Description: item.Description,
		Location:    item.Location,
		Status:      item.Status,
		HtmlLink:    item.HtmlLink,
		Recurrence:  item.Recurrence,
	}

	// Parse start time
	if item.Start != nil {
		if item.Start.DateTime != "" {
			t, err := time.Parse(time.RFC3339, item.Start.DateTime)
			if err != nil {
				return Event{}, fmt.Errorf("parse start time: %w", err)
			}
			event.Start = t
		} else if item.Start.Date != "" {
			t, err := time.Parse("2006-01-02", item.Start.Date)
			if err != nil {
				return Event{}, fmt.Errorf("parse start date: %w", err)
			}
			event.Start = t
			event.AllDay = true
		}
	}

	// Parse end time
	if item.End != nil {
		if item.End.DateTime != "" {
			t, err := time.Parse(time.RFC3339, item.End.DateTime)
			if err != nil {
				return Event{}, fmt.Errorf("parse end time: %w", err)
			}
			event.End = t
		} else if item.End.Date != "" {
			t, err := time.Parse("2006-01-02", item.End.Date)
			if err != nil {
				return Event{}, fmt.Errorf("parse end date: %w", err)
			}
			event.End = t
		}
	}

	// Extract organizer
	if item.Organizer != nil {
		if item.Organizer.DisplayName != "" {
			event.Organizer = item.Organizer.DisplayName
		} else {
			event.Organizer = item.Organizer.Email
		}
	}

	// Extract attendees
	if len(item.Attendees) > 0 {
		event.Attendees = make([]Attendee, len(item.Attendees))
		for i, a := range item.Attendees {
			event.Attendees[i] = Attendee{
				Email:          a.Email,
				DisplayName:    a.DisplayName,
				ResponseStatus: a.ResponseStatus,
				Self:           a.Self,
				Organizer:      a.Organizer,
			}
		}
	}

	// Extract Google Meet link
	if item.ConferenceData != nil {
		for _, entry := range item.ConferenceData.EntryPoints {
			if entry.EntryPointType == "video" {
				event.MeetLink = entry.URI
				break
			}
		}
	}

	// Extract extended properties as metadata
	if item.ExtendedProperties != nil && item.ExtendedProperties.Private != nil {
		event.Metadata = item.ExtendedProperties.Private
	}

	return event, nil
}

// FormatEventSummary returns a human-readable summary of an event
func (e *Event) FormatEventSummary() string {
	timeStr := e.Start.Format("3:04 PM")
	if e.AllDay {
		timeStr = "All day"
	}

	summary := fmt.Sprintf("%s - %s", timeStr, e.Summary)
	if e.Location != "" {
		summary += fmt.Sprintf(" @ %s", e.Location)
	}
	if e.MeetLink != "" {
		summary += " (has video call)"
	}

	return summary
}

// Duration returns the event duration
func (e *Event) Duration() time.Duration {
	return e.End.Sub(e.Start)
}

// IsHappeningSoon returns true if the event starts within the given duration
func (e *Event) IsHappeningSoon(within time.Duration) bool {
	return time.Until(e.Start) <= within && time.Until(e.Start) > 0
}

// IsHappeningNow returns true if the event is currently in progress
func (e *Event) IsHappeningNow() bool {
	now := time.Now()
	return now.After(e.Start) && now.Before(e.End)
}

// ToJSON returns the event as a JSON string
func (e *Event) ToJSON() string {
	data, _ := json.MarshalIndent(e, "", "  ")
	return string(data)
}
