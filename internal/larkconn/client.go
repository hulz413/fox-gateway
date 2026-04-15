package larkconn

import (
	"context"
	"fmt"
	"log"
	"strings"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/larkutil"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkcallback "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type Responder interface {
	HandleLarkEvent(context.Context, domain.LarkMessageEvent) error
	HandleLarkAction(context.Context, larkutil.ActionRequest) error
}

type Client struct {
	wsClient *larkws.Client
	logger   *log.Logger
}

func New(appID, appSecret string, responder Responder, logger *log.Logger) *Client {
	sdkLogger := sdkLogAdapter{logger: logger}

	dispatcher := larkdispatcher.NewEventDispatcher("", "")
	dispatcher.InitConfig(
		larkevent.WithLogger(sdkLogger),
		larkevent.WithLogLevel(larkcore.LogLevelInfo),
		larkevent.WithSkipSignVerify(true),
	)

	handleMessage := func(ctx context.Context, senderOpenID, chatID, messageID, content string) error {
		if responder == nil {
			return nil
		}
		text := larkutil.ExtractText(content)
		if logger != nil {
			logger.Printf("received Feishu message event: chat=%s sender=%s message=%s text=%q", chatID, senderOpenID, messageID, text)
		}
		return responder.HandleLarkEvent(ctx, domain.LarkMessageEvent{
			SenderOpenID: senderOpenID,
			ChatID:       chatID,
			Text:         text,
			MessageID:    messageID,
		})
	}

	dispatcher.OnP1MessageReceiveV1(func(ctx context.Context, event *larkim.P1MessageReceiveV1) error {
		if event == nil || event.Event == nil {
			return nil
		}
		return handleMessage(ctx, event.Event.OpenID, event.Event.OpenChatID, event.Event.OpenMessageID, event.Event.Text)
	})

	dispatcher.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		if event == nil || event.Event == nil {
			return nil
		}
		var senderOpenID, chatID, messageID, content string
		if event.Event.Sender != nil && event.Event.Sender.SenderId != nil && event.Event.Sender.SenderId.OpenId != nil {
			senderOpenID = *event.Event.Sender.SenderId.OpenId
		}
		if event.Event.Message != nil {
			if event.Event.Message.ChatId != nil {
				chatID = *event.Event.Message.ChatId
			}
			if event.Event.Message.MessageId != nil {
				messageID = *event.Event.Message.MessageId
			}
			if event.Event.Message.Content != nil {
				content = *event.Event.Message.Content
			}
		}
		return handleMessage(ctx, senderOpenID, chatID, messageID, content)
	})

	dispatcher.OnP2CardActionTrigger(func(ctx context.Context, event *larkcallback.CardActionTriggerEvent) (*larkcallback.CardActionTriggerResponse, error) {
		resp := &larkcallback.CardActionTriggerResponse{}
		if responder == nil || event == nil || event.Event == nil {
			return resp, nil
		}
		request := larkutil.ActionRequest{}
		if event.Event.Operator != nil {
			request.ApproverOpenID = event.Event.Operator.OpenID
		}
		if event.Event.Action != nil {
			request.JobID = stringify(event.Event.Action.Value["job_id"])
			request.Decision = stringify(event.Event.Action.Value["decision"])
		}
		if logger != nil {
			logger.Printf("received Feishu card action: approver=%s job=%s decision=%s", request.ApproverOpenID, request.JobID, request.Decision)
		}
		if err := responder.HandleLarkAction(ctx, request); err != nil {
			if logger != nil {
				logger.Printf("card action handling error: %v", err)
			}
			resp.Toast = &larkcallback.Toast{Type: "error", Content: err.Error()}
			return resp, nil
		}
		resp.Toast = &larkcallback.Toast{Type: "success", Content: "Action received"}
		return resp, nil
	})

	client := larkws.NewClient(
		appID,
		appSecret,
		larkws.WithEventHandler(dispatcher),
		larkws.WithAutoReconnect(true),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
		larkws.WithLogger(sdkLogger),
	)

	return &Client{wsClient: client, logger: logger}
}

func (c *Client) Start(ctx context.Context) error {
	if c == nil || c.wsClient == nil {
		return fmt.Errorf("websocket client is not initialized")
	}
	if c.logger != nil {
		c.logger.Printf("starting Feishu websocket client")
	}
	return c.wsClient.Start(ctx)
}

type sdkLogAdapter struct {
	logger *log.Logger
}

func (l sdkLogAdapter) Debug(_ context.Context, args ...interface{}) {
	l.logFiltered(args...)
}

func (l sdkLogAdapter) Info(_ context.Context, args ...interface{}) {
	l.logFiltered(args...)
}

func (l sdkLogAdapter) Warn(_ context.Context, args ...interface{}) {
	l.logFiltered(args...)
}

func (l sdkLogAdapter) Error(_ context.Context, args ...interface{}) {
	l.logFiltered(args...)
}

func (l sdkLogAdapter) logFiltered(args ...interface{}) {
	if l.logger == nil {
		return
	}
	message := strings.TrimSpace(fmt.Sprint(args...))
	lower := strings.ToLower(message)
	if strings.Contains(lower, "ping success") || strings.Contains(lower, "receive pong") {
		return
	}
	l.logger.Println(args...)
}

func stringify(v interface{}) string {
	switch value := v.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}
