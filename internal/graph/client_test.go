package graph

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockTokenProvider struct {
	token string
}

func (m *mockTokenProvider) GetAccessToken(ctx context.Context) (string, error) {
	return m.token, nil
}

func TestListFolders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/me/mailFolders" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			t.Errorf("missing or invalid Authorization header: %s", r.Header.Get("Authorization"))
		}

		jsonResp := `{
			"value": [
				{"id": "inbox", "displayName": "Inbox", "childFolderCount": 0, "unreadItemCount": 5, "totalItemCount": 10},
				{"id": "sentitems", "displayName": "Sent Items", "childFolderCount": 0, "unreadItemCount": 0, "totalItemCount": 8}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(jsonResp))
	}))
	defer server.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, server.URL)

	folders, err := client.ListFolders(context.Background())
	if err != nil {
		t.Fatalf("ListFolders failed: %v", err)
	}

	if len(folders) != 2 {
		t.Fatalf("expected 2 folders, got %d", len(folders))
	}
	if folders[0].ID != "inbox" || folders[0].UnreadItemCount != 5 {
		t.Errorf("unexpected folder data: %+v", folders[0])
	}
}

func TestListMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/me/mailFolders/inbox/messages") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		jsonResp := `{
			"value": [
				{"id": "msg1", "subject": "Test Email 1", "isRead": false, "parentFolderId": "inbox"},
				{"id": "msg2", "subject": "Test Email 2", "isRead": true, "parentFolderId": "inbox"}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(jsonResp))
	}))
	defer server.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, server.URL)

	messages, err := client.ListMessages(context.Background(), "inbox", 10, 0)
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].ID != "msg1" || messages[0].Subject != "Test Email 1" || messages[0].IsRead {
		t.Errorf("unexpected message data: %+v", messages[0])
	}
}

func TestFetchMIME(t *testing.T) {
	rawMIMEContent := "From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Hello\r\n\r\nHello World!"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/me/messages/msg123/$value" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "message/rfc822")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawMIMEContent))
	}))
	defer server.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, server.URL)

	mimeData, err := client.FetchMIME(context.Background(), "msg123")
	if err != nil {
		t.Fatalf("FetchMIME failed: %v", err)
	}

	if string(mimeData) != rawMIMEContent {
		t.Errorf("expected MIME data %q, got %q", rawMIMEContent, string(mimeData))
	}
}

func TestUpdateMessageFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/me/messages/msg123" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"isRead":true`) {
			t.Errorf("expected payload to contain isRead:true, got %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, server.URL)

	isRead := true
	if err := client.UpdateMessageFlags(context.Background(), "msg123", &isRead, nil); err != nil {
		t.Fatalf("UpdateMessageFlags failed: %v", err)
	}
}

func TestDeleteMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/me/messages/msg123" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, server.URL)

	if err := client.DeleteMessage(context.Background(), "msg123"); err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}
}

func TestSendMail(t *testing.T) {
	rawMIME := []byte("From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Test Send\r\n\r\nTest Body")
	expectedBase64 := base64.StdEncoding.EncodeToString(rawMIME)

	step := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if step == 0 {
			if r.Method != http.MethodPost || r.URL.Path != "/me/messages" {
				t.Errorf("step 0: unexpected request %s %s", r.Method, r.URL.Path)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			bodyBytes, _ := io.ReadAll(r.Body)
			if string(bodyBytes) != expectedBase64 {
				t.Errorf("step 0: expected base64 MIME body %q, got %q", expectedBase64, string(bodyBytes))
			}
			step++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": "draft_msg_999"}`))
			return
		}

		if step == 1 {
			if r.Method != http.MethodPost || r.URL.Path != "/me/messages/draft_msg_999/send" {
				t.Errorf("step 1: unexpected request %s %s", r.Method, r.URL.Path)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			step++
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}))
	defer server.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, server.URL)

	if err := client.SendMail(context.Background(), rawMIME); err != nil {
		t.Fatalf("SendMail failed: %v", err)
	}
	if step != 2 {
		t.Errorf("expected 2 steps to complete in SendMail flow, completed %d", step)
	}
}

func TestErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"code": "InvalidAuthenticationToken", "message": "Access token is invalid."}}`))
	}))
	defer server.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, server.URL)

	_, err := client.ListFolders(context.Background())
	if err == nil {
		t.Fatalf("expected error on 401 Unauthorized, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "InvalidAuthenticationToken") {
		t.Errorf("unexpected error format: %v", err)
	}
}

func TestListAllMessagesPagination(t *testing.T) {
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/me/mailFolders/inbox/messages" {
			// First page with @odata.nextLink
			nextURL := mockServer.URL + "/me/mailFolders/inbox/messages/page2"
			resp := `{
				"value": [
					{"id": "msg1", "subject": "Page 1 - Email 1"},
					{"id": "msg2", "subject": "Page 1 - Email 2"}
				],
				"@odata.nextLink": "` + nextURL + `"
			}`
			_, _ = w.Write([]byte(resp))
			return
		}
		if r.URL.Path == "/me/mailFolders/inbox/messages/page2" {
			// Second page without @odata.nextLink
			resp := `{
				"value": [
					{"id": "msg3", "subject": "Page 2 - Email 3"}
				]
			}`
			_, _ = w.Write([]byte(resp))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer mockServer.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, mockServer.URL)

	messages, err := client.ListAllMessages(context.Background(), "inbox")
	if err != nil {
		t.Fatalf("ListAllMessages failed: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages across 2 pages, got %d", len(messages))
	}
	if messages[2].ID != "msg3" || messages[2].Subject != "Page 2 - Email 3" {
		t.Errorf("unexpected message on page 2: %+v", messages[2])
	}
}

func TestListFoldersRecursive(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/me/mailFolders" {
			resp := `{
				"value": [
					{"id": "folder_inbox", "displayName": "Inbox", "childFolderCount": 0},
					{
						"id": "folder_custom",
						"displayName": "BrightSpace",
						"childFolderCount": 1,
						"childFolders": [
							{"id": "folder_bs_sub", "displayName": "Assignments", "childFolderCount": 0}
						]
					}
				]
			}`
			_, _ = w.Write([]byte(resp))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer mockServer.Close()

	tp := &mockTokenProvider{token: "test-access-token"}
	client := NewClient(tp, mockServer.URL)

	folders, err := client.ListFolders(context.Background())
	if err != nil {
		t.Fatalf("ListFolders failed: %v", err)
	}

	if len(folders) != 3 {
		t.Fatalf("expected 3 folders (including child folder), got %d", len(folders))
	}

	foundBS := false
	foundSub := false
	for _, f := range folders {
		if f.DisplayName == "BrightSpace" {
			foundBS = true
		}
		if f.DisplayName == "Assignments" {
			foundSub = true
		}
	}

	if !foundBS || !foundSub {
		t.Errorf("expected BrightSpace and Assignments folders, foundBS=%v, foundSub=%v", foundBS, foundSub)
	}
}
