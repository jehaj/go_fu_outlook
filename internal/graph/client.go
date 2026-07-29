package graph

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"graph-mail-proxy/internal/auth"
)

type Client struct {
	baseURL    string
	authMgr    TokenProvider
	httpClient *http.Client
}

type TokenProvider interface {
	GetAccessToken(ctx context.Context) (string, error)
}

type Folder struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	ParentFolderID   string `json:"parentFolderId,omitempty"`
	ChildFolderCount int    `json:"childFolderCount"`
	UnreadItemCount  int    `json:"unreadItemCount"`
	TotalItemCount   int    `json:"totalItemCount"`
	WellKnownName    string `json:"wellKnownName,omitempty"`
}

type Message struct {
	ID                   string         `json:"id"`
	Subject              string         `json:"subject"`
	BodyPreview          string         `json:"bodyPreview"`
	CreatedDateTime      time.Time      `json:"createdDateTime"`
	LastModifiedDateTime time.Time      `json:"lastModifiedDateTime"`
	SentDateTime         time.Time      `json:"sentDateTime"`
	ReceivedDateTime     time.Time      `json:"receivedDateTime"`
	HasAttachments       bool           `json:"hasAttachments"`
	IsRead               bool           `json:"isRead"`
	IsDraft              bool           `json:"isDraft"`
	ParentFolderID       string         `json:"parentFolderId"`
	Sender               *Recipient     `json:"sender,omitempty"`
	From                 *Recipient     `json:"from,omitempty"`
	ToRecipients         []Recipient    `json:"toRecipients,omitempty"`
	CcRecipients         []Recipient    `json:"ccRecipients,omitempty"`
	BccRecipients        []Recipient    `json:"bccRecipients,omitempty"`
	InternetMessageID    string         `json:"internetMessageId,omitempty"`
	ODataRemoved         *RemovedReason `json:"@removed,omitempty"`
}

type RemovedReason struct {
	Reason string `json:"reason"`
}

type Recipient struct {
	EmailAddress EmailAddress `json:"emailAddress"`
}

type EmailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type FolderListResponse struct {
	Value    []Folder `json:"value"`
	NextLink string   `json:"@odata.nextLink,omitempty"`
}

type MessageListResponse struct {
	Value     []Message `json:"value"`
	NextLink  string    `json:"@odata.nextLink,omitempty"`
	DeltaLink string    `json:"@odata.deltaLink,omitempty"`
}

type DeltaResult struct {
	Messages  []Message
	NextLink  string
	DeltaLink string
}

const DefaultBaseURL = "https://graph.microsoft.com/v1.0"

func NewClient(authMgr TokenProvider, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		authMgr:    authMgr,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	token, err := c.authMgr.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth error obtaining access token: %w", err)
	}

	var reqURL string
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		reqURL = endpoint
	} else {
		reqURL = fmt.Sprintf("%s%s", c.baseURL, endpoint)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		sanitizedBody := auth.Redact(string(bodyBytes))
		return nil, fmt.Errorf("graph API error HTTP %d: %s", resp.StatusCode, sanitizedBody)
	}

	return resp, nil
}

// ListFolders fetches mail folders for the current user.
func (c *Client) ListFolders(ctx context.Context) ([]Folder, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/me/mailFolders?$top=250", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result FolderListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode folder list response: %w", err)
	}

	return result.Value, nil
}

// GetFolder retrieves a single folder by ID or well-known name (inbox, sentitems, drafts, deleteditems).
func (c *Client) GetFolder(ctx context.Context, folderID string) (*Folder, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/me/mailFolders/%s", url.PathEscape(folderID)), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var folder Folder
	if err := json.NewDecoder(resp.Body).Decode(&folder); err != nil {
		return nil, fmt.Errorf("failed to decode folder response: %w", err)
	}

	return &folder, nil
}

// ListMessages fetches messages in a folder with pagination.
func (c *Client) ListMessages(ctx context.Context, folderID string, top int, skip int) ([]Message, error) {
	if top <= 0 {
		top = 50
	}
	endpoint := fmt.Sprintf("/me/mailFolders/%s/messages?$top=%d&$skip=%d&$select=id,subject,bodyPreview,createdDateTime,lastModifiedDateTime,sentDateTime,receivedDateTime,hasAttachments,isRead,isDraft,parentFolderId,sender,from,toRecipients,ccRecipients,bccRecipients,internetMessageId", url.PathEscape(folderID), top, skip)

	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result MessageListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode message list response: %w", err)
	}

	return result.Value, nil
}

// GetMessageDelta performs a delta query to retrieve changed/deleted messages since deltaLink.
func (c *Client) GetMessageDelta(ctx context.Context, folderID string, deltaLink string) (*DeltaResult, error) {
	var endpoint string
	if deltaLink != "" {
		endpoint = deltaLink
	} else {
		endpoint = fmt.Sprintf("/me/mailFolders/%s/messages/delta?$select=id,subject,isRead,isDraft,lastModifiedDateTime,parentFolderId", url.PathEscape(folderID))
	}

	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result MessageListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode message delta response: %w", err)
	}

	return &DeltaResult{
		Messages:  result.Value,
		NextLink:  result.NextLink,
		DeltaLink: result.DeltaLink,
	}, nil
}

// FetchMIME downloads the full RFC822 raw MIME content of a message.
func (c *Client) FetchMIME(ctx context.Context, messageID string) ([]byte, error) {
	endpoint := fmt.Sprintf("/me/messages/%s/$value", url.PathEscape(messageID))
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	mimeBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read raw MIME stream: %w", err)
	}

	return mimeBytes, nil
}

// UpdateMessageFlags updates message read or draft status.
func (c *Client) UpdateMessageFlags(ctx context.Context, messageID string, isRead *bool, isDraft *bool) error {
	payload := make(map[string]interface{})
	if isRead != nil {
		payload["isRead"] = *isRead
	}
	if isDraft != nil {
		payload["isDraft"] = *isDraft
	}

	if len(payload) == 0 {
		return nil
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("/me/messages/%s", url.PathEscape(messageID))
	req, err := c.newRequest(ctx, http.MethodPatch, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// DeleteMessage deletes a message from Graph.
func (c *Client) DeleteMessage(ctx context.Context, messageID string) error {
	endpoint := fmt.Sprintf("/me/messages/%s", url.PathEscape(messageID))
	req, err := c.newRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// SendMail sends a raw MIME message by uploading a MIME draft and issuing a send command.
func (c *Client) SendMail(ctx context.Context, rawMIME []byte) error {
	// Step 1: Create draft from MIME base64 text/plain request body
	base64MIME := base64.StdEncoding.EncodeToString(rawMIME)
	req, err := c.newRequest(ctx, http.MethodPost, "/me/messages", strings.NewReader(base64MIME))
	if err != nil {
		return fmt.Errorf("failed to create draft send request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("failed to upload raw MIME draft: %w", err)
	}
	defer resp.Body.Close()

	var draft Message
	if err := json.NewDecoder(resp.Body).Decode(&draft); err != nil {
		return fmt.Errorf("failed to decode created draft response: %w", err)
	}

	// Step 2: Issue send on created draft ID
	sendEndpoint := fmt.Sprintf("/me/messages/%s/send", url.PathEscape(draft.ID))
	sendReq, err := c.newRequest(ctx, http.MethodPost, sendEndpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create send request: %w", err)
	}
	sendReq.Header.Set("Content-Length", "0")

	sendResp, err := c.do(sendReq)
	if err != nil {
		return fmt.Errorf("failed to send draft: %w", err)
	}
	sendResp.Body.Close()

	return nil
}
