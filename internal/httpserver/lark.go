package httpserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/larkutil"
)

type LarkResponder interface {
	HandleLarkEvent(context.Context, domain.LarkMessageEvent) error
	HandleLarkAction(context.Context, larkutil.ActionRequest) error
}

type LarkHandler struct {
	verificationToken string
	appSecret         string
	responder         LarkResponder
}

func NewLarkHandler(verificationToken, appSecret string, responder LarkResponder) *LarkHandler {
	return &LarkHandler{verificationToken: verificationToken, appSecret: appSecret, responder: responder}
}

func (h *LarkHandler) Events(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.verify(r, body); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var envelope struct {
		Challenge string `json:"challenge"`
		Token     string `json:"token"`
		Type      string `json:"type"`
		Event     struct {
			Sender struct {
				SenderID struct {
					OpenID string `json:"open_id"`
				} `json:"sender_id"`
			} `json:"sender"`
			Message struct {
				MessageID string `json:"message_id"`
				ChatID    string `json:"chat_id"`
				Content   string `json:"content"`
			} `json:"message"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if envelope.Challenge != "" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": envelope.Challenge})
		return
	}
	if h.verificationToken != "" && envelope.Token != "" && envelope.Token != h.verificationToken {
		http.Error(w, "invalid verification token", http.StatusUnauthorized)
		return
	}
	text := larkutil.ExtractText(envelope.Event.Message.Content)
	if err := h.responder.HandleLarkEvent(r.Context(), domain.LarkMessageEvent{
		SenderOpenID: envelope.Event.Sender.SenderID.OpenID,
		ChatID:       envelope.Event.Message.ChatID,
		Text:         text,
		MessageID:    envelope.Event.Message.MessageID,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"code": "ok"})
}

func (h *LarkHandler) Actions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.verify(r, body); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var payload struct {
		Token    string `json:"token"`
		OpenID   string `json:"open_id"`
		Operator struct {
			OpenID string `json:"open_id"`
		} `json:"operator"`
		Action struct {
			Value map[string]string `json:"value"`
		} `json:"action"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.verificationToken != "" && payload.Token != "" && payload.Token != h.verificationToken {
		http.Error(w, "invalid verification token", http.StatusUnauthorized)
		return
	}
	openID := payload.Operator.OpenID
	if openID == "" {
		openID = payload.OpenID
	}
	if err := h.responder.HandleLarkAction(r.Context(), larkutil.ActionRequest{
		JobID:          payload.Action.Value["job_id"],
		Decision:       payload.Action.Value["decision"],
		ApproverOpenID: openID,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"toast": "received"})
}

func (h *LarkHandler) verify(r *http.Request, body []byte) error {
	if h.appSecret == "" {
		return nil
	}
	timestamp := r.Header.Get("X-Lark-Request-Timestamp")
	signature := r.Header.Get("X-Lark-Signature")
	if timestamp == "" || signature == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(h.appSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte(body))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(signature)), []byte(strings.ToLower(expected))) {
		return fmt.Errorf("invalid signature")
	}
	if ts, err := parseLarkTimestamp(timestamp); err == nil {
		if time.Since(ts) > 10*time.Minute {
			return fmt.Errorf("stale request")
		}
	}
	return nil
}

func parseLarkTimestamp(value string) (time.Time, error) {
	if unixSeconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(unixSeconds, 0).UTC(), nil
	}
	return time.Parse(time.RFC3339, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
